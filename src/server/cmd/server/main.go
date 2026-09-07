package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/liveconfig"
	"pixi_game_server/internal/server"
	"pixi_game_server/internal/units"
)

func main() {

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	optimizeRuntime()

	liveConfigCtx, liveConfigCancel := context.WithCancel(context.Background())
	defer liveConfigCancel()

	// Postgres is mandatory: game/world rules (game_settings) and unit stats
	// (units) live only in the database now, seeded once by
	// docker/postgres/init/001_init.sql — there is no bundled-file fallback
	// left to fall back to.
	dbCtx, dbCancel := context.WithTimeout(liveConfigCtx, 5*time.Second)
	liveStore, dbErr := liveconfig.Connect(dbCtx)
	dbCancel()
	if dbErr != nil {
		slog.Error("cannot connect to Postgres/Redis — game_settings and units live only in the database now, run `make docker-up-core` or check POSTGRES_*/REDIS_* env vars", "error", dbErr)
		os.Exit(1)
	}
	defer liveStore.Close()

	gameSettings, err := liveStore.LoadGameSettings(liveConfigCtx)
	if err != nil {
		slog.Error("failed to load game_settings from database — did the migration in docker/postgres/init run?", "error", err)
		os.Exit(1)
	}

	unitDefs, err := liveStore.LoadUnitDefinitions(liveConfigCtx)
	if err != nil {
		slog.Error("failed to load units from database — did the migration in docker/postgres/init run?", "error", err)
		os.Exit(1)
	}
	if err := units.LoadDefinitions(unitDefs); err != nil {
		slog.Error("units table has invalid data", "error", err)
		os.Exit(1)
	}

	cfg, gameConfigJSON, err := config.Build(gameSettings)
	if err != nil {
		slog.Error("failed to build client-facing config JSON", "error", err)
		os.Exit(1)
	}
	unitsJSON, err := json.Marshal(units.All())
	if err != nil {
		slog.Error("failed to build client-facing units JSON", "error", err)
		os.Exit(1)
	}

	slog.Info("server starting",
		"port", cfg.Server.Port,
		"tick_rate_hz", cfg.Game.TickRate,
		"workers", cfg.Server.Workers,
		"max_connections", cfg.Net.MaxConnections,
		"unit_count", len(unitDefs),
	)

	gameServer := server.New(cfg)
	gameServer.SetStaticBlobs(gameConfigJSON, unitsJSON)

	if os.Getenv("ENABLE_UNIT_ADMIN_API") == "true" {
		gameServer.EnableUnitAdminAPI(liveStore)
	}

	if err := liveStore.EnsureSchema(liveConfigCtx); err != nil {
		slog.Error("live config: failed to ensure schema", "error", err)
	} else if err := liveStore.Seed(liveConfigCtx, gameServer.Live().Load().KeyValues()); err != nil {
		slog.Error("live config: failed to seed net-config defaults", "error", err)
	} else if err := liveStore.LoadInto(liveConfigCtx, gameServer.Live()); err != nil {
		slog.Error("live config: failed to load net-config from database", "error", err)
	} else {
		slog.Info("live net-config loaded from database, watching for changes")
		go liveStore.Watch(liveConfigCtx, gameServer.Live())
	}

	// Live-reload unit stats: any PATCH /api/admin/units/{typeId} (from this
	// instance or another one behind the same DB) publishes here, and every
	// connected server re-reads the whole `units` table and recomputes the
	// move/stamina/attack tables — see internal/game.RecomputeUnitTables.
	go liveStore.WatchUnits(liveConfigCtx, func() {
		defs, err := liveStore.LoadUnitDefinitions(liveConfigCtx)
		if err != nil {
			slog.Error("units: failed to reload from database", "error", err)
			return
		}
		if err := units.LoadDefinitions(defs); err != nil {
			slog.Error("units: reloaded data is invalid, keeping previous definitions", "error", err)
			return
		}
		gameServer.RecomputeUnitTables()
		if data, err := json.Marshal(units.All()); err != nil {
			slog.Error("units: failed to re-marshal client-facing JSON", "error", err)
		} else {
			gameServer.UpdateUnitsJSON(data)
		}
		slog.Info("units reloaded live", "unit_count", len(defs))
	})

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- gameServer.Start()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		if err != nil {
			slog.Error("failed to start server", "error", err)
			os.Exit(1)
		}
	case sig := <-signals:
		slog.Info("shutdown signal received", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := gameServer.Shutdown(ctx); err != nil {
			slog.Error("graceful shutdown failed", "error", err)
			os.Exit(1)
		}
	}
}

func optimizeRuntime() {

	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	gcPct := 400
	if v := os.Getenv("GOGC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			gcPct = n
		}
	}
	debug.SetGCPercent(gcPct)

	memLimit := debug.SetMemoryLimit(-1)
	if memLimit != 9223372036854775807 {
		slog.Info("memory limit active", "limit_mb", memLimit/1024/1024)
	}

	slog.Info("runtime optimized",
		"gomaxprocs", runtime.GOMAXPROCS(0),
		"gogc", gcPct,
	)
}
