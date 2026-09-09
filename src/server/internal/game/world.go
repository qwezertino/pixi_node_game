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
	fn func(all []types.PlayerState, changed []types.PlayerState, fullSync bool, worldTick uint32, computeDur time.Duration) bool
}

type tickWorkerInput struct {
	ptrs      []*types.Player
	nowNano   int64
	worldTick uint32
}

type GameWorld struct {
	stepMu                sync.Mutex
	stopOnce              sync.Once
	loopDone              chan struct{}
	workersDone           sync.WaitGroup
	membershipChanged     bool
	cfg                   *config.Config
	live                  *config.LiveNet
	playersMu             sync.RWMutex
	playersMap            map[uint32]*types.Player
	broadcastFn           atomic.Value
	visibilityManager     *systems.VisibilityManager
	prevStates            map[uint32]types.PlayerState
	prevMoveRemainder     map[uint32]uint32
	tickCount             uint32
	scratchStates         []types.PlayerState
	scratchChanged        []types.PlayerState
	scratchRemainders     []uint32
	scratchSeenIDs        map[uint32]struct{}
	scratchPtrs           []*types.Player
	nTickWorkers          int
	tickWorkerChs         []chan tickWorkerInput
	tickWorkerWg          sync.WaitGroup
	tickDuration          int64
	lastSyncTime          int64
	ticker                atomic.Pointer[time.Ticker]
	stopChan              chan struct{}
	nominalTickIntervalNs int64
	currentTickIntervalNs int64

	nextPlayerID             uint32
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
	lastSlowTickLog          int64
	lastDeltaCompositeLog    int64
	unitTablesPtr            atomic.Pointer[unitTables]
}

type moveStat struct {
	milliUnitsPerTick uint32
}

type staminaStat struct {
	maxCenti          uint16
	regenPerTickCenti uint16

	canBlock               bool
	blockDrainPerTickCenti uint16

	attackStaminaCostCenti uint16

	sprintSpeedMultiplier   float64
	sprintDrainPerTickCenti uint16
}

type unitTables struct {
	staminaStats        map[uint8]staminaStat
	moveStats           map[uint8]moveStat
	attackDurationTicks map[uint8]uint32
	comboSteps          map[uint8]uint8
	comboWindowTicks    map[uint8]uint32
}

func buildUnitTables(tickRate int, unitsPerMeter float64) *unitTables {
	defs := units.All()

	staminaStats := make(map[uint8]staminaStat, len(defs))
	for _, def := range defs {
		stat := staminaStat{
			maxCenti:          uint16(math.Round(def.Stamina * 100)),
			regenPerTickCenti: uint16(math.Round(def.StaminaRegenPerSecond * 100 / float64(tickRate))),
		}
		if def.Block != nil {
			stat.canBlock = true
			stat.blockDrainPerTickCenti = uint16(math.Round(def.Block.DrainPerSecond * 100 / float64(tickRate)))
		}
		if def.AttackStaminaCost != nil {
			stat.attackStaminaCostCenti = uint16(math.Round(*def.AttackStaminaCost * 100))
		}
		stat.sprintSpeedMultiplier = def.SprintSpeedMultiplier
		stat.sprintDrainPerTickCenti = uint16(math.Round(def.SprintStaminaCostPerSecond * 100 / float64(tickRate)))
		staminaStats[def.TypeID] = stat
	}

	moveStats := make(map[uint8]moveStat, len(defs))
	for _, def := range defs {
		milli := def.MoveSpeed * unitsPerMeter * 1000 / float64(tickRate)
		moveStats[def.TypeID] = moveStat{
			milliUnitsPerTick: uint32(math.Round(milli)),
		}
	}

	attackDurationTicks := make(map[uint8]uint32, len(defs))
	for _, def := range defs {
		seconds := def.WindupSeconds + def.ActiveSeconds + def.RecoverySeconds
		ticks := uint32(math.Round(seconds * float64(tickRate)))
		if ticks < 1 {
			ticks = 1
		}
		attackDurationTicks[def.TypeID] = ticks
	}

	comboSteps := make(map[uint8]uint8, len(defs))
	comboWindowTicks := make(map[uint8]uint32, len(defs))
	for _, def := range defs {
		steps := def.ComboSteps
		if steps < 1 {
			steps = 1
		}
		comboSteps[def.TypeID] = uint8(steps)
		comboWindowTicks[def.TypeID] = uint32(math.Round(def.ComboWindowSeconds * float64(tickRate)))
	}

	return &unitTables{
		staminaStats:        staminaStats,
		moveStats:           moveStats,
		attackDurationTicks: attackDurationTicks,
		comboSteps:          comboSteps,
		comboWindowTicks:    comboWindowTicks,
	}
}

func (gw *GameWorld) unitTables() *unitTables {
	return gw.unitTablesPtr.Load()
}

func (gw *GameWorld) RecomputeUnitTables() {
	gw.stepMu.Lock()
	defer gw.stepMu.Unlock()
	gw.unitTablesPtr.Store(buildUnitTables(gw.cfg.Game.TickRate, gw.cfg.Game.UnitsPerMeter))
}

func NewGameWorld(cfg *config.Config, live *config.LiveNet) *GameWorld {
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
		cfg:          cfg,
		live:         live,
		playersMap:   make(map[uint32]*types.Player, 256),
		stopChan:     make(chan struct{}),
		loopDone:     make(chan struct{}),
		nextPlayerID: 1000,

		lastDeltaCompositeLog: clock.Now(),
		prevStates:            make(map[uint32]types.PlayerState, initialCap),
		prevMoveRemainder:     make(map[uint32]uint32, initialCap),
		scratchStates:         make([]types.PlayerState, 0, initialCap),
		scratchChanged:        make([]types.PlayerState, 0, changedCap),
		scratchRemainders:     make([]uint32, 0, initialCap),
		scratchSeenIDs:        make(map[uint32]struct{}, initialCap),
		scratchPtrs:           make([]*types.Player, 0, initialCap),
	}
	gw.RecomputeUnitTables()

	n := runtime.GOMAXPROCS(0)
	gw.nTickWorkers = n
	gw.tickWorkerChs = make([]chan tickWorkerInput, n)
	for i := range gw.tickWorkerChs {
		ch := make(chan tickWorkerInput, 1)
		gw.tickWorkerChs[i] = ch
		gw.workersDone.Add(1)
		go func() { defer gw.workersDone.Done(); gw.runTickWorker(ch) }()
	}

	gw.visibilityManager = systems.NewVisibilityManager(
		cfg.World.Width, cfg.World.Height, 100)

	nominalInterval := time.Second / time.Duration(cfg.Game.TickRate)
	gw.nominalTickIntervalNs = nominalInterval.Nanoseconds()
	gw.currentTickIntervalNs = nominalInterval.Nanoseconds()

	go gw.gameLoop()

	slog.Info("gameworld initialized",
		"tick_rate_hz", cfg.Game.TickRate,
		"tick_interval_ms", nominalInterval.Milliseconds())

	return gw
}

func (gw *GameWorld) AddPlayer(requestedUnitType string) *types.Player {
	gw.stepMu.Lock()
	defer gw.stepMu.Unlock()
	playerID := atomic.AddUint32(&gw.nextPlayerID, 1)

	live := gw.live.Load()
	spawnRangeX := live.SpawnMaxX - live.SpawnMinX
	spawnRangeY := live.SpawnMaxY - live.SpawnMinY

	spawnX := live.SpawnMinX + uint16(rand.Intn(int(spawnRangeX)))
	spawnY := live.SpawnMinY + uint16(rand.Intn(int(spawnRangeY)))

	player := &types.Player{
		ID:       playerID,
		JoinTime: time.Now(),
	}

	unitDef := units.Get(requestedUnitType)

	player.SetX(spawnX)
	player.SetY(spawnY)
	player.SetDirection(0)
	player.SetState(types.StateIdle)
	player.SetUnitType(unitDef.TypeID)

	player.SetHP(uint16(math.Round(unitDef.HP)))
	player.SetStaminaCenti(uint16(math.Round(unitDef.Stamina * 100)))
	player.SetLastUpdate(clock.Now())

	gw.playersMu.Lock()
	gw.playersMap[playerID] = player
	gw.membershipChanged = true
	gw.playersMu.Unlock()
	gw.visibilityManager.AddPlayer(playerID, spawnX, spawnY)

	return player
}

func (gw *GameWorld) RemovePlayer(playerID uint32) {
	gw.stepMu.Lock()
	defer gw.stepMu.Unlock()
	gw.playersMu.Lock()
	_, loaded := gw.playersMap[playerID]
	if loaded {
		delete(gw.playersMap, playerID)
		gw.membershipChanged = true
	}
	gw.playersMu.Unlock()
	if loaded {
		gw.visibilityManager.RemovePlayer(playerID)
		metrics.EventsProcessed.WithLabelValues("disconnect").Inc()
	}
}

func (gw *GameWorld) QueueAction(playerID uint32, action types.PlayerAction) bool {
	gw.playersMu.RLock()
	player := gw.playersMap[playerID]
	gw.playersMu.RUnlock()
	return player != nil && player.OfferAction(action)
}

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

func (gw *GameWorld) GetTickCount() uint32 {
	return atomic.LoadUint32(&gw.tickCount)
}

func (gw *GameWorld) GetAllPlayers() []types.PlayerState {
	gw.playersMu.RLock()
	allPlayers := make([]types.PlayerState, 0, len(gw.playersMap))
	for _, player := range gw.playersMap {
		allPlayers = append(allPlayers, player.ToState())
	}
	gw.playersMu.RUnlock()
	return allPlayers
}

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

func (gw *GameWorld) GetPlayerCount() int {
	gw.playersMu.RLock()
	count := len(gw.playersMap)
	gw.playersMu.RUnlock()
	return count
}

func (gw *GameWorld) gameLoop() {
	defer close(gw.loopDone)

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

func (gw *GameWorld) SetTickBroadcaster(fn func(all []types.PlayerState, changed []types.PlayerState, fullSync bool, worldTick uint32, computeDur time.Duration) bool) {
	gw.broadcastFn.Store(broadcastFuncHolder{fn: fn})
}

func (gw *GameWorld) SetTickInterval(d time.Duration) {
	t := gw.ticker.Load()
	if d <= 0 || t == nil {
		return
	}
	atomic.StoreInt64(&gw.currentTickIntervalNs, d.Nanoseconds())
	t.Reset(d)
}

func (gw *GameWorld) GetTickInterval() time.Duration {
	return time.Duration(atomic.LoadInt64(&gw.currentTickIntervalNs))
}

func (gw *GameWorld) GetNominalTickInterval() time.Duration {
	return time.Duration(atomic.LoadInt64(&gw.nominalTickIntervalNs))
}

func (gw *GameWorld) GetTickDuration() time.Duration {
	return time.Duration(atomic.LoadInt64(&gw.tickDuration))
}

func (gw *GameWorld) TryAttack(playerID uint32) (x, y uint16, accepted bool) {
	gw.stepMu.Lock()
	defer gw.stepMu.Unlock()
	return gw.tryAttack(playerID)
}

func (gw *GameWorld) tryAttack(playerID uint32) (x, y uint16, accepted bool) {
	gw.playersMu.RLock()
	player, ok := gw.playersMap[playerID]
	gw.playersMu.RUnlock()
	if !ok {
		return 0, 0, false
	}

	if player.GetState() == types.StateBlocking {
		return 0, 0, false
	}

	currentTick := gw.GetTickCount()
	start := player.GetAttackStartTick()

	if start > 0 && currentTick-start < gw.unitTables().attackDurationTicks[player.GetUnitType()] {
		player.SetPendingComboInput(true)
		return 0, 0, false
	}

	return gw.executeAttack(player, currentTick)
}

func (gw *GameWorld) executeAttack(player *types.Player, currentTick uint32) (x, y uint16, accepted bool) {
	tables := gw.unitTables()

	step := uint8(1)
	if currentTick <= player.GetComboExpireTick() {
		step = player.GetComboStep() + 1
		if step > tables.comboSteps[player.GetUnitType()] {
			step = 1
		}
	}

	if stat, ok := tables.staminaStats[player.GetUnitType()]; ok && stat.attackStaminaCostCenti > 0 {
		if !player.TrySpendStaminaCenti(stat.attackStaminaCostCenti) {
			player.SetPendingComboInput(false)
			return 0, 0, false
		}
	}

	player.SetComboStep(step)
	player.SetComboExpireTick(currentTick + tables.attackDurationTicks[player.GetUnitType()] + tables.comboWindowTicks[player.GetUnitType()])
	player.SetState(types.StateAttacking)
	player.SetAttackStartTick(currentTick)
	player.SetPendingComboInput(false)
	metrics.EventsProcessed.WithLabelValues("attack").Inc()

	return player.GetX(), player.GetY(), true
}

func (gw *GameWorld) TryBlockStart(playerID uint32) bool {
	gw.stepMu.Lock()
	defer gw.stepMu.Unlock()
	return gw.tryBlockStart(playerID)
}

func (gw *GameWorld) tryBlockStart(playerID uint32) bool {
	gw.playersMu.RLock()
	player, ok := gw.playersMap[playerID]
	gw.playersMu.RUnlock()
	if !ok {
		return false
	}

	stat, ok := gw.unitTables().staminaStats[player.GetUnitType()]
	if !ok || !stat.canBlock {
		return false
	}
	if player.GetState() == types.StateAttacking {
		return false
	}
	if player.GetStaminaCenti() == 0 {
		return false
	}

	player.SetVX(0)
	player.SetVY(0)
	player.SetState(types.StateBlocking)
	return true
}

func (gw *GameWorld) EndBlock(playerID uint32) {
	gw.stepMu.Lock()
	defer gw.stepMu.Unlock()
	gw.endBlock(playerID)
}

func (gw *GameWorld) endBlock(playerID uint32) {
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

	stat, ok := gw.unitTables().staminaStats[player.GetUnitType()]
	if !ok {
		return false
	}
	player.SpendStaminaUpTo(stat.blockDrainPerTickCenti)
	return true
}

func (gw *GameWorld) tick() {
	gw.stepMu.Lock()
	defer gw.stepMu.Unlock()

	tickStart := time.Now()

	gw.scratchStates = gw.scratchStates[:0]
	gw.scratchChanged = gw.scratchChanged[:0]
	gw.scratchRemainders = gw.scratchRemainders[:0]
	clear(gw.scratchSeenIDs)

	nowNano := clock.Now()

	worldTick := atomic.AddUint32(&gw.tickCount, 1)

	lastSync := atomic.LoadInt64(&gw.lastSyncTime)
	fullSync := gw.membershipChanged || lastSync == 0 || time.Duration(nowNano-lastSync) >= gw.cfg.Game.SyncInterval
	if fullSync {
		gw.membershipChanged = false
		atomic.StoreInt64(&gw.lastSyncTime, nowNano)
	}

	t0 := time.Now()

	gw.scratchPtrs = gw.scratchPtrs[:0]
	gw.playersMu.RLock()
	for _, p := range gw.playersMap {
		gw.scratchPtrs = append(gw.scratchPtrs, p)
	}
	gw.playersMu.RUnlock()

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
	t1 := time.Now()
	metrics.TickPhaseDuration.WithLabelValues("range").Observe(t1.Sub(t0).Seconds())
	metrics.TickPhaseDuration.WithLabelValues("world_step").Observe(t1.Sub(t0).Seconds())
	metrics.TickWorldStepDuration.Observe(t1.Sub(t0).Seconds())

	gw.deltaVectorChanges = 0
	gw.deltaPositionOnly = 0
	gw.deltaClamped = 0
	gw.deltaKeyframes = 0

	elapsedTicks := int32(worldTick - gw.prevBaselineTick)
	if elapsedTicks < 0 {
		elapsedTicks = 0
	}
	tickLive := gw.live.Load()
	velocityReplication := tickLive.VelocityReplication
	keyframeMod := uint32(0)
	if tickLive.KeyframeDivisor > 0 {
		keyframeMod = uint32(tickLive.KeyframeDivisor)
	}
	for _, player := range gw.scratchPtrs {
		st := player.ToState()
		gw.scratchStates = append(gw.scratchStates, st)
		gw.scratchRemainders = append(gw.scratchRemainders, player.GetMoveRemainderMilli())
		gw.scratchSeenIDs[st.ID] = struct{}{}

		if !fullSync {
			prev, exists := gw.prevStates[st.ID]
			prevRemainder := gw.prevMoveRemainder[st.ID]
			reason := gw.classifyDelta(st, prev, exists, elapsedTicks, player.GetUnitType(), prevRemainder, velocityReplication)

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
	t2 := time.Now()
	metrics.TickPhaseDuration.WithLabelValues("delta").Observe(t2.Sub(t1).Seconds())

	if len(gw.scratchStates) == 0 {
		return
	}

	changedCount := len(gw.scratchChanged)
	if fullSync {
		changedCount = len(gw.scratchStates)
	}
	metrics.DeltaPlayersCount.Observe(float64(changedCount))
	metrics.DeltaRatio.Set(float64(changedCount) / float64(len(gw.scratchStates)))

	if holder, ok := gw.broadcastFn.Load().(broadcastFuncHolder); ok {
		computeDur := time.Since(tickStart)
		broadcasted := false
		if fullSync {
			broadcasted = holder.fn(gw.scratchStates, nil, true, worldTick, computeDur)
		} else {
			broadcasted = holder.fn(gw.scratchStates, gw.scratchChanged, false, worldTick, computeDur)
		}
		if broadcasted {
			gw.reportDeltaComposition()
			gw.prevBaselineTick = worldTick
			if mod := tickLive.KeyframeDivisor; mod > 0 {
				gw.keyframeCursor = (gw.keyframeCursor + 1) % uint32(mod)
			}

			for id := range gw.prevStates {
				if _, seen := gw.scratchSeenIDs[id]; !seen {
					delete(gw.prevStates, id)
					delete(gw.prevMoveRemainder, id)
				}
			}
			for i, st := range gw.scratchStates {
				gw.prevStates[st.ID] = st
				gw.prevMoveRemainder[st.ID] = gw.scratchRemainders[i]
			}
		}
	}

}

type deltaReason struct {
	include bool

	unpredictable bool

	diverged bool

	positionOnly bool
}

// classifyDelta predicts where st should be, starting from the last
// broadcast state prev, using the exact same fixed-point speed integration
// as updatePlayerPosition (per-unit rate, diagonal 1/sqrt2 factor, sprint
// multiplier and the carried MoveRemainderMilli) for this specific player.
// A mismatch means the client's own dead-reckoning would have diverged too,
// so the new state must be sent rather than left to be predicted.
func (gw *GameWorld) classifyDelta(st, prev types.PlayerState, exists bool, elapsedTicks int32, unitType uint8, prevRemainderMilli uint32, velocityReplication bool) deltaReason {
	if !exists {
		return deltaReason{include: true, unpredictable: true}
	}

	unpredictable := st.VX != prev.VX || st.VY != prev.VY ||
		st.State != prev.State || st.Direction != prev.Direction || st.Sprinting != prev.Sprinting ||
		st.ComboStep != prev.ComboStep

	predictedX, predictedY := int32(prev.X), int32(prev.Y)
	if elapsedTicks > 0 && (prev.VX != 0 || prev.VY != 0) {
		tables := gw.unitTables()
		stat := tables.staminaStats[unitType]
		milliRate := tables.moveStats[unitType].milliUnitsPerTick

		rateMultiplier := 1.0
		if prev.Sprinting {
			rateMultiplier *= stat.sprintSpeedMultiplier
		}
		if prev.VX != 0 && prev.VY != 0 {
			rateMultiplier *= 1 / math.Sqrt2
		}
		if rateMultiplier != 1.0 {
			milliRate = uint32(math.Round(float64(milliRate) * rateMultiplier))
		}

		totalMilli := uint64(prevRemainderMilli) + uint64(milliRate)*uint64(elapsedTicks)
		distance := int64(totalMilli / 1000)
		px, py := gw.integrateMovement(prev.X, prev.Y, prev.VX, prev.VY, distance)
		predictedX, predictedY = int32(px), int32(py)
	}
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
		tables := gw.unitTables()
		stat := tables.staminaStats[player.GetUnitType()]

		sprinting = player.GetSprint() && player.GetStaminaCenti() > 0
		if sprinting {
			player.SpendStaminaUpTo(stat.sprintDrainPerTickCenti)
			sprintDrained = true
		}

		milliRate := tables.moveStats[player.GetUnitType()].milliUnitsPerTick
		rateMultiplier := 1.0
		if sprinting {
			rateMultiplier *= stat.sprintSpeedMultiplier
		}

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

func (gw *GameWorld) GetMetrics() types.PerformanceMetrics {
	return types.PerformanceMetrics{
		ConnectedPlayers: uint32(gw.GetPlayerCount()),
		TickDuration:     time.Duration(atomic.LoadInt64(&gw.tickDuration)),
	}
}

func (gw *GameWorld) Stop() {
	gw.stopOnce.Do(func() {
		close(gw.stopChan)
		<-gw.loopDone
		for _, ch := range gw.tickWorkerChs {
			close(ch)
		}
		gw.workersDone.Wait()
	})
}

func (gw *GameWorld) runTickWorker(ch chan tickWorkerInput) {
	for input := range ch {
		tables := gw.unitTables()
		for _, player := range input.ptrs {

			if player.GetState() == types.StateAttacking {
				start := player.GetAttackStartTick()
				if start > 0 && input.worldTick-start >= tables.attackDurationTicks[player.GetUnitType()] {
					player.SetState(types.StateIdle)
					player.SetAttackStartTick(0)

					if player.GetPendingComboInput() {
						gw.executeAttack(player, input.worldTick)
					}
				}
			}

			var actions [types.MaxPendingActions]types.PlayerAction
			for _, action := range player.ConsumeActions(actions[:0]) {
				switch action.Type {
				case types.ActionAttack:
					gw.tryAttack(player.ID)
				case types.ActionBlockStart:
					gw.tryBlockStart(player.ID)
				case types.ActionBlockEnd:
					gw.endBlock(player.ID)
				case types.ActionFace:
					player.SetDirection(action.Direction)
				}
			}
			sprintDrained := gw.updatePlayerPosition(player, input.nowNano)
			blockDrained := gw.updateBlockDrain(player)

			if !sprintDrained && !blockDrained {
				gw.regenStamina(player)
			}
		}
		gw.tickWorkerWg.Done()
	}
}

func (gw *GameWorld) regenStamina(player *types.Player) {
	stat, ok := gw.unitTables().staminaStats[player.GetUnitType()]
	if !ok {
		return
	}
	player.RegenStamina(stat.regenPerTickCenti, stat.maxCenti)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
