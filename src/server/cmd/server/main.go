package main

import (
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"

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

	// Optimize GC for high throughput.
	// GOGC=400: allow heap to grow to 4× live heap before triggering GC.
	// NOTE: os.Setenv("GOGC", ...) has no effect — the Go runtime reads GOGC before
	// main() runs. debug.SetGCPercent is the only way to change it at runtime.
	gcPct := 400
	if v := os.Getenv("GOGC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			gcPct = n
		}
	}
	debug.SetGCPercent(gcPct)

	// GOMEMLIMIT is read automatically by the Go runtime from the env var.
	// Log the current value so it's visible in structured logs.
	memLimit := debug.SetMemoryLimit(-1) // -1 = read current without changing
	if memLimit != 9223372036854775807 { // math.MaxInt64 = no limit
		slog.Info("memory limit active", "limit_mb", memLimit/1024/1024)
	}

	slog.Info("runtime optimized",
		"gomaxprocs", runtime.GOMAXPROCS(0),
		"gogc", gcPct,
	)
}
