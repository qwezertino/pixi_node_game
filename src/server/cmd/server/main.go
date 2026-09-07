package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/server"
)

func main() {

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	optimizeRuntime()

	cfg := config.Load()

	slog.Info("server starting",
		"port", cfg.Server.Port,
		"tick_rate_hz", cfg.Game.TickRate,
		"workers", cfg.Server.Workers,
		"max_connections", cfg.Net.MaxConnections,
	)

	gameServer := server.New(cfg)

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
