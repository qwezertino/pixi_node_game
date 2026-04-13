package config

import (
	"fmt"
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
	Port      int
	Host      string
	Workers   int
	StaticDir string
}

type GameConfig struct {
	TickRate           int
	SyncInterval       time.Duration
	BatchInterval      time.Duration
	PlayerSpeedPerTick int
	AttackDuration     time.Duration
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
	MaxConnections             int
	MessageRateLimit           int
	BurstLimit                 int
	IPConnRate                 float64 // connections/sec per IP; 0 = disabled
	IPConnBurst                int
	FanoutWorkers              int
	FanoutDropStreak           int
	WriteBatchSize             int
	FanoutMinRecipientsPerTick int
	FanoutMaxRecipientsPerTick int // 0 = unlimited (all connections)
	FanoutTargetMs             int
	WorldStateActiveStaleness  time.Duration
	WorldStateIdleStaleness    time.Duration
	WorldStateActiveWindow     time.Duration
}

// JSONConfig mirrors the structure of gameConfig.json (shared with the TypeScript client).
// Only game-rule values live here; server infrastructure is configured via .env.
type JSONConfig struct {
	Network struct {
		TickRate        int `json:"tickRate"`
		SyncInterval    int `json:"syncInterval"`
		BatchIntervalMs int `json:"batchIntervalMs"`
	} `json:"network"`
	Movement struct {
		PlayerSpeedPerTick int `json:"playerSpeedPerTick"`
	} `json:"movement"`
	World struct {
		VirtualSize struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"virtualSize"`
		SpawnArea struct {
			MinX int `json:"minX"`
			MaxX int `json:"maxX"`
			MinY int `json:"minY"`
			MaxY int `json:"maxY"`
		} `json:"spawnArea"`
		Boundaries struct {
			MinX int `json:"minX"`
			MaxX int `json:"maxX"`
			MinY int `json:"minY"`
			MaxY int `json:"maxY"`
		} `json:"boundaries"`
	} `json:"world"`
	Player struct {
		BaseScale        float64 `json:"baseScale"`
		AnimationSpeed   float64 `json:"animationSpeed"`
		AttackDurationMs int     `json:"attackDurationMs"`
	} `json:"player"`
	Game struct {
		DebugMode bool `json:"debugMode"`
	} `json:"game"`
}

// Load builds the server Config.
//
// Priority order (highest to lowest):
//  1. Environment variables (from .env or system)
//  2. Embedded gameConfig.json (game-rule defaults, shared with client)
//  3. Hardcoded fallbacks for server-only infrastructure values
func Load() *Config {
	jsonConfig, err := loadEmbeddedConfig()
	if err != nil {
		fmt.Printf("Error: Could not load embedded config: %v\n", err)
		os.Exit(1)
	}

	syncIntervalSec := jsonConfig.Network.SyncInterval / 1000

	return &Config{
		// ── Server infrastructure ─────────────────────────────────────────────
		// Defaults are hardcoded here; override via .env for deployment tuning.
		Server: ServerConfig{
			Port:      getEnvInt("PORT", 8108),
			Host:      getEnvString("HOST", "0.0.0.0"),
			Workers:   getEnvInt("WORKERS", 0),
			StaticDir: getEnvString("STATIC_DIR", "../dist"),
		},
		// ── Game rules ────────────────────────────────────────────────────────
		// Defaults come from embedded gameConfig.json so they always match the client.
		Game: GameConfig{
			TickRate:           getEnvInt("TICK_RATE", jsonConfig.Network.TickRate),
			SyncInterval:       time.Duration(getEnvInt("SYNC_INTERVAL_SEC", syncIntervalSec)) * time.Second,
			BatchInterval:      time.Duration(getEnvInt("BATCH_INTERVAL_MS", jsonConfig.Network.BatchIntervalMs)) * time.Millisecond,
			PlayerSpeedPerTick: getEnvInt("PLAYER_SPEED", jsonConfig.Movement.PlayerSpeedPerTick),
			AttackDuration:     time.Duration(getEnvInt("ATTACK_DURATION_MS", jsonConfig.Player.AttackDurationMs)) * time.Millisecond,
		},
		World: WorldConfig{
			Width:     uint16(getEnvInt("WORLD_WIDTH", jsonConfig.World.VirtualSize.Width)),
			Height:    uint16(getEnvInt("WORLD_HEIGHT", jsonConfig.World.VirtualSize.Height)),
			SpawnMinX: uint16(getEnvInt("SPAWN_MIN_X", jsonConfig.World.SpawnArea.MinX)),
			SpawnMaxX: uint16(getEnvInt("SPAWN_MAX_X", jsonConfig.World.SpawnArea.MaxX)),
			SpawnMinY: uint16(getEnvInt("SPAWN_MIN_Y", jsonConfig.World.SpawnArea.MinY)),
			SpawnMaxY: uint16(getEnvInt("SPAWN_MAX_Y", jsonConfig.World.SpawnArea.MaxY)),
			MinX:      0,
			MaxX:      uint16(getEnvInt("WORLD_WIDTH", jsonConfig.World.VirtualSize.Width)),
			MinY:      0,
			MaxY:      uint16(getEnvInt("WORLD_HEIGHT", jsonConfig.World.VirtualSize.Height)),
		},
		// ── Network infrastructure ────────────────────────────────────────────
		// All configurable via .env; hardcoded values are production-tested defaults.
		Net: NetworkConfig{
			MaxConnections:             getEnvInt("MAX_CONNECTIONS", 12000),
			MessageRateLimit:           getEnvInt("RATE_LIMIT_MSG_SEC", 120),
			BurstLimit:                 getEnvInt("RATE_LIMIT_BURST", 20),
			IPConnRate:                 getEnvFloat("IP_CONN_RATE", 10.0),
			IPConnBurst:                getEnvInt("IP_CONN_BURST", 20),
			FanoutWorkers:              getEnvInt("FANOUT_WORKERS", 0),
			FanoutDropStreak:           getEnvInt("FANOUT_DROP_STREAK", 120),
			WriteBatchSize:             getEnvInt("WRITE_BATCH_SIZE", 8),
			FanoutMinRecipientsPerTick: getEnvInt("FANOUT_MIN_RECIPIENTS_PER_TICK", 256),
			FanoutMaxRecipientsPerTick: getEnvInt("FANOUT_MAX_RECIPIENTS_PER_TICK", 0),
			FanoutTargetMs:             getEnvInt("FANOUT_TARGET_MS", 12),
			WorldStateActiveStaleness:  time.Duration(getEnvInt("WORLD_STATE_ACTIVE_STALENESS_MS", 150)) * time.Millisecond,
			WorldStateIdleStaleness:    time.Duration(getEnvInt("WORLD_STATE_IDLE_STALENESS_MS", 350)) * time.Millisecond,
			WorldStateActiveWindow:     time.Duration(getEnvInt("WORLD_STATE_ACTIVE_WINDOW_MS", 1000)) * time.Millisecond,
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

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}
