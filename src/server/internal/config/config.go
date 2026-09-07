package config

import (
	"encoding/json"
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
	TickRate     int
	SyncInterval time.Duration

	UnitsPerMeter float64
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
	MaxConnections                 int
	MessageRateLimit               int
	BurstLimit                     int
	IPConnRate                     float64
	IPConnBurst                    int
	FanoutMaxBroadcastBytesPerTick int
	VelocityReplication            bool
	KeyframeDivisor                int
	FanoutQueueShedDepth           int
	FanoutDropStreak               int
	WriteBatchSize                 int
	FanoutFairDebtMax              int
	FanoutFairDebtInc              int
	FanoutFairDebtDec              int
	FanoutFairDebtWeightNs         int64
	FanoutRoundRobinWeightNs       int64
	FanoutCriticalWindow           time.Duration
	FanoutCriticalBoostNs          int64
	FanoutMinRecipientsPerTick     int
	FanoutMaxRecipientsPerTick     int
	FanoutTargetMs                 int
	WorldStateActiveStaleness      time.Duration
	WorldStateIdleStaleness        time.Duration
	WorldStateActiveWindow         time.Duration
}

// GameSettings mirrors the `game_settings` row in Postgres (see
// docker/postgres/init/001_init.sql) — the single source for game/world
// rules that used to live in gameConfig.json. No live-reload: these are
// baked into precomputed tables and an already-bound listener at startup.
type GameSettings struct {
	TickRate        int
	SyncIntervalSec int
	UnitsPerMeter   float64

	WorldWidth  int
	WorldHeight int

	SpawnMinX int
	SpawnMaxX int
	SpawnMinY int
	SpawnMaxY int

	PlayerBaseScale      float64
	DebugMode            bool
	WorldBackgroundColor string
}

// clientConfigView is the exact JSON shape the TS client expects at
// GET /api/config (see src/shared/gameConfig.ts's GameConfig interface) —
// what used to be the bundled gameConfig.json.
type clientConfigView struct {
	Network struct {
		TickRate     int `json:"tickRate"`
		SyncInterval int `json:"syncInterval"`
	} `json:"network"`
	Movement struct {
		UnitsPerMeter float64 `json:"unitsPerMeter"`
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
		BaseScale float64 `json:"baseScale"`
	} `json:"player"`
	Game struct {
		DebugMode bool `json:"debugMode"`
	} `json:"game"`
	Colors struct {
		WorldBackground string `json:"worldBackground"`
	} `json:"colors"`
}

// Build applies .env overrides on top of settings loaded from Postgres and
// returns both the internal Config the Go server runs on and the exact JSON
// served to the client at GET /api/config — built from the same effective
// values, so client and server can never see different tick rate, world
// size, etc. (the whole reason those were pulled out of two separate bundled
// files in the first place).
func Build(gs *GameSettings) (*Config, []byte, error) {
	tickRate := getEnvInt("TICK_RATE", gs.TickRate)
	syncIntervalSec := getEnvInt("SYNC_INTERVAL_SEC", gs.SyncIntervalSec)
	unitsPerMeter := getEnvFloat("UNITS_PER_METER", gs.UnitsPerMeter)
	worldWidth := getEnvInt("WORLD_WIDTH", gs.WorldWidth)
	worldHeight := getEnvInt("WORLD_HEIGHT", gs.WorldHeight)
	spawnMinX := getEnvInt("SPAWN_MIN_X", gs.SpawnMinX)
	spawnMaxX := getEnvInt("SPAWN_MAX_X", gs.SpawnMaxX)
	spawnMinY := getEnvInt("SPAWN_MIN_Y", gs.SpawnMinY)
	spawnMaxY := getEnvInt("SPAWN_MAX_Y", gs.SpawnMaxY)

	cfg := &Config{
		Server: ServerConfig{
			Port:      getEnvInt("PORT", 8108),
			Host:      getEnvString("HOST", "0.0.0.0"),
			Workers:   getEnvInt("WORKERS", 0),
			StaticDir: getEnvString("STATIC_DIR", "../dist"),
		},

		Game: GameConfig{
			TickRate:      tickRate,
			SyncInterval:  time.Duration(syncIntervalSec) * time.Second,
			UnitsPerMeter: unitsPerMeter,
		},
		World: WorldConfig{
			Width:     uint16(worldWidth),
			Height:    uint16(worldHeight),
			SpawnMinX: uint16(spawnMinX),
			SpawnMaxX: uint16(spawnMaxX),
			SpawnMinY: uint16(spawnMinY),
			SpawnMaxY: uint16(spawnMaxY),
			MinX:      0,
			MaxX:      uint16(worldWidth),
			MinY:      0,
			MaxY:      uint16(worldHeight),
		},

		Net: NetworkConfig{
			MaxConnections:                 getEnvInt("MAX_CONNECTIONS", 12000),
			MessageRateLimit:               getEnvInt("RATE_LIMIT_MSG_SEC", 120),
			BurstLimit:                     getEnvInt("RATE_LIMIT_BURST", 20),
			IPConnRate:                     getEnvFloat("IP_CONN_RATE", 10.0),
			IPConnBurst:                    getEnvInt("IP_CONN_BURST", 20),
			FanoutMaxBroadcastBytesPerTick: getEnvInt("FANOUT_MAX_BROADCAST_BYTES_PER_TICK", 0),
			VelocityReplication:            getEnvBool("VELOCITY_REPLICATION", true),
			KeyframeDivisor:                getEnvInt("KEYFRAME_DIVISOR", 100),
			FanoutQueueShedDepth:           getEnvInt("FANOUT_QUEUE_SHED_DEPTH", 6),
			FanoutDropStreak:               getEnvInt("FANOUT_DROP_STREAK", 120),
			WriteBatchSize:                 getEnvInt("WRITE_BATCH_SIZE", 8),
			FanoutFairDebtMax:              getEnvInt("FANOUT_FAIR_DEBT_MAX", 12),
			FanoutFairDebtInc:              getEnvInt("FANOUT_FAIR_DEBT_INC", 1),
			FanoutFairDebtDec:              getEnvInt("FANOUT_FAIR_DEBT_DEC", 2),
			FanoutFairDebtWeightNs:         int64(getEnvInt("FANOUT_FAIR_DEBT_WEIGHT_NS", 250000)),
			FanoutRoundRobinWeightNs:       int64(getEnvInt("FANOUT_ROUND_ROBIN_WEIGHT_NS", 150000)),
			FanoutCriticalWindow:           time.Duration(getEnvInt("FANOUT_CRITICAL_WINDOW_MS", 400)) * time.Millisecond,
			FanoutCriticalBoostNs:          int64(getEnvInt("FANOUT_CRITICAL_BOOST_NS", 3000000)),
			FanoutMinRecipientsPerTick:     getEnvInt("FANOUT_MIN_RECIPIENTS_PER_TICK", 256),
			FanoutMaxRecipientsPerTick:     getEnvInt("FANOUT_MAX_RECIPIENTS_PER_TICK", 0),
			FanoutTargetMs:                 getEnvInt("FANOUT_TARGET_MS", 12),
			WorldStateActiveStaleness:      time.Duration(getEnvInt("WORLD_STATE_ACTIVE_STALENESS_MS", 150)) * time.Millisecond,
			WorldStateIdleStaleness:        time.Duration(getEnvInt("WORLD_STATE_IDLE_STALENESS_MS", 350)) * time.Millisecond,
			WorldStateActiveWindow:         time.Duration(getEnvInt("WORLD_STATE_ACTIVE_WINDOW_MS", 1000)) * time.Millisecond,
		},
	}

	var client clientConfigView
	client.Network.TickRate = tickRate
	client.Network.SyncInterval = syncIntervalSec * 1000
	client.Movement.UnitsPerMeter = unitsPerMeter
	client.World.VirtualSize.Width = worldWidth
	client.World.VirtualSize.Height = worldHeight
	client.World.SpawnArea.MinX = spawnMinX
	client.World.SpawnArea.MaxX = spawnMaxX
	client.World.SpawnArea.MinY = spawnMinY
	client.World.SpawnArea.MaxY = spawnMaxY
	client.World.Boundaries.MinX = 0
	client.World.Boundaries.MaxX = worldWidth
	client.World.Boundaries.MinY = 0
	client.World.Boundaries.MaxY = worldHeight
	client.Player.BaseScale = gs.PlayerBaseScale
	client.Game.DebugMode = gs.DebugMode
	client.Colors.WorldBackground = gs.WorldBackgroundColor

	clientJSON, err := json.Marshal(client)
	if err != nil {
		return nil, nil, err
	}
	return cfg, clientJSON, nil
}

func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
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
