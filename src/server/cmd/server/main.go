package main

import (
	"log"
	"os"
	"runtime"
	"strconv"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/server"
)

func main() {
	// Optimize Go runtime for 10K connections
	optimizeRuntime()

	// Load configuration
	cfg := config.Load()

	log.Printf("🚀 Starting HIGH-PERFORMANCE Go game server")
	log.Printf("📊 Config: Port=%d, TickRate=%dHz, Workers=%d",
		cfg.Server.Port, cfg.Game.TickRate, cfg.Server.Workers)

	// Create and start game server
	gameServer := server.New(cfg)
	if err := gameServer.Start(); err != nil {
		log.Fatalf("❌ Failed to start server: %v", err)
	}
}

func optimizeRuntime() {
	// Set GOMAXPROCS to CPU count if not set
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	// Optimize GC for high throughput
	if os.Getenv("GOGC") == "" {
		os.Setenv("GOGC", "800") // Reduce GC frequency
	}

	// Set memory limit if available
	if memLimit := os.Getenv("GOMEMLIMIT"); memLimit != "" {
		if limit, err := strconv.Atoi(memLimit); err == nil {
			log.Printf("📈 Memory limit set to %d MB", limit/1024/1024)
		}
	}

	log.Printf("⚙️  Runtime optimized: GOMAXPROCS=%d, GOGC=%s",
		runtime.GOMAXPROCS(0), os.Getenv("GOGC"))
}
