package game

import (
	"log/slog"
	"math"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"pixi_game_server/internal/clock"
	"pixi_game_server/internal/config"
	"pixi_game_server/internal/metrics"
	"pixi_game_server/internal/systems"
	"pixi_game_server/internal/types"
	"pixi_game_server/internal/units"
)

type broadcastFuncHolder struct {
	fn func(all []types.PlayerState, changed []types.PlayerState, fullSync bool, worldTick uint32) bool
}

type tickWorkerInput struct {
	ptrs      []*types.Player
	nowNano   int64
	worldTick uint32
}

// GameWorld управляет состоянием игрового мира
type GameWorld struct {
	cfg                   *config.Config
	playersMu             sync.RWMutex
	playersMap            map[uint32]*types.Player
	broadcastFn           atomic.Value // stores broadcastFuncHolder
	visibilityManager     *systems.VisibilityManager
	prevStates            map[uint32]types.PlayerState
	tickCount             uint32
	scratchStates         []types.PlayerState
	scratchChanged        []types.PlayerState
	scratchSeenIDs        map[uint32]struct{}
	scratchPtrs           []*types.Player
	nTickWorkers          int
	tickWorkerChs         []chan tickWorkerInput
	tickWorkerWg          sync.WaitGroup // Performance metrics
	tickDuration          int64          // atomic
	lastSyncTime          int64          // atomic
	ticker                atomic.Pointer[time.Ticker]
	stopChan              chan struct{}
	nominalTickIntervalNs int64 // atomic
	currentTickIntervalNs int64 // atomic
	// attackDurationTicks is per unit type (windup+active+recovery, GDD/UNITS.md),
	// not one flat cooldown for everyone — see NewGameWorld.
	attackDurationTicks map[uint8]uint32
	// comboSteps is how many steps this unit's attack combo chain has (1 = no
	// combo, just re-attacks) — see units.Definition.ComboSteps.
	comboSteps map[uint8]uint8
	// comboWindowTicks is how long past a swing's own duration a new swing still
	// continues the chain instead of resetting to step 1 — see
	// units.Definition.ComboWindowSeconds.
	comboWindowTicks         map[uint8]uint32
	nextPlayerID             uint32 // atomic
	playerCountEstimate      uint32 // atomic
	lastFullSync             time.Time
	deltaVectorChanges       int
	deltaPositionOnly        int
	deltaClamped             int
	deltaKeyframes           int
	prevBaselineTick         uint32
	keyframeCursor           uint32
	deltaWindowVectorChanges int64
	deltaWindowPositionOnly  int64
	deltaWindowClamped       int64
	deltaWindowKeyframes     int64
	deltaWindowBroadcasts    int64
	lastSlowTickLog          int64 // atomic monotonic timestamp
	lastDeltaCompositeLog    int64 // atomic monotonic timestamp
	staminaStats             map[uint8]staminaStat
	moveStats                map[uint8]moveStat
}

// moveStat holds the fixed-point per-unit movement rate (GDD §60 World Coordinate
// Resolution) for one unit type.
type moveStat struct {
	// milliUnitsPerTick is 1/1000 world units per tick — moveSpeed(m/s) *
	// UnitsPerMeter * 1000 / TickRate. Rarely a whole number; the remainder
	// accumulates in Player.MoveRemainderMilli and flushes a whole unit at a time
	// (see updatePlayerPosition), the same fixed-point technique as centi-stamina.
	milliUnitsPerTick uint32
	// avgUnitsPerTick is milliUnitsPerTick rounded to the nearest whole unit — used
	// only by classifyDelta's "is this predictable" bandwidth heuristic, never for
	// actual movement. Guessing wrong there only costs bandwidth (the record gets
	// included instead of omitted), never correctness.
	avgUnitsPerTick int32
}

type staminaStat struct {
	maxCenti          uint16
	regenPerTickCenti uint16
	// canBlock mirrors whether units.Definition.Block is set — units without a
	// block profile (e.g. Rogue, Archer, per docs/UNITS.md) never enter StateBlocking.
	canBlock               bool
	blockDrainPerTickCenti uint16
	// attackStaminaCostCenti is 0 for units with no AttackStaminaCost (GDD: a "flat
	// cost per swing, where the doc specifies one") — indistinguishable from a real
	// zero-cost swing, which is the desired behavior either way.
	attackStaminaCostCenti uint16
	// Sprint (GDD §54/§57) is per-unit, not a global constant — see
	// units.Definition.SprintSpeedMultiplier/SprintStaminaCostPerSecond.
	sprintSpeedMultiplier   float64
	sprintDrainPerTickCenti uint16
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

	// Regen is per-tick, not per-wall-clock-second, so it dilates along with
	// movement/attacks under time dilation (see SetTickInterval) instead of quietly
	// running at a different effective rate than the rest of the simulation.
	staminaStats := make(map[uint8]staminaStat, len(units.All()))
	for _, def := range units.All() {
		stat := staminaStat{
			maxCenti:          uint16(math.Round(def.Stamina * 100)),
			regenPerTickCenti: uint16(math.Round(def.StaminaRegenPerSecond * 100 / float64(cfg.Game.TickRate))),
		}
		if def.Block != nil {
			stat.canBlock = true
			stat.blockDrainPerTickCenti = uint16(math.Round(def.Block.DrainPerSecond * 100 / float64(cfg.Game.TickRate)))
		}
		if def.AttackStaminaCost != nil {
			stat.attackStaminaCostCenti = uint16(math.Round(*def.AttackStaminaCost * 100))
		}
		stat.sprintSpeedMultiplier = def.SprintSpeedMultiplier
		stat.sprintDrainPerTickCenti = uint16(math.Round(def.SprintStaminaCostPerSecond * 100 / float64(cfg.Game.TickRate)))
		staminaStats[def.TypeID] = stat
	}

	// Per-unit movement rate (GDD §60 World Coordinate Resolution) instead of one
	// flat server-wide speed — every unit now actually moves at its own moveSpeed.
	moveStats := make(map[uint8]moveStat, len(units.All()))
	for _, def := range units.All() {
		milli := def.MoveSpeed * cfg.Game.UnitsPerMeter * 1000 / float64(cfg.Game.TickRate)
		moveStats[def.TypeID] = moveStat{
			milliUnitsPerTick: uint32(math.Round(milli)),
			avgUnitsPerTick:   int32(math.Round(milli / 1000)),
		}
	}

	// Attack cooldown per unit type instead of one flat duration for everyone —
	// windup+active+recovery are already per-unit in units.json (e.g. Spearman
	// ~1.02s vs Greatsword ~1.70s), they just weren't wired to the actual cooldown.
	attackDurationTicks := make(map[uint8]uint32, len(units.All()))
	for _, def := range units.All() {
		seconds := def.WindupSeconds + def.ActiveSeconds + def.RecoverySeconds
		ticks := uint32(math.Round(seconds * float64(cfg.Game.TickRate)))
		if ticks < 1 {
			ticks = 1
		}
		attackDurationTicks[def.TypeID] = ticks
	}

	// Combo chain length/window per unit (docs/UNITS.md doesn't have a dedicated
	// section for this yet) — units with no ComboSteps set (archer, rogue, citizen:
	// no attack1/attack2 art, see spriteLoader.ts's melee-attack fallback) default
	// to 1, i.e. every swing just re-attacks with no chain.
	comboSteps := make(map[uint8]uint8, len(units.All()))
	comboWindowTicks := make(map[uint8]uint32, len(units.All()))
	for _, def := range units.All() {
		steps := def.ComboSteps
		if steps < 1 {
			steps = 1
		}
		comboSteps[def.TypeID] = uint8(steps)
		comboWindowTicks[def.TypeID] = uint32(math.Round(def.ComboWindowSeconds * float64(cfg.Game.TickRate)))
	}

	gw := &GameWorld{
		cfg:          cfg,
		playersMap:   make(map[uint32]*types.Player, 256),
		stopChan:     make(chan struct{}),
		nextPlayerID: 1000, // Start from 1000 for easy debugging
		lastFullSync: time.Now(),
		// Start the window now so the first composition log covers a real interval
		// rather than the single broadcast that happens to be first.
		lastDeltaCompositeLog: clock.Now(),
		prevStates:            make(map[uint32]types.PlayerState, initialCap),
		scratchStates:         make([]types.PlayerState, 0, initialCap),
		scratchChanged:        make([]types.PlayerState, 0, changedCap),
		scratchSeenIDs:        make(map[uint32]struct{}, initialCap),
		scratchPtrs:           make([]*types.Player, 0, initialCap),
		staminaStats:          staminaStats,
		moveStats:             moveStats,
		attackDurationTicks:   attackDurationTicks,
		comboSteps:            comboSteps,
		comboWindowTicks:      comboWindowTicks,
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

	// Set before spawning gameLoop so GetNominalTickInterval/GetTickInterval are safe
	// to call immediately after NewGameWorld returns (Server reads them at startup).
	nominalInterval := time.Second / time.Duration(cfg.Game.TickRate)
	gw.nominalTickIntervalNs = nominalInterval.Nanoseconds()
	gw.currentTickIntervalNs = nominalInterval.Nanoseconds()

	// Start game loop
	go gw.gameLoop()

	slog.Info("gameworld initialized",
		"tick_rate_hz", cfg.Game.TickRate,
		"tick_interval_ms", nominalInterval.Milliseconds())

	return gw
}

// AddPlayer добавляет нового игрока (lock-free). requestedUnitType is whatever the
// client asked for at connect time (e.g. the "unit" query param) — unknown or empty
// values fall back to units.DefaultUnitType, so this never fails.
func (gw *GameWorld) AddPlayer(requestedUnitType string) *types.Player {
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

	unitDef := units.Get(requestedUnitType)

	player.SetX(spawnX)
	player.SetY(spawnY)
	player.SetDirection(0) // 0 = right, see protocol/binary.go direction encoding
	player.SetState(types.StateIdle)
	player.SetUnitType(unitDef.TypeID)
	// Full HP/stamina at spawn. HP still never drains (no damage resolution yet);
	// stamina now does, via attacks/block/sprint (see TryAttack, updateBlockDrain,
	// updatePlayerPosition) — see types.UnitAssignment for the replication caveat.
	player.SetHP(uint16(math.Round(unitDef.HP)))
	player.SetStaminaCenti(uint16(math.Round(unitDef.Stamina * 100)))
	player.SetLastUpdate(clock.Now())

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

// QueueMovementInput stores the newest fixed-rate input sample. Network delivery can
// coalesce samples before a server tick; old samples never become movement backlog.
func (gw *GameWorld) QueueMovementInput(playerID uint32, dx, dy int8, sequence uint32, sprint bool) types.InputResult {
	if abs(int(dx)) > 1 || abs(int(dy)) > 1 {
		return types.InputInvalid
	}
	gw.playersMu.RLock()
	player, exists := gw.playersMap[playerID]
	gw.playersMu.RUnlock()
	if !exists {
		return types.InputInvalid
	}
	result := player.OfferMovementInput(types.MovementInput{Sequence: sequence, DX: dx, DY: dy, Sprint: sprint})
	if result == types.InputAccepted {
		metrics.EventsProcessed.WithLabelValues("move").Inc()
	}
	return result
}

// GetTickCount returns the current simulation tick. Clients use it as the baseline
// for dead reckoning, so it must be readable from connection goroutines.
func (gw *GameWorld) GetTickCount() uint32 {
	return atomic.LoadUint32(&gw.tickCount)
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

// GetAllUnitAssignments returns every connected player's unit type and current
// HP/stamina. Unlike PlayerState, this is not replicated every tick — it is read
// only when a client needs the roster (on connect, and once per full-sync cycle so
// existing clients learn about players who joined since their last roster). See
// types.UnitAssignment for why that cadence is fine today but won't be once combat
// can actually change HP/stamina.
func (gw *GameWorld) GetAllUnitAssignments() []types.UnitAssignment {
	gw.playersMu.RLock()
	assignments := make([]types.UnitAssignment, 0, len(gw.playersMap))
	for _, player := range gw.playersMap {
		assignments = append(assignments, types.UnitAssignment{
			ID:             player.ID,
			UnitType:       player.GetUnitType(),
			CurrentHP:      player.GetHP(),
			CurrentStamina: player.GetStaminaCenti(),
		})
	}
	gw.playersMu.RUnlock()
	return assignments
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
	// Keep Go's concurrent GC enabled. Linux epoll bounds read goroutines, while
	// each connection still owns one writer goroutine. Forced runtime.GC cycles
	// would turn allocation spikes into avoidable latency spikes.

	tickInterval := gw.GetNominalTickInterval()
	ticker := time.NewTicker(tickInterval)
	gw.ticker.Store(ticker)
	defer ticker.Stop()

	slog.Info("game loop started",
		"interval_ms", tickInterval.Milliseconds(),
		"tick_rate_hz", gw.cfg.Game.TickRate)

	for {
		select {
		case scheduled := <-ticker.C:
			start := time.Now()
			metrics.TickStartDelay.Observe(start.Sub(scheduled).Seconds())
			gw.tick()
			duration := time.Since(start)
			atomic.StoreInt64(&gw.tickDuration, duration.Nanoseconds())
			metrics.TickDuration.Observe(duration.Seconds())
			metrics.TicksTotal.Inc()

			budget := gw.GetTickInterval()
			if duration > budget {
				nowNano := clock.Now()
				prev := atomic.LoadInt64(&gw.lastSlowTickLog)
				if nowNano-prev >= int64(5*time.Second) &&
					atomic.CompareAndSwapInt64(&gw.lastSlowTickLog, prev, nowNano) {
					slog.Warn("slow tick detected",
						"duration_ms", duration.Milliseconds(),
						"budget_ms", budget.Milliseconds(),
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
func (gw *GameWorld) SetTickBroadcaster(fn func(all []types.PlayerState, changed []types.PlayerState, fullSync bool, worldTick uint32) bool) {
	gw.broadcastFn.Store(broadcastFuncHolder{fn: fn})
}

// SetTickInterval changes the simulation tick period going forward — this is the
// mechanism behind time dilation. Movement-per-tick and cooldown-per-tick stay in
// tick-units unchanged; slowing the ticker itself is what stretches them in
// wall-clock time, same as EVE's TiDi. In production this always runs on the
// gameLoop goroutine (Server calls it from inside broadcastTick, which tick()
// invokes synchronously), but ticker is an atomic.Pointer so this is also safe to
// call from any other goroutine — e.g. a test exercising the dilation controller
// directly, or a future caller that doesn't share that assumption.
func (gw *GameWorld) SetTickInterval(d time.Duration) {
	t := gw.ticker.Load()
	if d <= 0 || t == nil {
		return
	}
	atomic.StoreInt64(&gw.currentTickIntervalNs, d.Nanoseconds())
	t.Reset(d)
}

// GetTickInterval returns the ticker's current (possibly dilated) period.
func (gw *GameWorld) GetTickInterval() time.Duration {
	return time.Duration(atomic.LoadInt64(&gw.currentTickIntervalNs))
}

// GetNominalTickInterval returns the configured TickRate baseline, fixed at startup.
func (gw *GameWorld) GetNominalTickInterval() time.Duration {
	return time.Duration(atomic.LoadInt64(&gw.nominalTickIntervalNs))
}

// GetTickDuration returns how long the most recently completed tick took to compute.
func (gw *GameWorld) GetTickDuration() time.Duration {
	return time.Duration(atomic.LoadInt64(&gw.tickDuration))
}

// TryAttack проверяет cooldown и запускает атаку если она разрешена.
// Возвращает (x, y, true) если атака принята, (0, 0, false) если в cooldown.
// Cooldown измеряется в тиках, не в wall-clock времени: под time dilation тик идёт
// реже, значит те же attackDurationTicks растягиваются в реальном времени так же,
// как и движение — атака честно замедляется вместе с остальной симуляцией.
func (gw *GameWorld) TryAttack(playerID uint32) (x, y uint16, accepted bool) {
	gw.playersMu.RLock()
	player, ok := gw.playersMap[playerID]
	gw.playersMu.RUnlock()
	if !ok {
		return 0, 0, false
	}

	// Can't swing while holding a shield up (GDD §54 — block is a stance, not an
	// interrupt), and stationary since block sets vx/vy to 0 by construction (see
	// TryBlockStart) is not a concern here.
	if player.GetState() == types.StateBlocking {
		return 0, 0, false
	}

	currentTick := gw.GetTickCount()
	start := player.GetAttackStartTick()

	// Still mid-swing: buffer this press instead of dropping it outright. The
	// instant the current swing's cooldown ends (see runTickWorker), the buffered
	// press fires the next combo step automatically — the same result as the
	// player clicking again at the exact right instant, without requiring
	// frame-perfect timing. Unsigned subtraction wraps correctly for any
	// realistic elapsed tick count, same convention as movement sequence math.
	if start > 0 && currentTick-start < gw.attackDurationTicks[player.GetUnitType()] {
		player.SetPendingComboInput(true)
		return 0, 0, false
	}

	return gw.executeAttack(player, currentTick)
}

// executeAttack performs one swing: advances the combo chain (GDD §54-adjacent —
// no doc section of its own yet), spends stamina, and enters StateAttacking. Shared
// by TryAttack (fresh client input) and the buffered-combo continuation in
// runTickWorker, so a press queued mid-swing fires identically to a fresh one.
// Not tied to attack specifically by design — any future action that should chain
// into the same combo (a different mouse button, a keybound skill) can reuse this
// once it's wired to call it.
func (gw *GameWorld) executeAttack(player *types.Player, currentTick uint32) (x, y uint16, accepted bool) {
	// Continue the chain only if this swing starts within the previous swing's
	// combo window (see ComboExpireTick in executeAttack's own write below);
	// otherwise it's a fresh combo starting over at step 1. Wraps back to 1 past
	// the unit's max step count so the combo can be performed over and over.
	step := uint8(1)
	if currentTick <= player.GetComboExpireTick() {
		step = player.GetComboStep() + 1
		if step > gw.comboSteps[player.GetUnitType()] {
			step = 1
		}
	}

	// Spam должен наказываться (GDD §57): an attack that can't afford its stamina
	// cost is rejected outright rather than queued or partially charged. Every
	// combo step costs the same as a single swing for now — no per-step cost
	// differentiation yet, matching the unit's one shared attack timing/damage.
	if stat, ok := gw.staminaStats[player.GetUnitType()]; ok && stat.attackStaminaCostCenti > 0 {
		if !player.TrySpendStaminaCenti(stat.attackStaminaCostCenti) {
			player.SetPendingComboInput(false)
			return 0, 0, false
		}
	}

	player.SetComboStep(step)
	player.SetComboExpireTick(currentTick + gw.attackDurationTicks[player.GetUnitType()] + gw.comboWindowTicks[player.GetUnitType()])
	player.SetState(types.StateAttacking)
	player.SetAttackStartTick(currentTick)
	player.SetPendingComboInput(false)
	metrics.EventsProcessed.WithLabelValues("attack").Inc()

	return player.GetX(), player.GetY(), true
}

// TryBlockStart enters StateBlocking (RMB pressed) if the unit has a block profile
// (docs/UNITS.md — Rogue/Archer etc. have none), isn't mid-swing, and has stamina
// left. Like attack, it overrides in-progress movement rather than requiring the
// player already be stationary (see the velocity reset below). Holding block
// itself is drained/cancelled per tick, see updateBlockDrain.
func (gw *GameWorld) TryBlockStart(playerID uint32) bool {
	gw.playersMu.RLock()
	player, ok := gw.playersMap[playerID]
	gw.playersMu.RUnlock()
	if !ok {
		return false
	}

	stat, ok := gw.staminaStats[player.GetUnitType()]
	if !ok || !stat.canBlock {
		return false
	}
	if player.GetState() == types.StateAttacking {
		return false
	}
	if player.GetStaminaCenti() == 0 {
		return false
	}

	// Block is a stance (GDD §54), and — same as attack (see TryAttack, which has
	// no movement precondition either) — pressing it overrides whatever movement
	// was in progress rather than being silently rejected while moving. Zeroing
	// velocity here (instead of requiring it already be zero) is what makes RMB
	// actually stop the player; updateBlockDrain still auto-cancels the stance the
	// instant a still-held movement key reasserts non-zero velocity next tick.
	player.SetVX(0)
	player.SetVY(0)
	player.SetState(types.StateBlocking)
	return true
}

// EndBlock releases StateBlocking (RMB released). A no-op if the player wasn't
// blocking (e.g. movement already cancelled it — see updateBlockDrain).
func (gw *GameWorld) EndBlock(playerID uint32) {
	gw.playersMu.RLock()
	player, ok := gw.playersMap[playerID]
	gw.playersMu.RUnlock()
	if !ok {
		return
	}
	if player.GetState() == types.StateBlocking {
		player.SetState(types.StateIdle)
	}
}

// updateBlockDrain runs once per tick per player. It auto-cancels block the instant
// movement or an empty stamina bar makes it invalid (GDD §54: "попытка сдвинуться
// немедленно снимает block"), otherwise applies this tick's stamina drain. Must run
// after updatePlayerPosition so VX/VY reflect any movement input applied this tick.
func (gw *GameWorld) updateBlockDrain(player *types.Player) (drained bool) {
	if player.GetState() != types.StateBlocking {
		return false
	}
	if player.GetVX() != 0 || player.GetVY() != 0 {
		player.SetState(types.StateIdle)
		return false
	}
	if player.GetStaminaCenti() == 0 {
		player.SetState(types.StateIdle)
		return false
	}

	stat, ok := gw.staminaStats[player.GetUnitType()]
	if !ok {
		return false
	}
	player.SpendStaminaUpTo(stat.blockDrainPerTickCenti)
	return true
}

// tick выполняет один тик игрового цикла.
// За один Range обновляет позиции, собирает состояния и вычисляет дельту.
// Scratch-буферы переиспользуются между тиками — нет аллокаций на горячем пути.
func (gw *GameWorld) tick() {
	// Reset scratch buffers without allocating.
	gw.scratchStates = gw.scratchStates[:0]
	gw.scratchChanged = gw.scratchChanged[:0]
	clear(gw.scratchSeenIDs)

	nowNano := clock.Now()

	worldTick := atomic.AddUint32(&gw.tickCount, 1)
	// Full sync is controlled by configured SyncInterval (usually tens of seconds),
	// not by tick rate. Full-sync every second explodes outbound traffic.
	lastSync := atomic.LoadInt64(&gw.lastSyncTime)
	fullSync := lastSync == 0 || time.Duration(nowNano-lastSync) >= gw.cfg.Game.SyncInterval
	if fullSync {
		atomic.StoreInt64(&gw.lastSyncTime, nowNano)
		gw.lastFullSync = time.Now()
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
				ptrs:      gw.scratchPtrs[start:end],
				nowNano:   nowNano,
				worldTick: worldTick,
			}
		}
		gw.tickWorkerWg.Wait()
	}

	// Sequential state collection — ToState() is fast (atomic reads only).
	// No synchronisation needed: only the gameLoop goroutine writes scratchStates.
	gw.deltaVectorChanges = 0
	gw.deltaPositionOnly = 0
	gw.deltaClamped = 0
	gw.deltaKeyframes = 0

	elapsedTicks := int32(worldTick - gw.prevBaselineTick)
	if elapsedTicks < 0 {
		elapsedTicks = 0
	}
	velocityReplication := gw.cfg.Net.VelocityReplication
	keyframeMod := uint32(0)
	if gw.cfg.Net.KeyframeDivisor > 0 {
		keyframeMod = uint32(gw.cfg.Net.KeyframeDivisor)
	}

	for _, player := range gw.scratchPtrs {
		st := player.ToState()
		gw.scratchStates = append(gw.scratchStates, st)
		gw.scratchSeenIDs[st.ID] = struct{}{}

		// Delta: compare with the last state accepted by the replication layer.
		if !fullSync {
			prev, exists := gw.prevStates[st.ID]
			speed := gw.moveStats[player.GetUnitType()].avgUnitsPerTick
			reason := classifyDelta(st, prev, exists, elapsedTicks, speed, velocityReplication)

			// Keyframe rotation refreshes a slice of the world every broadcast so a
			// client that missed a record — shed by the fanout, or lost — converges
			// without waiting for that player to change direction.
			keyframe := keyframeMod > 0 && !reason.include && st.ID%keyframeMod == gw.keyframeCursor

			if reason.include || keyframe {
				gw.scratchChanged = append(gw.scratchChanged, st)
			}
			if keyframe {
				gw.deltaKeyframes++
			}
			if reason.unpredictable {
				gw.deltaVectorChanges++
			} else if reason.positionOnly {
				gw.deltaPositionOnly++
			}
			if reason.diverged {
				gw.deltaClamped++
			}
		}
	}
	t1 := time.Now()
	metrics.TickPhaseDuration.WithLabelValues("range").Observe(t1.Sub(t0).Seconds())
	metrics.TickPhaseDuration.WithLabelValues("world_step").Observe(t1.Sub(t0).Seconds())
	metrics.TickWorldStepDuration.Observe(t1.Sub(t0).Seconds())

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

	// A tick with no replicated change still reaches the replication layer, where
	// transition ACKs are emitted independently from the state payload.
	// broadcastTick paces this pass itself and sends no state frame for an empty delta.

	// Call broadcastFn synchronously — it enqueues one push() per connection (non-blocking
	// lock+append), then returns in microseconds. No allCopy/changedCopy allocations needed:
	// EncodeGameState serialises scratchStates into bytes before tick() returns.
	if holder, ok := gw.broadcastFn.Load().(broadcastFuncHolder); ok {
		broadcasted := false
		if fullSync {
			broadcasted = holder.fn(gw.scratchStates, nil, true, worldTick)
		} else {
			broadcasted = holder.fn(gw.scratchStates, gw.scratchChanged, false, worldTick)
		}
		if broadcasted {
			gw.reportDeltaComposition()
			gw.prevBaselineTick = worldTick
			if mod := gw.cfg.Net.KeyframeDivisor; mod > 0 {
				gw.keyframeCursor = (gw.keyframeCursor + 1) % uint32(mod)
			}

			// prevStates is the last broadcast baseline, not merely the previous
			// simulation tick. This accumulates changes while replication is paced.
			for id := range gw.prevStates {
				if _, seen := gw.scratchSeenIDs[id]; !seen {
					delete(gw.prevStates, id)
				}
			}
			for _, st := range gw.scratchStates {
				gw.prevStates[st.ID] = st
			}
		}
	}

}

// deltaReason explains why a player belongs in the outgoing delta.
type deltaReason struct {
	include bool
	// unpredictable: velocity, state or facing changed — input a client cannot derive.
	unpredictable bool
	// diverged: the actual position differs from what integrating the baseline velocity
	// predicts. In practice this is a world-boundary clamp, including the diagonal case
	// where only one axis is clamped and a "did it move at all" test would miss it.
	diverged bool
	// positionOnly: the record exists solely because position advanced exactly as
	// predicted — the traffic velocity replication removes.
	positionOnly bool
}

// classifyDelta compares a player against the last state the replication layer
// accepted, elapsedTicks simulation steps ago.
//
// Prediction is deliberately unclamped: it mirrors what a dead-reckoning client
// computes, so any clamp the server applied shows up as a divergence and is sent.
// ClientTick is ignored — it is not part of the wire record, so including it would
// append players whose encoded bytes match the baseline. Their input sequence still
// reaches the client, because broadcastTick emits movement ACKs regardless of whether
// the delta payload is empty.
func classifyDelta(st, prev types.PlayerState, exists bool, elapsedTicks, speed int32, velocityReplication bool) deltaReason {
	if !exists {
		return deltaReason{include: true, unpredictable: true}
	}

	// ComboStep is checked on its own, not just State: a buffered combo
	// continuation (see game/world.go executeAttack/runTickWorker) can go
	// straight from one swing into the next without State ever visibly leaving
	// Attacking between the two per-tick snapshots this compares, so State alone
	// wouldn't flag the new swing as unpredictable and the record could get
	// compressed away — silently dropping the client's cue to replay the attack
	// animation for the new step.
	unpredictable := st.VX != prev.VX || st.VY != prev.VY ||
		st.State != prev.State || st.Direction != prev.Direction || st.Sprinting != prev.Sprinting ||
		st.ComboStep != prev.ComboStep

	predictedX := int32(prev.X) + int32(prev.VX)*speed*elapsedTicks
	predictedY := int32(prev.Y) + int32(prev.VY)*speed*elapsedTicks
	diverged := int32(st.X) != predictedX || int32(st.Y) != predictedY
	positionMoved := st.X != prev.X || st.Y != prev.Y

	r := deltaReason{
		unpredictable: unpredictable,
		diverged:      diverged,
		positionOnly:  !unpredictable && !diverged && positionMoved,
	}
	if velocityReplication {
		r.include = unpredictable || diverged
	} else {
		r.include = unpredictable || positionMoved
	}
	return r
}

// reportDeltaComposition records what the delta that just went out was made of.
// It runs only on ticks that actually broadcast, so the numbers describe real traffic
// rather than intermediate state accumulated between paced broadcasts.
func (gw *GameWorld) reportDeltaComposition() {
	total := gw.deltaVectorChanges + gw.deltaPositionOnly
	if total == 0 {
		return
	}

	metrics.DeltaVectorChanges.Observe(float64(gw.deltaVectorChanges))
	metrics.DeltaPositionOnly.Observe(float64(gw.deltaPositionOnly))
	metrics.DeltaClampedPlayers.Observe(float64(gw.deltaClamped))
	metrics.DeltaKeyframes.Observe(float64(gw.deltaKeyframes))

	predictable := float64(gw.deltaPositionOnly) / float64(total)
	metrics.DeltaPredictableRatio.Set(predictable)

	// Also log periodically: this number decides whether velocity replication is
	// worth building, and it should be readable without a metrics stack attached.
	// Logged as a window aggregate — one tick's composition is too noisy to act on.
	gw.deltaWindowVectorChanges += int64(gw.deltaVectorChanges)
	gw.deltaWindowPositionOnly += int64(gw.deltaPositionOnly)
	gw.deltaWindowClamped += int64(gw.deltaClamped)
	gw.deltaWindowKeyframes += int64(gw.deltaKeyframes)
	gw.deltaWindowBroadcasts++

	nowNano := clock.Now()
	prev := atomic.LoadInt64(&gw.lastDeltaCompositeLog)
	if nowNano-prev < int64(30*time.Second) ||
		!atomic.CompareAndSwapInt64(&gw.lastDeltaCompositeLog, prev, nowNano) {
		return
	}

	windowTotal := gw.deltaWindowVectorChanges + gw.deltaWindowPositionOnly
	projected := gw.deltaWindowVectorChanges + gw.deltaWindowClamped
	reduction := 0.0
	if projected > 0 {
		reduction = float64(windowTotal) / float64(projected)
	}
	slog.Info("delta composition",
		"broadcasts", gw.deltaWindowBroadcasts,
		"records", windowTotal,
		"vector_changes", gw.deltaWindowVectorChanges,
		"position_only", gw.deltaWindowPositionOnly,
		"diverged", gw.deltaWindowClamped,
		"keyframes", gw.deltaWindowKeyframes,
		"predictable_pct", int(100*float64(gw.deltaWindowPositionOnly)/float64(windowTotal)),
		"projected_records", projected,
		"projected_reduction_x", math.Round(reduction*100)/100)

	gw.deltaWindowVectorChanges = 0
	gw.deltaWindowPositionOnly = 0
	gw.deltaWindowClamped = 0
	gw.deltaWindowKeyframes = 0
	gw.deltaWindowBroadcasts = 0
}

// updatePlayerPosition consumes at most one latest sample and advances exactly one
// authoritative server step. A burst can change the current vector but cannot add
// extra distance or leave STOP trapped behind stale MOVE packets.
// Returns whether this tick's step spent sprint stamina, so the caller can skip
// passive regen for the same tick (see runTickWorker).
func (gw *GameWorld) updatePlayerPosition(player *types.Player, nowNano int64) (sprintDrained bool) {
	originalX := player.GetX()
	originalY := player.GetY()
	input, appliedInput := player.ConsumeLatestMovementInput()
	if appliedInput {
		player.SetVX(input.DX)
		player.SetVY(input.DY)
		player.SetSprint(input.Sprint)
	}

	vx, vy := player.GetVX(), player.GetVY()
	sprinting := false
	if vx != 0 || vy != 0 {
		stat := gw.staminaStats[player.GetUnitType()]
		// Sprint (GDD §54/§57): only takes effect while actually moving and only
		// while stamina remains — the instant it hits zero, movement silently falls
		// back to normal speed rather than the request being denied outright.
		sprinting = player.GetSprint() && player.GetStaminaCenti() > 0
		if sprinting {
			player.SpendStaminaUpTo(stat.sprintDrainPerTickCenti)
			sprintDrained = true
		}

		// Fixed-point per-unit movement (GDD §60 World Coordinate Resolution): the
		// unit's own rate rarely lands on a whole world-unit-per-tick step, so the
		// fractional part accumulates in MoveRemainderMilli and flushes a whole unit
		// once it crosses 1000 — same technique as centi-stamina, just for distance.
		milliRate := gw.moveStats[player.GetUnitType()].milliUnitsPerTick
		rateMultiplier := 1.0
		if sprinting {
			rateMultiplier *= stat.sprintSpeedMultiplier
		}
		// Moving on both axes at once (vx and vy both nonzero) would otherwise cover
		// sqrt(2) times the distance of an axis-aligned move per tick — the classic
		// diagonal-speed bug. Scale by 1/sqrt(2) so diagonal speed matches straight
		// speed. Folded into one multiplier/one rounding step (matched exactly by
		// the client's movement.ts/movementController.ts) rather than two chained
		// roundings, so client and server can't drift apart from rounding order.
		if vx != 0 && vy != 0 {
			rateMultiplier *= 1 / math.Sqrt2
		}
		if rateMultiplier != 1.0 {
			milliRate = uint32(math.Round(float64(milliRate) * rateMultiplier))
		}
		remainder := player.GetMoveRemainderMilli() + milliRate
		distance := int64(remainder / 1000)
		player.SetMoveRemainderMilli(remainder % 1000)

		newX, newY := gw.integrateMovement(originalX, originalY, vx, vy, distance)
		player.SetX(newX)
		player.SetY(newY)
		player.SetLastUpdate(nowNano)
	}
	// Replicated as PlayerState.Sprinting so remote clients can pick walk vs run
	// (GDD §54) instead of only the local player's own client-side prediction.
	player.SetSprintingNow(sprinting)

	finalX, finalY := player.GetX(), player.GetY()
	if appliedInput {
		player.SetMovementAck(input.Sequence, finalX, finalY)
	}
	if finalX != originalX || finalY != originalY {
		gw.visibilityManager.MovePlayer(player.ID, finalX, finalY)
	}
	return sprintDrained
}

func (gw *GameWorld) integrateMovement(x, y uint16, vx, vy int8, distance int64) (uint16, uint16) {
	newX := int64(x) + int64(vx)*distance
	newY := int64(y) + int64(vy)*distance
	newX = max(int64(gw.cfg.World.MinX), min(newX, int64(gw.cfg.World.MaxX)))
	newY = max(int64(gw.cfg.World.MinY), min(newY, int64(gw.cfg.World.MaxY)))
	return uint16(newX), uint16(newY)
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
		player.OfferMovementInput(types.MovementInput{
			Sequence: event.InputSequence,
			DX:       event.VectorX,
			DY:       event.VectorY,
		})

	case types.EventFace:
		metrics.EventsProcessed.WithLabelValues("face").Inc()
		player.SetDirection(event.Direction)

	case types.EventAttack:
		metrics.EventsProcessed.WithLabelValues("attack").Inc()
		// Legacy path (via ProcessEvent queue) - TryAttack is now preferred.
		// Guard against double-processing if called from old code paths.
		if player.GetState() == types.StateAttacking {
			break
		}
		player.SetState(types.StateAttacking)
		player.SetAttackStartTick(gw.GetTickCount())
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
			// Server-authoritative attack timeout, measured in ticks so it dilates
			// with the simulation the same way movement does.
			if player.GetState() == types.StateAttacking {
				start := player.GetAttackStartTick()
				if start > 0 && input.worldTick-start >= gw.attackDurationTicks[player.GetUnitType()] {
					player.SetState(types.StateIdle)
					player.SetAttackStartTick(0)
					// A press buffered mid-swing (see TryAttack) fires the next combo
					// step immediately — the same result as the player clicking again
					// at the exact right instant.
					if player.GetPendingComboInput() {
						gw.executeAttack(player, input.worldTick)
					}
				}
			}
			// updateBlockDrain must run after updatePlayerPosition: it reads this
			// tick's VX/VY to auto-cancel block the instant the player moves.
			sprintDrained := gw.updatePlayerPosition(player, input.nowNano)
			blockDrained := gw.updateBlockDrain(player)
			// Stamina doesn't regen the same tick it's spent — letting both happen
			// at once would let a high-regen unit sprint or block for near-free,
			// undermining the whole point of the cost (GDD §57: "Spam должен
			// наказываться").
			if !sprintDrained && !blockDrained {
				gw.regenStamina(player)
			}
		}
		gw.tickWorkerWg.Done()
	}
}

// regenStamina applies one tick of passive stamina regen (GDD §8, Stamina Economy).
// A no-op once stamina is at max, or on a tick that already spent stamina on
// something else (see runTickWorker).
func (gw *GameWorld) regenStamina(player *types.Player) {
	stat, ok := gw.staminaStats[player.GetUnitType()]
	if !ok {
		return
	}
	player.RegenStamina(stat.regenPerTickCenti, stat.maxCenti)
}

// Helper function
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
