package game

import (
	"log/slog"
	"math/rand"
	"runtime"
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

// tickWorkerInput — chunk of player pointers dispatched to a persistent tick worker.
// Workers do only the CPU-heavy part (position update + attack timeout).
// State snapshot (ToState + delta) remains sequential in the gameLoop goroutine.
type tickWorkerInput struct {
	ptrs          []*types.Player
	nowNano       int64
	attackDurNano int64
}

// GameWorld управляет состоянием игрового мира
type GameWorld struct {
	cfg        *config.Config
	playersMu  sync.RWMutex
	playersMap map[uint32]*types.Player

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
	// scratchPtrs holds a snapshot of player pointers taken under a brief RLock each tick.
	// Processing (position update + ToState) happens outside the lock since all Player
	// fields are atomic — the lock only protects the map structure itself.
	scratchPtrs []*types.Player
	// Persistent tick worker pool (pattern from nbio/nakama).
	// Workers are created once in NewGameWorld; each tick dispatches a chunk of players
	// via a buffered channel. Workers do only the expensive part (updatePlayerPosition +
	// attack timeout). State collection (ToState + delta) stays in gameLoop goroutine.
	// Avoids per-tick goroutine spawn overhead (~2µs/goroutine × N workers).
	nTickWorkers  int
	tickWorkerChs []chan tickWorkerInput
	tickWorkerWg  sync.WaitGroup // Performance metrics
	tickDuration  int64          // atomic
	lastSyncTime  int64          // atomic

	// Tick management
	ticker   *time.Ticker
	stopChan chan struct{}

	// Player ID generation
	nextPlayerID uint32 // atomic

	// Estimated player count for pre-allocation
	playerCountEstimate uint32 // atomic

	// State for full sync
	lastFullSync      time.Time
	lastBroadcastNano int64

	// Throttled diagnostics
	lastSlowTickLog int64 // atomic UnixNano timestamp
}

// NewGameWorld создает новый игровой мир
func NewGameWorld(cfg *config.Config) *GameWorld {
	initialCap := cfg.Net.MaxConnections
	if initialCap < 256 {
		initialCap = 256
	} else if initialCap > 16384 {
		initialCap = 16384
	}
	changedCap := initialCap / 8
	if changedCap < 64 {
		changedCap = 64
	}

	gw := &GameWorld{
		cfg:            cfg,
		playersMap:     make(map[uint32]*types.Player, 256),
		stopChan:       make(chan struct{}),
		nextPlayerID:   1000, // Start from 1000 for easy debugging
		lastFullSync:   time.Now(),
		prevStates:     make(map[uint32]types.PlayerState, initialCap),
		scratchStates:  make([]types.PlayerState, 0, initialCap),
		scratchChanged: make([]types.PlayerState, 0, changedCap),
		scratchSeenIDs: make(map[uint32]struct{}, initialCap),
		scratchPtrs:    make([]*types.Player, 0, initialCap),
	}

	// Spawn persistent tick workers — one per logical CPU.
	// Pattern: nbio TaskPool / nakama runtime worker pool.
	// Workers receive chunks of player pointers, process them, signal done via WaitGroup.
	// Channels are buffered=1 so gameLoop never blocks on dispatch.
	n := runtime.GOMAXPROCS(0)
	gw.nTickWorkers = n
	gw.tickWorkerChs = make([]chan tickWorkerInput, n)
	for i := range gw.tickWorkerChs {
		ch := make(chan tickWorkerInput, 1)
		gw.tickWorkerChs[i] = ch
		go gw.runTickWorker(ch)
	}

	// Initialize high-performance systems
	gw.visibilityManager = systems.NewVisibilityManager(
		cfg.World.Width, cfg.World.Height, 100) // 100-unit grid cells

	// Start game loop
	go gw.gameLoop()

	slog.Info("gameworld initialized",
		"tick_rate_hz", cfg.Game.TickRate,
		"batch_interval_ms", cfg.Game.BatchInterval.Milliseconds())

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
		ID:       playerID,
		JoinTime: time.Now(),
	}

	player.SetX(spawnX)
	player.SetY(spawnY)
	player.SetFacingRight(true)
	player.SetState(0) // idle state
	player.SetLastUpdate(time.Now().UnixNano())

	gw.playersMu.Lock()
	gw.playersMap[playerID] = player
	gw.playersMu.Unlock()
	gw.visibilityManager.AddPlayer(playerID, spawnX, spawnY)
	atomic.AddUint32(&gw.playerCountEstimate, 1)

	return player
}

// RemovePlayer удаляет игрока (lock-free)
func (gw *GameWorld) RemovePlayer(playerID uint32) {
	gw.playersMu.Lock()
	_, loaded := gw.playersMap[playerID]
	if loaded {
		delete(gw.playersMap, playerID)
	}
	gw.playersMu.Unlock()
	if loaded {
		gw.visibilityManager.RemovePlayer(playerID)
		atomic.AddUint32(&gw.playerCountEstimate, ^uint32(0)) // decrement
		metrics.EventsProcessed.WithLabelValues("disconnect").Inc()
	}
}

// ProcessEvent обрабатывает событие инлайн (все операции atomic, нет нужды в канале/воркерах).
func (gw *GameWorld) ProcessEvent(event types.GameEvent) {
	gw.handleEvent(event)
}

// GetAllPlayers возвращает всех игроков (для полной синхронизации)
func (gw *GameWorld) GetAllPlayers() []types.PlayerState {
	gw.playersMu.RLock()
	allPlayers := make([]types.PlayerState, 0, len(gw.playersMap))
	for _, player := range gw.playersMap {
		allPlayers = append(allPlayers, player.ToState())
	}
	gw.playersMu.RUnlock()
	return allPlayers
}

// GetPlayerCount возвращает количество подключенных игроков
func (gw *GameWorld) GetPlayerCount() int {
	gw.playersMu.RLock()
	count := len(gw.playersMap)
	gw.playersMu.RUnlock()
	return count
}

// gameLoop главный игровой цикл
func (gw *GameWorld) gameLoop() {
	// Automatic GC: epoll refactor reduced goroutines from ~2400 to ~70.
	// STW now takes ~70 × 3µs ≈ 0.2ms — safe to let Go run GC automatically.
	// Manual runtime.GC() every 5s caused 100ms blocking pauses because
	// GOGC=-1 allowed memory to accumulate without incremental marking.

	tickInterval := time.Second / time.Duration(gw.cfg.Game.TickRate)
	gw.ticker = time.NewTicker(tickInterval)
	defer gw.ticker.Stop()

	slog.Info("game loop started",
		"interval_ms", tickInterval.Milliseconds(),
		"tick_rate_hz", gw.cfg.Game.TickRate)

	for {
		select {
		case <-gw.ticker.C:
			start := time.Now()
			gw.tick()
			duration := time.Since(start)
			atomic.StoreInt64(&gw.tickDuration, duration.Nanoseconds())
			metrics.TickDuration.Observe(duration.Seconds())
			metrics.TicksTotal.Inc()

			if duration > tickInterval {
				nowNano := time.Now().UnixNano()
				prev := atomic.LoadInt64(&gw.lastSlowTickLog)
				if nowNano-prev >= int64(5*time.Second) &&
					atomic.CompareAndSwapInt64(&gw.lastSlowTickLog, prev, nowNano) {
					slog.Warn("slow tick detected",
						"duration_ms", duration.Milliseconds(),
						"budget_ms", tickInterval.Milliseconds(),
						"players", gw.GetPlayerCount())
				}
			}

		case <-gw.stopChan:
			slog.Info("game loop stopped")
			return
		}
	}
}

// SetTickBroadcaster регистрирует функцию, вызываемую раз в тик со срезом
// состояний всех игроков. Вызывается из server.New() до первого тика.
// Функция вызывается синхронно из tick() — broadcastTick делает push() в
// writeQueue каждого соединения (non-blocking), поэтому задержка tick'а минимальна.
func (gw *GameWorld) SetTickBroadcaster(fn func(all []types.PlayerState, changed []types.PlayerState, fullSync bool)) {
	gw.broadcastFn.Store(broadcastFuncHolder{fn: fn})
}

// TryAttack проверяет cooldown и запускает атаку если она разрешена.
// Возвращает (x, y, true) если атака принята, (0, 0, false) если в cooldown.
// Потокобезопасно: использует атомарный CAS на AttackStartTime.
func (gw *GameWorld) TryAttack(playerID uint32) (x, y uint16, accepted bool) {
	gw.playersMu.RLock()
	player, ok := gw.playersMap[playerID]
	gw.playersMu.RUnlock()
	if !ok {
		return 0, 0, false
	}

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
	// Full sync is controlled by configured SyncInterval (usually tens of seconds),
	// not by tick rate. Full-sync every second explodes outbound traffic.
	lastSync := atomic.LoadInt64(&gw.lastSyncTime)
	fullSync := lastSync == 0 || time.Duration(nowNano-lastSync) >= gw.cfg.Game.SyncInterval
	if fullSync {
		atomic.StoreInt64(&gw.lastSyncTime, nowNano)
		gw.lastFullSync = time.Unix(0, nowNano)
	}

	t0 := time.Now()
	// Snapshot player pointers under a minimal RLock — only protects the map structure.
	// All Player fields (X, Y, VX, VY, State, ...) are atomic and safe to read/write
	// without holding the lock. Lock hold time: ~N×8ns (pointer copy) instead of ~N×200ns
	// (atomic reads + position math), reducing contention with epoll movement writers.
	gw.scratchPtrs = gw.scratchPtrs[:0]
	gw.playersMu.RLock()
	for _, p := range gw.playersMap {
		gw.scratchPtrs = append(gw.scratchPtrs, p)
	}
	gw.playersMu.RUnlock()

	// Parallel position update: dispatch chunks to persistent workers (one per CPU).
	// Workers do attack timeout + updatePlayerPosition (atomic writes to player fields).
	// IMPORTANT: wg.Add(n) must be called BEFORE sending to channels, otherwise a fast
	// worker could call wg.Done() before wg.Add(), causing a panic or missed wait.
	n := gw.nTickWorkers
	total := len(gw.scratchPtrs)
	if total > 0 {
		chunkSize := (total + n - 1) / n
		activeWorkers := 0
		for i := range gw.tickWorkerChs {
			start := i * chunkSize
			if start >= total {
				break
			}
			activeWorkers++
		}
		// Add BEFORE any send — prevents Done() racing ahead of Add().
		gw.tickWorkerWg.Add(activeWorkers)
		for i, ch := range gw.tickWorkerChs {
			start := i * chunkSize
			if start >= total {
				break
			}
			end := min(start+chunkSize, total)
			ch <- tickWorkerInput{
				ptrs:          gw.scratchPtrs[start:end],
				nowNano:       nowNano,
				attackDurNano: attackDurNano,
			}
		}
		gw.tickWorkerWg.Wait()
	}

	// Sequential state collection — ToState() is fast (atomic reads only).
	// No synchronisation needed: only the gameLoop goroutine writes scratchStates.
	for _, player := range gw.scratchPtrs {
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
	}
	t1 := time.Now()
	metrics.TickPhaseDuration.WithLabelValues("range").Observe(t1.Sub(t0).Seconds())
	metrics.TickPhaseDuration.WithLabelValues("world_step").Observe(t1.Sub(t0).Seconds())
	metrics.TickWorldStepDuration.Observe(t1.Sub(t0).Seconds())

	// Update prevStates: remove departed players, update existing.
	for id := range gw.prevStates {
		if _, ok := gw.scratchSeenIDs[id]; !ok {
			delete(gw.prevStates, id)
		}
	}
	for _, st := range gw.scratchStates {
		gw.prevStates[st.ID] = st
	}
	t2 := time.Now()
	metrics.TickPhaseDuration.WithLabelValues("delta").Observe(t2.Sub(t1).Seconds())

	if len(gw.scratchStates) == 0 {
		return
	}

	// Delta metrics: how many players changed state this tick.
	changedCount := len(gw.scratchChanged)
	if fullSync {
		changedCount = len(gw.scratchStates)
	}
	metrics.DeltaPlayersCount.Observe(float64(changedCount))
	metrics.DeltaRatio.Set(float64(changedCount) / float64(len(gw.scratchStates)))

	// No-op tick: avoid broadcasting identical state when no player changed.
	if !fullSync && changedCount == 0 {
		return
	}

	batchIntervalNano := gw.cfg.Game.BatchInterval.Nanoseconds()
	shouldBroadcast := fullSync || gw.lastBroadcastNano == 0 ||
		batchIntervalNano <= 0 || nowNano-gw.lastBroadcastNano >= batchIntervalNano

	if !shouldBroadcast {
		return
	}

	gw.lastBroadcastNano = nowNano

	// Call broadcastFn synchronously — it enqueues one push() per connection (non-blocking
	// lock+append), then returns in microseconds. No allCopy/changedCopy allocations needed:
	// EncodeGameState serialises scratchStates into bytes before tick() returns.
	if holder, ok := gw.broadcastFn.Load().(broadcastFuncHolder); ok {
		if fullSync {
			holder.fn(gw.scratchStates, nil, true)
		} else {
			holder.fn(gw.scratchStates, gw.scratchChanged, false)
		}
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

	if newX != currentX || newY != currentY {
		gw.visibilityManager.MovePlayer(player.ID, newX, newY)
	}
}

// handleEvent обрабатывает одно событие инлайн (atomic-операции, потокобезопасно)
func (gw *GameWorld) handleEvent(event types.GameEvent) {
	gw.playersMu.RLock()
	player, exists := gw.playersMap[event.PlayerID]
	gw.playersMu.RUnlock()
	if !exists {
		return // Player no longer exists
	}

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
	// Close worker channels so runTickWorker goroutines exit cleanly.
	for _, ch := range gw.tickWorkerChs {
		close(ch)
	}
	slog.Info("gameworld stopped")
}

// runTickWorker is a persistent goroutine (one per logical CPU) that processes
// a chunk of players per tick: attack timeout + position update.
// Only the CPU-heavy atomic operations run here; state snapshot (ToState + delta)
// stays sequential in the gameLoop goroutine to avoid synchronisation on scratch slices.
// Pattern sourced from nbio TaskPool and nakama runtime worker pool.
func (gw *GameWorld) runTickWorker(ch chan tickWorkerInput) {
	for input := range ch {
		for _, player := range input.ptrs {
			// Server-authoritative attack timeout
			if player.GetState() == 1 {
				start := player.GetAttackStartTime()
				if start > 0 && input.nowNano-start >= input.attackDurNano {
					player.SetState(0)
					player.SetAttackStartTime(0)
				}
			}
			gw.updatePlayerPosition(player, input.nowNano)
		}
		gw.tickWorkerWg.Done()
	}
}

// Helper function
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
