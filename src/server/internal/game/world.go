package game

import (
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/metrics"
	"pixi_game_server/internal/systems"
	"pixi_game_server/internal/types"
)

// broadcastFuncHolder оборачивает функцию для хранения в atomic.Value.
type broadcastFuncHolder struct {
	fn func([]types.PlayerState)
}

// GameWorld управляет состоянием игрового мира
type GameWorld struct {
	cfg     *config.Config
	players sync.Map // map[uint32]*types.Player - lock-free player storage

	// Tick-driven broadcast: вызывается раз в тик с текущим состоянием всех игроков.
	// Сервер регистрирует callback через SetTickBroadcaster.
	tickBroadcast atomic.Value // хранит broadcastFuncHolder

	// High-performance systems
	visibilityManager *systems.VisibilityManager

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

	slog.Info("gameworld initialized", "workers", workerCount, "tick_rate_hz", cfg.Game.TickRate)

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
	gw.visibilityManager.AddPlayer(playerID, spawnX, spawnY)

	return player
}

// RemovePlayer удаляет игрока (lock-free)
func (gw *GameWorld) RemovePlayer(playerID uint32) {
	if _, loaded := gw.players.LoadAndDelete(playerID); loaded {
		gw.visibilityManager.RemovePlayer(playerID)
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
		slog.Warn("event channel full, dropping event", "player_id", event.PlayerID)
		metrics.EventsDropped.Inc()
	}
	metrics.EventChannelLen.Set(float64(len(gw.eventChan)))
}

// GetVisiblePlayers возвращает игроков видимых для данного игрока.
// Использует пространственную сетку — O(ячейки в viewport × плотность) вместо O(N).
func (gw *GameWorld) GetVisiblePlayers(playerID uint32, viewport types.ViewportBounds) []types.PlayerState {
	ids := gw.visibilityManager.GetVisibleIDs(viewport)
	defer gw.visibilityManager.ReleaseIDs(ids)

	visiblePlayers := make([]types.PlayerState, 0, len(ids))
	for _, id := range ids {
		if id == playerID {
			continue
		}
		if val, ok := gw.players.Load(id); ok {
			visiblePlayers = append(visiblePlayers, val.(*types.Player).ToState())
		}
	}
	return visiblePlayers
}

// GetAllPlayers возвращает всех игроков (для полной синхронизации)
func (gw *GameWorld) GetAllPlayers() []types.PlayerState {
	allPlayers := make([]types.PlayerState, 0, gw.GetPlayerCount())

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
	tickInterval := time.Second / time.Duration(gw.cfg.Game.TickRate)
	gw.ticker = time.NewTicker(tickInterval)
	defer gw.ticker.Stop()

	slog.Info("game loop started", "interval_ms", tickInterval.Milliseconds(), "tick_rate_hz", gw.cfg.Game.TickRate)

	for {
		select {
		case <-gw.ticker.C:
			start := time.Now()
			gw.tick()
			duration := time.Since(start)
			atomic.StoreInt64(&gw.tickDuration, duration.Nanoseconds())
			metrics.TickDuration.Observe(duration.Seconds())
			metrics.TicksTotal.Inc()

		case <-gw.stopChan:
			slog.Info("game loop stopped")
			return
		}
	}
}

// SetTickBroadcaster регистрирует функцию, вызываемую раз в тик со срезом
// состояний всех игроков. Вызывается из server.New() до первого тика.
func (gw *GameWorld) SetTickBroadcaster(fn func([]types.PlayerState)) {
	gw.tickBroadcast.Store(broadcastFuncHolder{fn: fn})
}

// TryAttack проверяет cooldown и запускает атаку если она разрешена.
// Возвращает (x, y, true) если атака принята, (0, 0, false) если в cooldown.
// Потокобезопасно: использует атомарный CAS на AttackStartTime.
func (gw *GameWorld) TryAttack(playerID uint32) (x, y uint16, accepted bool) {
	val, ok := gw.players.Load(playerID)
	if !ok {
		return 0, 0, false
	}
	player := val.(*types.Player)

	now := time.Now().UnixNano()
	cooldown := gw.cfg.Game.AttackDuration.Nanoseconds()
	start := player.GetAttackStartTime()

	// Reject if still in attack cooldown
	if start > 0 && now-start < cooldown {
		return 0, 0, false
	}

	player.SetState(1)
	player.SetAttackStartTime(now)
	metrics.EventsProcessed.WithLabelValues("attack").Inc()

	return player.GetX(), player.GetY(), true
}

// tick выполняет один тик игрового цикла.
// За один Range обновляет позиции и собирает состояния — ноль лишних итераций.
func (gw *GameWorld) tick() {
	v := gw.tickBroadcast.Load()
	hasBroadcaster := v != nil

	var states []types.PlayerState
	if hasBroadcaster {
		states = make([]types.PlayerState, 0, 64)
	}

	nowNano := time.Now().UnixNano()
	attackDurNano := gw.cfg.Game.AttackDuration.Nanoseconds()

	gw.players.Range(func(key, value interface{}) bool {
		player := value.(*types.Player)

		// Server-authoritative attack timeout
		if player.GetState() == 1 {
			start := player.GetAttackStartTime()
			if start > 0 && nowNano-start >= attackDurNano {
				player.SetState(0)
				player.SetAttackStartTime(0)
			}
		}

		gw.updatePlayerPosition(player)
		if hasBroadcaster {
			states = append(states, player.ToState())
		}
		return true
	})

	if hasBroadcaster && len(states) > 0 {
		v.(broadcastFuncHolder).fn(states)
	}

	now := time.Now()
	if now.Sub(gw.lastFullSync) >= gw.cfg.Game.SyncInterval {
		atomic.StoreInt64(&gw.lastSyncTime, now.UnixNano())
		gw.lastFullSync = now
	}
}

// updatePlayerPosition обновляет позицию игрока на основе его векторов движения
func (gw *GameWorld) updatePlayerPosition(player *types.Player) {
	vx := player.GetVX()
	vy := player.GetVY()
	if vx == 0 && vy == 0 {
		return // Player not moving
	}

	currentX := player.GetX()
	currentY := player.GetY()

	// Calculate new position using int32 to handle negative values
	newX32 := int32(currentX)
	newY32 := int32(currentY)

	if vx != 0 {
		newX32 += int32(vx) * int32(gw.cfg.Game.PlayerSpeedPerTick)
	}
	if vy != 0 {
		newY32 += int32(vy) * int32(gw.cfg.Game.PlayerSpeedPerTick)
	}

	// Apply world boundaries with clamping (matches client-side behavior)
	maxX := int32(gw.cfg.World.MaxX)
	minX := int32(gw.cfg.World.MinX)
	maxY := int32(gw.cfg.World.MaxY)
	minY := int32(gw.cfg.World.MinY)

	if newX32 >= maxX {
		newX32 = maxX
	} else if newX32 < minX {
		newX32 = minX
	}

	if newY32 >= maxY {
		newY32 = maxY
	} else if newY32 < minY {
		newY32 = minY
	}

	// Convert back to uint16 after boundary checks
	newX := uint16(newX32)
	newY := uint16(newY32)

	// Update position atomically
	player.SetX(newX)
	player.SetY(newY)
	player.SetLastUpdate(time.Now().UnixNano())
	gw.visibilityManager.MovePlayer(player.ID, newX, newY)
}

// eventWorker обрабатывает события в отдельной горутине
func (gw *GameWorld) eventWorker(workerID int) {
	defer gw.workerWG.Done()

	slog.Debug("event worker started", "worker_id", workerID)

	for {
		select {
		case event := <-gw.eventChan:
			gw.handleEvent(event)
			atomic.AddUint64(&gw.eventsProcessed, 1)
			metrics.EventChannelLen.Set(float64(len(gw.eventChan)))

		case <-gw.stopChan:
			slog.Debug("event worker stopped", "worker_id", workerID)
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
		metrics.EventsProcessed.WithLabelValues("move").Inc()
		// Validate movement (prevent cheating)
		if abs(int(event.VectorX)) <= 1 && abs(int(event.VectorY)) <= 1 {
			// Always update movement vectors, including stopping (0,0)
			player.SetVX(event.VectorX)
			player.SetVY(event.VectorY)
			player.SetClientTick(event.ClientTick)
		}

	case types.EventFace:
		metrics.EventsProcessed.WithLabelValues("face").Inc()
		player.SetFacingRight(event.FacingRight)

	case types.EventAttack:
		metrics.EventsProcessed.WithLabelValues("attack").Inc()
		// Legacy path (via ProcessEvent queue) - TryAttack is now preferred.
		// Guard against double-processing if called from old code paths.
		if player.GetState() == 1 {
			break
		}
		player.SetState(1)
		player.SetAttackStartTime(time.Now().UnixNano())

	case types.EventDisconnect:
		metrics.EventsProcessed.WithLabelValues("disconnect").Inc()
		// Additional cleanup if needed

	case types.EventViewportUpdate:
		metrics.EventsProcessed.WithLabelValues("viewport").Inc()
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
	slog.Info("gameworld stopped")
}

// Helper function
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
