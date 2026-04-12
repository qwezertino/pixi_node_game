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
	fn func(all []types.PlayerState, changed []types.PlayerState, fullSync bool)
}

// GameWorld управляет состоянием игрового мира
type GameWorld struct {
	cfg     *config.Config
	players sync.Map // map[uint32]*types.Player - lock-free player storage

	// Tick-driven broadcast: вызывается раз в тик с текущим состоянием всех игроков.
	// Хранится в atomic.Value — записывается один раз из SetTickBroadcaster,
	// читается из gameLoop горутины. Прямой вызов из tick() — никаких аллокаций.
	broadcastFn atomic.Value // stores broadcastFuncHolder

	// High-performance systems
	visibilityManager *systems.VisibilityManager

	// Delta tracking: previous tick state for each player
	prevStates map[uint32]types.PlayerState
	tickCount  uint32 // counts ticks for periodic full sync
	// Reusable scratch buffers for tick() — only touched from gameLoop goroutine, no sync needed.
	scratchStates  []types.PlayerState
	scratchChanged []types.PlayerState
	scratchSeenIDs map[uint32]struct{}
	// Performance metrics
	tickDuration int64 // atomic
	lastSyncTime int64 // atomic

	// Tick management
	ticker   *time.Ticker
	stopChan chan struct{}

	// Player ID generation
	nextPlayerID uint32 // atomic

	// Estimated player count for pre-allocation
	playerCountEstimate uint32 // atomic

	// State for full sync
	lastFullSync time.Time
}

// NewGameWorld создает новый игровой мир
func NewGameWorld(cfg *config.Config) *GameWorld {
	// Initialize random seed for spawn positions
	rand.Seed(time.Now().UnixNano())

	gw := &GameWorld{
		cfg:            cfg,
		stopChan:       make(chan struct{}),
		nextPlayerID:   1000, // Start from 1000 for easy debugging
		lastFullSync:   time.Now(),
		prevStates:     make(map[uint32]types.PlayerState, 256),
		scratchStates:  make([]types.PlayerState, 0, 256),
		scratchChanged: make([]types.PlayerState, 0, 64),
		scratchSeenIDs: make(map[uint32]struct{}, 256),
	}

	// Initialize high-performance systems
	gw.visibilityManager = systems.NewVisibilityManager(
		cfg.World.Width, cfg.World.Height, 100) // 100-unit grid cells

	// Start game loop
	go gw.gameLoop()

	slog.Info("gameworld initialized", "tick_rate_hz", cfg.Game.TickRate)

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
	atomic.AddUint32(&gw.playerCountEstimate, 1)

	return player
}

// RemovePlayer удаляет игрока (lock-free)
func (gw *GameWorld) RemovePlayer(playerID uint32) {
	if _, loaded := gw.players.LoadAndDelete(playerID); loaded {
		gw.visibilityManager.RemovePlayer(playerID)
		atomic.AddUint32(&gw.playerCountEstimate, ^uint32(0)) // decrement
		metrics.EventsProcessed.WithLabelValues("disconnect").Inc()
	}
}

// ProcessEvent обрабатывает событие инлайн (все операции atomic, нет нужды в канале/воркерах).
func (gw *GameWorld) ProcessEvent(event types.GameEvent) {
	gw.handleEvent(event)
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
// Функция вызывается синхронно из tick() — broadcastTick делает только
// 8 non-blocking channel sends, поэтому задержка tick'а минимальна.
func (gw *GameWorld) SetTickBroadcaster(fn func(all []types.PlayerState, changed []types.PlayerState, fullSync bool)) {
	gw.broadcastFn.Store(broadcastFuncHolder{fn: fn})
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
// За один Range обновляет позиции, собирает состояния и вычисляет дельту.
// Scratch-буферы переиспользуются между тиками — нет аллокаций на горячем пути.
func (gw *GameWorld) tick() {
	// Reset scratch buffers without allocating.
	gw.scratchStates = gw.scratchStates[:0]
	gw.scratchChanged = gw.scratchChanged[:0]
	clear(gw.scratchSeenIDs)

	nowNano := time.Now().UnixNano()
	attackDurNano := gw.cfg.Game.AttackDuration.Nanoseconds()

	gw.tickCount++
	// Full sync every TickRate ticks (~1 second)
	fullSync := gw.tickCount%uint32(gw.cfg.Game.TickRate) == 0

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

		gw.updatePlayerPosition(player, nowNano)
		st := player.ToState()
		gw.scratchStates = append(gw.scratchStates, st)
		gw.scratchSeenIDs[st.ID] = struct{}{}

		// Delta: compare with previous tick
		if !fullSync {
			prev, exists := gw.prevStates[st.ID]
			if !exists || st.X != prev.X || st.Y != prev.Y ||
				st.VX != prev.VX || st.VY != prev.VY ||
				st.State != prev.State || st.FacingRight != prev.FacingRight {
				gw.scratchChanged = append(gw.scratchChanged, st)
			}
		}

		return true
	})

	// Update prevStates: remove departed players, update existing.
	for id := range gw.prevStates {
		if _, ok := gw.scratchSeenIDs[id]; !ok {
			delete(gw.prevStates, id)
		}
	}
	for _, st := range gw.scratchStates {
		gw.prevStates[st.ID] = st
	}

	if len(gw.scratchStates) == 0 {
		return
	}

	// Call broadcastFn synchronously — it does only 8 non-blocking channel sends,
	// so it returns in microseconds. No allCopy/changedCopy allocations needed:
	// EncodeGameState serialises scratchStates into bytes before tick() returns.
	if holder, ok := gw.broadcastFn.Load().(broadcastFuncHolder); ok {
		var changed []types.PlayerState
		if !fullSync && len(gw.scratchChanged) > 0 {
			changed = gw.scratchChanged
		}
		holder.fn(gw.scratchStates, changed, fullSync)
	}

	if time.Duration(nowNano-atomic.LoadInt64(&gw.lastSyncTime)) >= gw.cfg.Game.SyncInterval {
		atomic.StoreInt64(&gw.lastSyncTime, nowNano)
		gw.lastFullSync = time.Unix(0, nowNano)
	}
}

// updatePlayerPosition обновляет позицию игрока на основе его векторов движения.
// nowNano передаётся из tick() чтобы избежать лишних time.Now() на горячем пути.
func (gw *GameWorld) updatePlayerPosition(player *types.Player, nowNano int64) {
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
	player.SetLastUpdate(nowNano)
	gw.visibilityManager.MovePlayer(player.ID, newX, newY)
}

// handleEvent обрабатывает одно событие инлайн (atomic-операции, потокобезопасно)
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
	}
}

// GetMetrics возвращает метрики производительности
func (gw *GameWorld) GetMetrics() types.PerformanceMetrics {
	return types.PerformanceMetrics{
		ConnectedPlayers: uint32(gw.GetPlayerCount()),
		TickDuration:     time.Duration(atomic.LoadInt64(&gw.tickDuration)),
	}
}

// Stop останавливает игровой мир
func (gw *GameWorld) Stop() {
	close(gw.stopChan)
	slog.Info("gameworld stopped")
}

// Helper function
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
