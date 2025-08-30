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
	SendChannelSize  int
	BroadcastWorkers int
	MessageRateLimit int
	BurstLimit       int
	RateLimitWindow  time.Duration
}

// JSONConfig represents the structure of gameConfig.json
type JSONConfig struct {
	Network struct {
		TickRate         int    `json:"tickRate"`
		SyncInterval     int    `json:"syncInterval"`
		BatchIntervalMs  int    `json:"batchIntervalMs"`
		Port             int    `json:"port"`
		Host             string `json:"host"`
		MaxConnections   int    `json:"maxConnections"`
		EventChannelSize int    `json:"eventChannelSize"`
		SendChannelSize  int    `json:"sendChannelSize"`
		BroadcastWorkers int    `json:"broadcastWorkers"`
		MessageRateLimit int    `json:"messageRateLimit"`
		BurstLimit       int    `json:"burstLimit"`
		ReadBufferSize   int    `json:"readBufferSize"`
		WriteBufferSize  int    `json:"writeBufferSize"`
		Workers          int    `json:"workers"`
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
		BaseScale      float64 `json:"baseScale"`
		AnimationSpeed float64 `json:"animationSpeed"`
	} `json:"player"`
	Game struct {
		DebugMode bool `json:"debugMode"`
	} `json:"game"`
}

// loadJSONConfig loads the shared gameConfig.json file
// func loadJSONConfig() (*JSONConfig, error) {
// 	// Try different paths for the config file
// 	configPaths := []string{
// 		"gameConfig.json",                  // In the same directory as binary (for dist/)
// 		"src/shared/gameConfig.json",       // From project root
// 		"../src/shared/gameConfig.json",    // From dist directory
// 		"../../src/shared/gameConfig.json", // From deeper nested dirs
// 		"shared/gameConfig.json",           // Alternative path
// 	}

// 	var data []byte
// 	var err error

// 	for _, path := range configPaths {
// 		data, err = os.ReadFile(path)
// 		if err == nil {
// 			break
// 		}
// 	}

// 	if err != nil {
// 		return nil, fmt.Errorf("failed to read config file from any of the paths: %w", err)
// 	}

// 	var config JSONConfig
// 	err = json.Unmarshal(data, &config)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to parse config file: %w", err)
// 	}

// 	return &config, nil
// }

func Load() *Config {
	// Try to load from JSON config first (external file), then embedded, then fallback to env vars
	// jsonConfig, err := loadJSONConfig()

	// If external config loading fails, try embedded config
	// if err != nil {
	// 	fmt.Printf("Warning: Could not load external JSON config (%v), trying embedded config\n", err)
	// 	jsonConfig, err = loadEmbeddedConfig()
	// 	if err != nil {
	// 		fmt.Printf("Warning: Could not load embedded config (%v), using defaults\n", err)
	// 	}
	// }

	jsonConfig, err := loadEmbeddedConfig()
	if err != nil {
		fmt.Printf("Error: Could not load embedded config (%v), using defaults\n", err)
		os.Exit(1)
	}

	var defaultTickRate, defaultSyncInterval, defaultBatchInterval int
	var defaultPlayerSpeed, defaultPort, defaultMaxConn, defaultEventChanSize, defaultSendChanSize int
	var defaultBroadcastWorkers, defaultMessageRateLimit, defaultBurstLimit int
	var defaultReadBufferSize, defaultWriteBufferSize, defaultWorkers int
	var defaultWorldWidth, defaultWorldHeight int
	var defaultSpawnMinX, defaultSpawnMaxX, defaultSpawnMinY, defaultSpawnMaxY int
	var defaultHost string

	if err != nil {
		// Fallback to default values if JSON config loading fails
		fmt.Printf("Warning: Could not load JSON config (%v), using defaults\n", err)
		defaultTickRate = 60
		defaultSyncInterval = 30
		defaultBatchInterval = 50
		defaultPlayerSpeed = 4
		defaultPort = 8108
		defaultHost = "0.0.0.0"
		defaultMaxConn = 12000
		defaultEventChanSize = 100000
		defaultSendChanSize = 256
		defaultBroadcastWorkers = 0
		defaultMessageRateLimit = 60
		defaultBurstLimit = 10
		defaultReadBufferSize = 4096
		defaultWriteBufferSize = 4096
		defaultWorkers = 0
		defaultWorldWidth = 2000
		defaultWorldHeight = 2000
		defaultSpawnMinX = 100
		defaultSpawnMaxX = 900
		defaultSpawnMinY = 100
		defaultSpawnMaxY = 900
	} else {
		// Use values from JSON config
		defaultTickRate = jsonConfig.Network.TickRate
		defaultSyncInterval = jsonConfig.Network.SyncInterval / 1000 // Convert to seconds
		defaultBatchInterval = jsonConfig.Network.BatchIntervalMs
		defaultPlayerSpeed = jsonConfig.Movement.PlayerSpeedPerTick
		defaultPort = jsonConfig.Network.Port
		defaultHost = jsonConfig.Network.Host
		defaultMaxConn = jsonConfig.Network.MaxConnections
		defaultEventChanSize = jsonConfig.Network.EventChannelSize
		defaultSendChanSize = jsonConfig.Network.SendChannelSize
		defaultBroadcastWorkers = jsonConfig.Network.BroadcastWorkers
		defaultMessageRateLimit = jsonConfig.Network.MessageRateLimit
		defaultBurstLimit = jsonConfig.Network.BurstLimit
		defaultReadBufferSize = jsonConfig.Network.ReadBufferSize
		defaultWriteBufferSize = jsonConfig.Network.WriteBufferSize
		defaultWorkers = jsonConfig.Network.Workers
		defaultWorldWidth = jsonConfig.World.VirtualSize.Width
		defaultWorldHeight = jsonConfig.World.VirtualSize.Height
		defaultSpawnMinX = jsonConfig.World.SpawnArea.MinX
		defaultSpawnMaxX = jsonConfig.World.SpawnArea.MaxX
		defaultSpawnMinY = jsonConfig.World.SpawnArea.MinY
		defaultSpawnMaxY = jsonConfig.World.SpawnArea.MaxY
	}

	return &Config{
		Server: ServerConfig{
			Port:            getEnvInt("PORT", defaultPort),
			Host:            getEnvString("HOST", defaultHost),
			Workers:         getEnvInt("WORKERS", defaultWorkers),
			ReadBufferSize:  getEnvInt("READ_BUFFER_SIZE", defaultReadBufferSize),
			WriteBufferSize: getEnvInt("WRITE_BUFFER_SIZE", defaultWriteBufferSize),
			StaticDir:       getEnvString("STATIC_DIR", "../dist"),
		},
		Game: GameConfig{
			TickRate:           getEnvInt("TICK_RATE", defaultTickRate),
			SyncInterval:       time.Duration(getEnvInt("SYNC_INTERVAL_SEC", defaultSyncInterval)) * time.Second,
			BatchInterval:      time.Duration(getEnvInt("BATCH_INTERVAL_MS", defaultBatchInterval)) * time.Millisecond,
			PlayerSpeedPerTick: getEnvInt("PLAYER_SPEED", defaultPlayerSpeed),
		},
		World: WorldConfig{
			Width:     uint16(getEnvInt("WORLD_WIDTH", defaultWorldWidth)),
			Height:    uint16(getEnvInt("WORLD_HEIGHT", defaultWorldHeight)),
			SpawnMinX: uint16(getEnvInt("SPAWN_MIN_X", defaultSpawnMinX)),
			SpawnMaxX: uint16(getEnvInt("SPAWN_MAX_X", defaultSpawnMaxX)),
			SpawnMinY: uint16(getEnvInt("SPAWN_MIN_Y", defaultSpawnMinY)),
			SpawnMaxY: uint16(getEnvInt("SPAWN_MAX_Y", defaultSpawnMaxY)),
			MinX:      0,
			MaxX:      uint16(getEnvInt("WORLD_WIDTH", defaultWorldWidth)),
			MinY:      0,
			MaxY:      uint16(getEnvInt("WORLD_HEIGHT", defaultWorldHeight)),
		},
		Net: NetworkConfig{
			MaxConnections:   getEnvInt("MAX_CONNECTIONS", defaultMaxConn),
			EventChannelSize: getEnvInt("EVENT_CHANNEL_SIZE", defaultEventChanSize),
			SendChannelSize:  getEnvInt("SEND_CHANNEL_SIZE", defaultSendChanSize),
			BroadcastWorkers: getEnvInt("BROADCAST_WORKERS", defaultBroadcastWorkers),
			MessageRateLimit: getEnvInt("RATE_LIMIT_MSG_SEC", defaultMessageRateLimit),
			BurstLimit:       getEnvInt("RATE_LIMIT_BURST", defaultBurstLimit),
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
