package liveconfig

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"pixi_game_server/internal/config"
)

const (
	updatesChannel = "game_config_updates"
	tableName      = "game_config"
	revisionTable  = "game_config_revision"

	unitsUpdatesChannel = "units_updates"
)

// dbPool is the subset of *pgxpool.Pool used by Store. It exists so tests can
// substitute a mock pool.
type dbPool interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Ping(ctx context.Context) error
	Close()
}

type dbQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Store struct {
	pool  dbPool
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
	if _, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+tableName+` (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+revisionTable+` (
			id       INTEGER PRIMARY KEY,
			revision BIGINT NOT NULL DEFAULT 0
		)
	`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO `+revisionTable+` (id, revision) VALUES (1, 0)
		ON CONFLICT (id) DO NOTHING
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

func loadAll(ctx context.Context, db dbQuerier) (map[string]string, error) {
	rows, err := db.Query(ctx, `SELECT key, value FROM `+tableName)
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

func (s *Store) LoadAll(ctx context.Context) (map[string]string, error) {
	return loadAll(ctx, s.pool)
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
// concurrency guarantees.
func (s *Store) SetKey(ctx context.Context, key, value string) error {
	return s.SetKeys(ctx, map[string]string{key: value})
}

// SetKeys applies patch as one atomic, serialized change to the live config:
// the current config revision row is locked first, the stored keys are read
// and merged with patch under that lock, the merged result is validated as a
// whole, and only then is patch written and the revision advanced — all in
// one transaction. On success it publishes a single notification carrying
// the new revision number.
func (s *Store) SetKeys(ctx context.Context, patch map[string]string) error {
	if len(patch) == 0 {
		return nil
	}

	revision, err := s.setKeysTx(ctx, patch)
	if err != nil {
		return err
	}
	return s.redis.Publish(ctx, updatesChannel, strconv.FormatInt(revision, 10)).Err()
}

func (s *Store) setKeysTx(ctx context.Context, patch map[string]string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	if _, err := lockConfigRevision(ctx, tx); err != nil {
		return 0, err
	}

	settings, err := loadGameSettings(ctx, tx)
	if err != nil {
		return 0, err
	}
	cfg, _, err := config.Build(settings)
	if err != nil {
		return 0, err
	}
	raw, err := loadAll(ctx, tx)
	if err != nil {
		return 0, err
	}
	if _, err := resolveLiveNetPatch(config.BuildLiveNetConfig(cfg), raw, patch); err != nil {
		return 0, err
	}

	for key, value := range patch {
		if _, err := tx.Exec(ctx, `
			INSERT INTO `+tableName+` (key, value, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
		`, key, value); err != nil {
			return 0, err
		}
	}

	revision, err := bumpConfigRevision(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return revision, nil
}

func lockConfigRevision(ctx context.Context, tx pgx.Tx) (int64, error) {
	var revision int64
	err := tx.QueryRow(ctx, `SELECT revision FROM `+revisionTable+` WHERE id = 1 FOR UPDATE`).Scan(&revision)
	return revision, err
}

func bumpConfigRevision(ctx context.Context, tx pgx.Tx) (int64, error) {
	var revision int64
	err := tx.QueryRow(ctx, `UPDATE `+revisionTable+` SET revision = revision + 1 WHERE id = 1 RETURNING revision`).Scan(&revision)
	return revision, err
}

func resolveLiveNetPatch(baseline *config.LiveNetConfig, raw map[string]string, patch map[string]string) (*config.LiveNetConfig, error) {
	merged := make(map[string]string, len(raw)+len(patch))
	for key, value := range raw {
		merged[key] = value
	}
	for key, value := range patch {
		merged[key] = value
	}

	live := config.NewLiveNet(baseline)
	if err := live.Update(func(c *config.LiveNetConfig) error {
		for key, value := range merged {
			if err := c.ApplyKey(key, value); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return live.Load(), nil
}

// LoadInto reloads every stored key from scratch and applies it to live as
// one atomically validated snapshot.
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

// Watch listens for config-revision notifications and, for each one,
// reloads and applies the entire stored config to live as a single atomic
// snapshot rather than key by key.
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
			if err := s.LoadInto(ctx, live); err != nil {
				slog.Error("live config: failed to apply snapshot after notification", "revision", msg.Payload, "error", err)
				continue
			}
			slog.Info("live config updated", "revision", msg.Payload)
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
