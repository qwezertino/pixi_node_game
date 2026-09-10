package liveconfig

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"pixi_game_server/internal/config"
)

const (
	updatesChannel = "game_config_updates"
	tableName      = "game_config"

	unitsUpdatesChannel = "units_updates"
)

type Store struct {
	pool  *pgxpool.Pool
	redis *redis.Client
}

func Connect(ctx context.Context) (*Store, error) {
	dsn := postgresDSNFromEnv()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddrFromEnv(),
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		pool.Close()
		rdb.Close()
		return nil, err
	}

	return &Store{pool: pool, redis: rdb}, nil
}

func (s *Store) Close() {
	s.pool.Close()
	s.redis.Close()
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+tableName+` (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	return err
}

func (s *Store) Seed(ctx context.Context, seed map[string]string) error {
	for key, value := range seed {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO `+tableName+` (key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO NOTHING
		`, key, value)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) LoadAll(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM `+tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) getKey(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.pool.QueryRow(ctx, `SELECT value FROM `+tableName+` WHERE key = $1`, key).Scan(&value)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

// SetKey sets a single live config key. See SetKeys for the atomicity and
// recovery guarantees.
func (s *Store) SetKey(ctx context.Context, key, value string) error {
	return s.SetKeys(ctx, map[string]string{key: value})
}

// SetKeys applies patch as one atomic change to the live config: every key
// in patch is validated and persisted together, so related keys (for
// example a min/max pair) can be moved from one valid combination to
// another in a single call instead of racing two separate SetKey calls
// against each other.
//
// Unlike the old SetKey, this does not require every value already stored
// in the database to be valid on its own: it starts from the raw stored
// rows, overlays patch on top, and only then validates the resulting
// snapshot. A row left over from a bad direct SQL edit therefore no longer
// blocks fixing that very row (or any other key) through this API — any
// other still-invalid legacy key is simply left out of the validated
// snapshot and keeps its old on-disk value untouched.
func (s *Store) SetKeys(ctx context.Context, patch map[string]string) error {
	if len(patch) == 0 {
		return nil
	}

	settings, err := s.LoadGameSettings(ctx)
	if err != nil {
		return err
	}
	cfg, _, err := config.Build(settings)
	if err != nil {
		return err
	}
	raw, err := s.LoadAll(ctx)
	if err != nil {
		return err
	}
	if _, err := resolveLiveNetPatch(config.BuildLiveNetConfig(cfg), raw, patch); err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for key, value := range patch {
		if _, err := tx.Exec(ctx, `
			INSERT INTO `+tableName+` (key, value, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
		`, key, value); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	for key := range patch {
		if err := s.redis.Publish(ctx, updatesChannel, key).Err(); err != nil {
			return err
		}
	}
	return nil
}

// resolveLiveNetPatch validates patch against a snapshot built from raw
// stored key/value rows: patch is applied first, atomically, as one group
// (so cross-field invariants between patched keys are enforced together);
// any other raw key is then layered in best-effort, one at a time, and
// silently skipped (keeping baseline's value for it) if it no longer
// validates. This lets a single still-invalid legacy row be fixed, or any
// other key be changed, without that legacy row blocking every write.
func resolveLiveNetPatch(baseline *config.LiveNetConfig, raw map[string]string, patch map[string]string) (*config.LiveNetConfig, error) {
	live := config.NewLiveNet(baseline)

	if err := live.Update(func(c *config.LiveNetConfig) error {
		for key, value := range patch {
			if err := c.ApplyKey(key, value); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	for key, value := range raw {
		if _, patched := patch[key]; patched {
			continue
		}
		key, value := key, value
		if err := live.Update(func(c *config.LiveNetConfig) error { return c.ApplyKey(key, value) }); err != nil {
			slog.Warn("live config: ignoring stored value that no longer validates", "key", key, "value", value, "error", err)
		}
	}

	return live.Load(), nil
}

func (s *Store) LoadInto(ctx context.Context, live *config.LiveNet) error {
	rows, err := s.LoadAll(ctx)
	if err != nil {
		return err
	}
	return live.Update(func(c *config.LiveNetConfig) error {
		for key, value := range rows {
			if err := c.ApplyKey(key, value); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Watch(ctx context.Context, live *config.LiveNet) {
	sub := s.redis.Subscribe(ctx, updatesChannel)
	defer sub.Close()
	ch := sub.Channel()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			key := msg.Payload
			value, found, err := s.getKey(ctx, key)
			if err != nil {
				slog.Error("live config: failed to reload key after notification", "key", key, "error", err)
				continue
			}
			if !found {
				continue
			}
			if err := live.Update(func(c *config.LiveNetConfig) error { return c.ApplyKey(key, value) }); err != nil {
				slog.Error("live config rejected", "key", key, "error", err)
				continue
			}
			slog.Info("live config updated", "key", key, "value", value)
		}
	}
}

func (s *Store) PublishUnitsChanged(ctx context.Context) error {
	return s.redis.Publish(ctx, unitsUpdatesChannel, "reload").Err()
}

func (s *Store) WatchUnits(ctx context.Context, onReload func()) {
	sub := s.redis.Subscribe(ctx, unitsUpdatesChannel)
	defer sub.Close()
	ch := sub.Channel()

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			onReload()
		}
	}
}

func postgresDSNFromEnv() string {
	host := getEnv("POSTGRES_HOST", "localhost")
	port := getEnv("POSTGRES_PORT", "5432")
	user := getEnv("POSTGRES_USER", "game")
	password := getEnv("POSTGRES_PASSWORD", "game")
	db := getEnv("POSTGRES_DB", "game")

	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + db,
	}
	q := url.Values{}
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

func redisAddrFromEnv() string {
	host := getEnv("REDIS_HOST", "localhost")
	port := getEnv("REDIS_PORT", "6379")
	return host + ":" + port
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
