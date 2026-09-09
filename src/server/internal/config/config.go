package config

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
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
	ManagementAddr string
	EnablePprof    bool
	AdminToken     string
	Port           int
	Host           string
	StaticDir      string
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

func Build(gs *GameSettings) (*Config, []byte, error) {
	if gs == nil {
		return nil, nil, fmt.Errorf("game settings are required")
	}
	if err := validateEnv(); err != nil {
		return nil, nil, err
	}
	tickRate := getEnvInt("TICK_RATE", gs.TickRate)
	syncIntervalSec := getEnvInt("SYNC_INTERVAL_SEC", gs.SyncIntervalSec)
	unitsPerMeter := getEnvFloat("UNITS_PER_METER", gs.UnitsPerMeter)
	worldWidth := getEnvInt("WORLD_WIDTH", gs.WorldWidth)
	worldHeight := getEnvInt("WORLD_HEIGHT", gs.WorldHeight)
	spawnMinX := getEnvInt("SPAWN_MIN_X", gs.SpawnMinX)
	spawnMaxX := getEnvInt("SPAWN_MAX_X", gs.SpawnMaxX)
	spawnMinY := getEnvInt("SPAWN_MIN_Y", gs.SpawnMinY)
	spawnMaxY := getEnvInt("SPAWN_MAX_Y", gs.SpawnMaxY)

	if tickRate < 1 || tickRate > 240 || syncIntervalSec < 1 || syncIntervalSec > 3600 {
		return nil, nil, fmt.Errorf("tick rate must be 1..240 and sync interval 1..3600 seconds")
	}
	if worldWidth < 1 || worldWidth > 65535 || worldHeight < 1 || worldHeight > 65535 {
		return nil, nil, fmt.Errorf("world dimensions must be 1..65535")
	}
	if spawnMinX < 0 || spawnMinY < 0 || spawnMaxX <= spawnMinX || spawnMaxY <= spawnMinY || spawnMaxX > worldWidth || spawnMaxY > worldHeight {
		return nil, nil, fmt.Errorf("spawn area must be nonempty and inside the world")
	}
	if !finiteRange(gs.PlayerBaseScale, 0.01, 100) {
		return nil, nil, fmt.Errorf("invalid player base scale")
	}
	cfg := &Config{
		Server: ServerConfig{
			ManagementAddr: getEnvString("MANAGEMENT_ADDR", "127.0.0.1:8110"),
			EnablePprof:    getEnvBool("ENABLE_PPROF", false),
			AdminToken:     os.Getenv("ADMIN_API_TOKEN"),
			Port:           getEnvInt("PORT", 8108),
			Host:           getEnvString("HOST", "0.0.0.0"),
			StaticDir:      getEnvString("STATIC_DIR", "../dist"),
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

	if err := cfg.Validate(); err != nil {
		return nil, nil, err
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

func finiteRange(v, lo, hi float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi
}

func (c *Config) Validate() error {
	if c.Game.TickRate < 1 || c.Game.TickRate > 240 || c.Game.SyncInterval < time.Second || c.Game.SyncInterval > time.Hour {
		return fmt.Errorf("invalid game timing")
	}
	if !finiteRange(c.Game.UnitsPerMeter, 0.001, 1000) {
		return fmt.Errorf("units per meter must be 0.001..1000")
	}
	if c.World.Width == 0 || c.World.Height == 0 || c.World.MaxX != c.World.Width || c.World.MaxY != c.World.Height {
		return fmt.Errorf("invalid world bounds")
	}
	if c.Server.Port < 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid port")
	}
	if c.Server.ManagementAddr != "" {
		if _, _, err := net.SplitHostPort(c.Server.ManagementAddr); err != nil {
			return fmt.Errorf("invalid management address: %w", err)
		}
	}
	for _, value := range []int{c.Net.FanoutDropStreak, c.Net.FanoutFairDebtMax, c.Net.FanoutFairDebtInc, c.Net.FanoutFairDebtDec} {
		if value < 0 || value > 100000 {
			return fmt.Errorf("fanout counters must be 0..100000 before conversion")
		}
	}
	return BuildLiveNetConfig(c).Validate()
}

func validateEnv() error {
	for _, key := range []string{"FANOUT_CRITICAL_BOOST_NS", "FANOUT_CRITICAL_WINDOW_MS", "FANOUT_DROP_STREAK", "FANOUT_FAIR_DEBT_DEC", "FANOUT_FAIR_DEBT_INC", "FANOUT_FAIR_DEBT_MAX", "FANOUT_FAIR_DEBT_WEIGHT_NS", "FANOUT_MAX_BROADCAST_BYTES_PER_TICK", "FANOUT_MAX_RECIPIENTS_PER_TICK", "FANOUT_MIN_RECIPIENTS_PER_TICK", "FANOUT_QUEUE_SHED_DEPTH", "FANOUT_ROUND_ROBIN_WEIGHT_NS", "FANOUT_TARGET_MS", "IP_CONN_BURST", "KEYFRAME_DIVISOR", "MAX_CONNECTIONS", "PORT", "RATE_LIMIT_BURST", "RATE_LIMIT_MSG_SEC", "SPAWN_MAX_X", "SPAWN_MAX_Y", "SPAWN_MIN_X", "SPAWN_MIN_Y", "SYNC_INTERVAL_SEC", "TICK_RATE", "WORKERS", "WORLD_HEIGHT", "WORLD_STATE_ACTIVE_STALENESS_MS", "WORLD_STATE_ACTIVE_WINDOW_MS", "WORLD_STATE_IDLE_STALENESS_MS", "WORLD_WIDTH", "WRITE_BATCH_SIZE"} {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		v, err := strconv.ParseInt(value, 10, 64)
		if err != nil || v < -1000000000000 || v > 1000000000000 {
			return fmt.Errorf("invalid integer for %s", key)
		}
		if len(key) >= 3 && key[len(key)-3:] == "_MS" && (v < 0 || v > 86400000) {
			return fmt.Errorf("invalid duration for %s", key)
		}
	}
	for _, key := range []string{"IP_CONN_RATE", "UNITS_PER_METER"} {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		v, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("invalid number for %s", key)
		}
	}
	for _, key := range []string{"ENABLE_PPROF", "VELOCITY_REPLICATION"} {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("invalid boolean for %s", key)
		}
	}
	return nil
}
