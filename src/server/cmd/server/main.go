package main

import (
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/server"
)

func main() {
	// Init structured JSON logger
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	// Optimize Go runtime for 10K connections
	optimizeRuntime()

	// Load configuration
	cfg := config.Load()

	slog.Info("server starting",
		"port", cfg.Server.Port,
		"tick_rate_hz", cfg.Game.TickRate,
		"workers", cfg.Server.Workers,
		"broadcast_workers", cfg.Net.BroadcastWorkers,
		"max_connections", cfg.Net.MaxConnections,
	)

	// Create and start game server
	gameServer := server.New(cfg)
	if err := gameServer.Start(); err != nil {
		slog.Error("failed to start server", "error", err)
		os.Exit(1)
	}
}

func optimizeRuntime() {
	// Set GOMAXPROCS to CPU count if not set
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	// Optimize GC for high throughput
	if os.Getenv("GOGC") == "" {
		os.Setenv("GOGC", "400")
	}

	// GOMEMLIMIT is read automatically by the Go runtime from the env var.
	// Log the current value so it's visible in structured logs.
	memLimit := debug.SetMemoryLimit(-1) // -1 = read current without changing
	if memLimit != 9223372036854775807 { // math.MaxInt64 = no limit
		slog.Info("memory limit active", "limit_mb", memLimit/1024/1024)
	}

	slog.Info("runtime optimized",
		"gomaxprocs", runtime.GOMAXPROCS(0),
		"gogc", os.Getenv("GOGC"),
	)
}
