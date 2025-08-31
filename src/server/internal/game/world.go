package game

import (
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/systems"
	"pixi_game_server/internal/types"
)

// GameWorld управляет состоянием игрового мира
type GameWorld struct {
	cfg     *config.Config
	players sync.Map // map[uint32]*types.Player - lock-free player storage

	// High-performance systems
	visibilityManager *systems.VisibilityManager
	broadcastManager  *systems.BroadcastManager

	// Event processing
	eventChan chan types.GameEvent
	workerWG  sync.WaitGroup

	// Performance metrics
	tickDuration    int64  // atomic
	eventsProcessed uint64 // atomic
	lastSyncTime    int64  // atomic

	// Tick management
	ticker   *time.Ticker
	stopChan chan struct{}

	// Player ID generation
	nextPlayerID uint32 // atomic

	// State for full sync
	lastFullSync time.Time
} // NewGameWorld создает новый игровой мир
func NewGameWorld(cfg *config.Config) *GameWorld {
	// Initialize random seed for spawn positions
	rand.Seed(time.Now().UnixNano())

	gw := &GameWorld{
		cfg:          cfg,
		eventChan:    make(chan types.GameEvent, cfg.Net.EventChannelSize),
		stopChan:     make(chan struct{}),
		nextPlayerID: 1000, // Start from 1000 for easy debugging
		lastFullSync: time.Now(),
	}

	// Initialize high-performance systems
	gw.visibilityManager = systems.NewVisibilityManager(
		cfg.World.Width, cfg.World.Height, 100) // 100-unit grid cells
	gw.broadcastManager = systems.NewBroadcastManager(cfg.Net.BroadcastWorkers)

	// Start event processing workers
	workerCount := cfg.Server.Workers
	if workerCount == 0 {
		workerCount = 4 // Default worker count
	}

	for i := 0; i < workerCount; i++ {
		gw.workerWG.Add(1)
		go gw.eventWorker(i)
	}

	// Start game loop
	go gw.gameLoop()

	log.Printf("🌍 GameWorld initialized with %d workers, tick rate %dHz",
		workerCount, cfg.Game.TickRate)

	return gw
}

// AddPlayer добавляет нового игрока (lock-free)
func (gw *GameWorld) AddPlayer() *types.Player {
	playerID := atomic.AddUint32(&gw.nextPlayerID, 1)

	// Generate random spawn position within spawn area
	spawnRangeX := gw.cfg.World.SpawnMaxX - gw.cfg.World.SpawnMinX
	spawnRangeY := gw.cfg.World.SpawnMaxY - gw.cfg.World.SpawnMinY

	spawnX := gw.cfg.World.SpawnMinX + uint16(rand.Intn(int(spawnRangeX)))
	spawnY := gw.cfg.World.SpawnMinY + uint16(rand.Intn(int(spawnRangeY)))

	player := &types.Player{
		ID:             playerID,
		ViewportWidth:  800, // Default viewport
		ViewportHeight: 600,
		JoinTime:       time.Now(),
	}

	player.SetX(spawnX)
	player.SetY(spawnY)
	player.SetFacingRight(true)
	player.SetState(0) // idle state
	player.SetLastUpdate(time.Now().UnixNano())

	gw.players.Store(playerID, player)

	return player
}

// RemovePlayer удаляет игрока (lock-free)
func (gw *GameWorld) RemovePlayer(playerID uint32) {
	if _, loaded := gw.players.LoadAndDelete(playerID); loaded {
		// Send disconnect event to event processing
		gw.eventChan <- types.GameEvent{
			PlayerID:  playerID,
			Type:      types.EventDisconnect,
			Timestamp: time.Now().UnixNano(),
		}
	}
}

// ProcessEvent добавляет событие в очередь обработки
func (gw *GameWorld) ProcessEvent(event types.GameEvent) {
	event.Timestamp = time.Now().UnixNano()
	select {
	case gw.eventChan <- event:
		// Event queued successfully
	default:
		// Channel full - drop event (graceful degradation)
		log.Printf("⚠️  Event channel full, dropping event from player %d", event.PlayerID)
	}
}

// GetVisiblePlayers возвращает игроков видимых для данного игрока
func (gw *GameWorld) GetVisiblePlayers(playerID uint32, viewport types.ViewportBounds) []types.PlayerState {
	var visiblePlayers []types.PlayerState

	gw.players.Range(func(key, value interface{}) bool {
		player := value.(*types.Player)

		// Skip self
		if player.ID == playerID {
			return true
		}

		// Check if player is in viewport
		x, y := player.GetX(), player.GetY()
		if x >= viewport.MinX && x <= viewport.MaxX &&
			y >= viewport.MinY && y <= viewport.MaxY {
			visiblePlayers = append(visiblePlayers, player.ToState())
		}

		return true
	})

	return visiblePlayers
}

// GetAllPlayers возвращает всех игроков (для полной синхронизации)
func (gw *GameWorld) GetAllPlayers() []types.PlayerState {
	var allPlayers []types.PlayerState

	gw.players.Range(func(key, value interface{}) bool {
		player := value.(*types.Player)
		allPlayers = append(allPlayers, player.ToState())
		return true
	})

	return allPlayers
}

// GetPlayerCount возвращает количество подключенных игроков
func (gw *GameWorld) GetPlayerCount() int {
	count := 0
	gw.players.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// gameLoop главный игровой цикл
func (gw *GameWorld) gameLoop() {
	tickRate := time.Duration(1000/gw.cfg.Game.TickRate) * time.Millisecond
	gw.ticker = time.NewTicker(tickRate)
	defer gw.ticker.Stop()

	log.Printf("🔄 Game loop started with %v tick interval", tickRate)

	for {
		select {
		case <-gw.ticker.C:
			start := time.Now()
			gw.tick()
			duration := time.Since(start)
			atomic.StoreInt64(&gw.tickDuration, duration.Nanoseconds())

		case <-gw.stopChan:
			log.Println("🛑 Game loop stopped")
			return
		}
	}
}

// tick выполняет один тик игрового цикла
func (gw *GameWorld) tick() {
	// Process movement for all players
	gw.players.Range(func(key, value interface{}) bool {
		player := value.(*types.Player)
		gw.updatePlayerPosition(player)
		return true
	})

	// Check if full sync is needed
	now := time.Now()
	if now.Sub(gw.lastFullSync) >= gw.cfg.Game.SyncInterval {
		atomic.StoreInt64(&gw.lastSyncTime, now.UnixNano())
		gw.lastFullSync = now
	}
}

// updatePlayerPosition обновляет позицию игрока на основе его векторов движения
func (gw *GameWorld) updatePlayerPosition(player *types.Player) {
	if player.VX == 0 && player.VY == 0 {
		return // Player not moving
	}

	currentX := player.GetX()
	currentY := player.GetY()

	// Calculate new position using int32 to handle negative values
	newX32 := int32(currentX)
	newY32 := int32(currentY)

	if player.VX != 0 {
		newX32 += int32(player.VX) * int32(gw.cfg.Game.PlayerSpeedPerTick)
	}
	if player.VY != 0 {
		newY32 += int32(player.VY) * int32(gw.cfg.Game.PlayerSpeedPerTick)
	}

	// Apply world boundaries with wrapping
	maxX := int32(gw.cfg.World.MaxX)
	minX := int32(gw.cfg.World.MinX)
	maxY := int32(gw.cfg.World.MaxY)
	minY := int32(gw.cfg.World.MinY)

	if newX32 >= maxX {
		newX32 = minX
	} else if newX32 < minX {
		newX32 = maxX - 1
	}

	if newY32 >= maxY {
		newY32 = minY
	} else if newY32 < minY {
		newY32 = maxY - 1
	}

	// Convert back to uint16 after boundary checks
	newX := uint16(newX32)
	newY := uint16(newY32)

	// Update position atomically
	player.SetX(newX)
	player.SetY(newY)
	player.SetLastUpdate(time.Now().UnixNano())
}

// eventWorker обрабатывает события в отдельной горутине
func (gw *GameWorld) eventWorker(workerID int) {
	defer gw.workerWG.Done()

	log.Printf("🔧 Event worker %d started", workerID)

	for {
		select {
		case event := <-gw.eventChan:
			gw.handleEvent(event)
			atomic.AddUint64(&gw.eventsProcessed, 1)

		case <-gw.stopChan:
			log.Printf("🔧 Event worker %d stopped", workerID)
			return
		}
	}
}

// handleEvent обрабатывает одно событие
func (gw *GameWorld) handleEvent(event types.GameEvent) {
	playerInterface, exists := gw.players.Load(event.PlayerID)
	if !exists {
		return // Player no longer exists
	}

	player := playerInterface.(*types.Player)

	switch event.Type {
	case types.EventMove:
		// Validate movement (prevent cheating)
		if abs(int(event.VectorX)) <= 1 && abs(int(event.VectorY)) <= 1 {
			// Always update movement vectors, including stopping (0,0)
			player.VX = event.VectorX
			player.VY = event.VectorY
			player.SetClientTick(event.ClientTick)
		}

	case types.EventFace:
		player.SetFacingRight(event.FacingRight)

	case types.EventAttack:
		player.SetState(1) // Attack state

	case types.EventDisconnect:
		// Additional cleanup if needed

	case types.EventViewportUpdate:
		// Viewport updates handled in connection layer
	}
}

// GetMetrics возвращает метрики производительности
func (gw *GameWorld) GetMetrics() types.PerformanceMetrics {
	return types.PerformanceMetrics{
		ConnectedPlayers: uint32(gw.GetPlayerCount()),
		TickDuration:     time.Duration(atomic.LoadInt64(&gw.tickDuration)),
		EventsPerSecond:  atomic.LoadUint64(&gw.eventsProcessed),
	}
}

// Stop останавливает игровой мир
func (gw *GameWorld) Stop() {
	close(gw.stopChan)
	gw.workerWG.Wait()
	log.Println("🌍 GameWorld stopped")
}

// Helper function
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
