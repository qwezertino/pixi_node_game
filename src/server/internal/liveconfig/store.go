// Package liveconfig wires config.LiveNet to Postgres (source of truth,
// persisted) and Redis (pub/sub fan-out of "something changed" events), and
// also reads the static game_settings/units tables (see settings.go and
// docker/postgres/init/001_init.sql) — everything the server needs now lives
// in Postgres, seeded once by that migration.
//
// Flow for the live-tunable `game_config` table (rate limits, fanout tuning):
//  1. Server.New builds an initial LiveNetConfig from .env defaults — this is
//     only ever a seed value, applied via Seed's ON CONFLICT DO NOTHING.
//  2. On startup, EnsureSchema+Seed create the table if missing and insert
//     the seed values for any key not already present, so the DB becomes
//     authoritative from then on without clobbering values an operator
//     already changed.
//  3. LoadInto pulls every row from Postgres and applies it onto the live
//     config in a single atomic swap.
//  4. Watch subscribes to a Redis channel; any publish there (from configctl,
//     an admin panel, or another server instance) triggers a re-read of that
//     one key from Postgres and a live, in-memory update.
//
// game_settings and units (settings.go) are read once at startup with no
// live-reload and no fallback — see internal/config.GameSettings and
// internal/units.LoadDefinitions.
package liveconfig

import (
	"context"
	"log/slog"
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

	// unitsUpdatesChannel carries no payload worth reading — any publish on
	// it just means "reload the whole `units` table", since recomputing all
	// 11 rows is cheap and a per-unit diff isn't worth the complexity.
	unitsUpdatesChannel = "units_updates"
)

type Store struct {
	pool  *pgxpool.Pool
	redis *redis.Client
}

// Connect dials Postgres and Redis using POSTGRES_*/REDIS_* env vars (see
// .env). It returns (nil, err) if either is unreachable so the caller can
// fall back to the static config instead of failing to boot.
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

// Seed inserts any key from seed that isn't already present in the table.
// Existing rows are left untouched — the DB, once populated, is the source
// of truth going forward.
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

// SetKey upserts a value in Postgres and publishes a change notification on
// Redis so every connected server instance re-reads it immediately.
func (s *Store) SetKey(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO `+tableName+` (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, key, value)
	if err != nil {
		return err
	}
	return s.redis.Publish(ctx, updatesChannel, key).Err()
}

// LoadInto applies every row currently in Postgres onto live in one atomic
// update, so a partially-read DB never produces a torn snapshot.
func (s *Store) LoadInto(ctx context.Context, live *config.LiveNet) error {
	rows, err := s.LoadAll(ctx)
	if err != nil {
		return err
	}
	live.Update(func(c *config.LiveNetConfig) {
		for key, value := range rows {
			if err := c.ApplyKey(key, value); err != nil {
				slog.Warn("live config: skipping bad row", "key", key, "value", value, "error", err)
			}
		}
	})
	return nil
}

// Watch blocks, applying every published key change onto live, until ctx is
// cancelled. Run it in its own goroutine.
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
			live.Update(func(c *config.LiveNetConfig) {
				if err := c.ApplyKey(key, value); err != nil {
					slog.Warn("live config: ignoring unknown/invalid key", "key", key, "value", value, "error", err)
				}
			})
			slog.Info("live config updated", "key", key, "value", value)
		}
	}
}

// PublishUnitsChanged tells every connected server instance to reload the
// `units` table (see WatchUnits). Call it after any write to that table.
func (s *Store) PublishUnitsChanged(ctx context.Context) error {
	return s.redis.Publish(ctx, unitsUpdatesChannel, "reload").Err()
}

// WatchUnits blocks, calling onReload for every publish on the units
// channel, until ctx is cancelled. Run it in its own goroutine — like Watch,
// but for the whole `units` table rather than one config key at a time.
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
	return "postgres://" + user + ":" + password + "@" + host + ":" + port + "/" + db + "?sslmode=disable"
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
