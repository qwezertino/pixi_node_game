package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Server ServerConfig
	Game   GameConfig
	World  WorldConfig
	Net    NetworkConfig
}

type ServerConfig struct {
	Port            int
	Host            string
	Workers         int
	ReadBufferSize  int
	WriteBufferSize int
	StaticDir       string
}

type GameConfig struct {
	TickRate           int
	SyncInterval       time.Duration
	BatchInterval      time.Duration
	PlayerSpeedPerTick int
}

type WorldConfig struct {
	Width     uint16
	Height    uint16
	SpawnMinX uint16
	SpawnMaxX uint16
	SpawnMinY uint16
	SpawnMaxY uint16
	MinX      uint16
	MaxX      uint16
	MinY      uint16
	MaxY      uint16
}

type NetworkConfig struct {
	MaxConnections   int
	EventChannelSize int
	BroadcastWorkers int
	MessageRateLimit int
	BurstLimit       int
	RateLimitWindow  time.Duration
}

func Load() *Config {
	return &Config{
		Server: ServerConfig{
			Port:            getEnvInt("PORT", 8108),
			Host:            getEnvString("HOST", "0.0.0.0"),
			Workers:         getEnvInt("WORKERS", 0), // 0 = auto-detect CPU count
			ReadBufferSize:  getEnvInt("READ_BUFFER_SIZE", 4096),
			WriteBufferSize: getEnvInt("WRITE_BUFFER_SIZE", 4096),
			StaticDir:       getEnvString("STATIC_DIR", "../dist"),
		},
		Game: GameConfig{
			TickRate:           getEnvInt("TICK_RATE", 24), // Match client and legacy server (24 Hz)
			SyncInterval:       time.Duration(getEnvInt("SYNC_INTERVAL_SEC", 30)) * time.Second,
			BatchInterval:      time.Duration(getEnvInt("BATCH_INTERVAL_MS", 50)) * time.Millisecond, // ~20 FPS for batching
			PlayerSpeedPerTick: getEnvInt("PLAYER_SPEED", 4),
		},
		World: WorldConfig{
			Width:     uint16(getEnvInt("WORLD_WIDTH", 2000)),
			Height:    uint16(getEnvInt("WORLD_HEIGHT", 2000)),
			SpawnMinX: uint16(getEnvInt("SPAWN_MIN_X", 100)),
			SpawnMaxX: uint16(getEnvInt("SPAWN_MAX_X", 900)),
			SpawnMinY: uint16(getEnvInt("SPAWN_MIN_Y", 100)),
			SpawnMaxY: uint16(getEnvInt("SPAWN_MAX_Y", 900)),
			MinX:      0,
			MaxX:      uint16(getEnvInt("WORLD_WIDTH", 2000)),
			MinY:      0,
			MaxY:      uint16(getEnvInt("WORLD_HEIGHT", 2000)),
		},
		Net: NetworkConfig{
			MaxConnections:   getEnvInt("MAX_CONNECTIONS", 12000),
			EventChannelSize: getEnvInt("EVENT_CHANNEL_SIZE", 100000), // Большой буфер для events
			BroadcastWorkers: getEnvInt("BROADCAST_WORKERS", 0),       // 0 = CPU_COUNT * 2
			MessageRateLimit: getEnvInt("RATE_LIMIT_MSG_SEC", 60),
			BurstLimit:       getEnvInt("RATE_LIMIT_BURST", 10),
			RateLimitWindow:  time.Duration(getEnvInt("RATE_LIMIT_WINDOW_MS", 1000)) * time.Millisecond,
		},
	}
}

func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
