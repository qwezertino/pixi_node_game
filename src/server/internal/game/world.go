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

type GameWorld struct {
	cfg                   *config.Config
	playersMu             sync.RWMutex
	playersMap            map[uint32]*types.Player
	broadcastFn           atomic.Value
	visibilityManager     *systems.VisibilityManager
	prevStates            map[uint32]types.PlayerState
	tickCount             uint32
	scratchStates         []types.PlayerState
	scratchChanged        []types.PlayerState
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

	attackDurationTicks map[uint8]uint32

	comboSteps map[uint8]uint8

	comboWindowTicks         map[uint8]uint32
	nextPlayerID             uint32
	playerCountEstimate      uint32
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
	lastSlowTickLog          int64
	lastDeltaCompositeLog    int64
	staminaStats             map[uint8]staminaStat
	moveStats                map[uint8]moveStat
}

type moveStat struct {

	milliUnitsPerTick uint32

	avgUnitsPerTick int32
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

	moveStats := make(map[uint8]moveStat, len(units.All()))
	for _, def := range units.All() {
		milli := def.MoveSpeed * cfg.Game.UnitsPerMeter * 1000 / float64(cfg.Game.TickRate)
		moveStats[def.TypeID] = moveStat{
			milliUnitsPerTick: uint32(math.Round(milli)),
			avgUnitsPerTick:   int32(math.Round(milli / 1000)),
		}
	}

	attackDurationTicks := make(map[uint8]uint32, len(units.All()))
	for _, def := range units.All() {
		seconds := def.WindupSeconds + def.ActiveSeconds + def.RecoverySeconds
		ticks := uint32(math.Round(seconds * float64(cfg.Game.TickRate)))
		if ticks < 1 {
			ticks = 1
		}
		attackDurationTicks[def.TypeID] = ticks
	}

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
		nextPlayerID: 1000,
		lastFullSync: time.Now(),

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

	n := runtime.GOMAXPROCS(0)
	gw.nTickWorkers = n
	gw.tickWorkerChs = make([]chan tickWorkerInput, n)
	for i := range gw.tickWorkerChs {
		ch := make(chan tickWorkerInput, 1)
		gw.tickWorkerChs[i] = ch
		go gw.runTickWorker(ch)
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
	playerID := atomic.AddUint32(&gw.nextPlayerID, 1)

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
	player.SetDirection(0)
	player.SetState(types.StateIdle)
	player.SetUnitType(unitDef.TypeID)

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

func (gw *GameWorld) RemovePlayer(playerID uint32) {
	gw.playersMu.Lock()
	_, loaded := gw.playersMap[playerID]
	if loaded {
		delete(gw.playersMap, playerID)
	}
	gw.playersMu.Unlock()
	if loaded {
		gw.visibilityManager.RemovePlayer(playerID)
		atomic.AddUint32(&gw.playerCountEstimate, ^uint32(0))
		metrics.EventsProcessed.WithLabelValues("disconnect").Inc()
	}
}

func (gw *GameWorld) ProcessEvent(event types.GameEvent) {
	gw.handleEvent(event)
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

func (gw *GameWorld) SetTickBroadcaster(fn func(all []types.PlayerState, changed []types.PlayerState, fullSync bool, worldTick uint32) bool) {
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

	if start > 0 && currentTick-start < gw.attackDurationTicks[player.GetUnitType()] {
		player.SetPendingComboInput(true)
		return 0, 0, false
	}

	return gw.executeAttack(player, currentTick)
}

func (gw *GameWorld) executeAttack(player *types.Player, currentTick uint32) (x, y uint16, accepted bool) {

	step := uint8(1)
	if currentTick <= player.GetComboExpireTick() {
		step = player.GetComboStep() + 1
		if step > gw.comboSteps[player.GetUnitType()] {
			step = 1
		}
	}

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

	player.SetVX(0)
	player.SetVY(0)
	player.SetState(types.StateBlocking)
	return true
}

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

func (gw *GameWorld) tick() {

	gw.scratchStates = gw.scratchStates[:0]
	gw.scratchChanged = gw.scratchChanged[:0]
	clear(gw.scratchSeenIDs)

	nowNano := clock.Now()

	worldTick := atomic.AddUint32(&gw.tickCount, 1)

	lastSync := atomic.LoadInt64(&gw.lastSyncTime)
	fullSync := lastSync == 0 || time.Duration(nowNano-lastSync) >= gw.cfg.Game.SyncInterval
	if fullSync {
		atomic.StoreInt64(&gw.lastSyncTime, nowNano)
		gw.lastFullSync = time.Now()
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

		if !fullSync {
			prev, exists := gw.prevStates[st.ID]
			speed := gw.moveStats[player.GetUnitType()].avgUnitsPerTick
			reason := classifyDelta(st, prev, exists, elapsedTicks, speed, velocityReplication)

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

	changedCount := len(gw.scratchChanged)
	if fullSync {
		changedCount = len(gw.scratchStates)
	}
	metrics.DeltaPlayersCount.Observe(float64(changedCount))
	metrics.DeltaRatio.Set(float64(changedCount) / float64(len(gw.scratchStates)))

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

type deltaReason struct {
	include bool

	unpredictable bool

	diverged bool

	positionOnly bool
}

func classifyDelta(st, prev types.PlayerState, exists bool, elapsedTicks, speed int32, velocityReplication bool) deltaReason {
	if !exists {
		return deltaReason{include: true, unpredictable: true}
	}

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
		stat := gw.staminaStats[player.GetUnitType()]

		sprinting = player.GetSprint() && player.GetStaminaCenti() > 0
		if sprinting {
			player.SpendStaminaUpTo(stat.sprintDrainPerTickCenti)
			sprintDrained = true
		}

		milliRate := gw.moveStats[player.GetUnitType()].milliUnitsPerTick
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

func (gw *GameWorld) handleEvent(event types.GameEvent) {
	gw.playersMu.RLock()
	player, exists := gw.playersMap[event.PlayerID]
	gw.playersMu.RUnlock()
	if !exists {
		return
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

		if player.GetState() == types.StateAttacking {
			break
		}
		player.SetState(types.StateAttacking)
		player.SetAttackStartTick(gw.GetTickCount())
	}
}

func (gw *GameWorld) GetMetrics() types.PerformanceMetrics {
	return types.PerformanceMetrics{
		ConnectedPlayers: uint32(gw.GetPlayerCount()),
		TickDuration:     time.Duration(atomic.LoadInt64(&gw.tickDuration)),
	}
}

func (gw *GameWorld) Stop() {
	close(gw.stopChan)

	for _, ch := range gw.tickWorkerChs {
		close(ch)
	}
	slog.Info("gameworld stopped")
}

func (gw *GameWorld) runTickWorker(ch chan tickWorkerInput) {
	for input := range ch {
		for _, player := range input.ptrs {

			if player.GetState() == types.StateAttacking {
				start := player.GetAttackStartTick()
				if start > 0 && input.worldTick-start >= gw.attackDurationTicks[player.GetUnitType()] {
					player.SetState(types.StateIdle)
					player.SetAttackStartTick(0)

					if player.GetPendingComboInput() {
						gw.executeAttack(player, input.worldTick)
					}
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
	stat, ok := gw.staminaStats[player.GetUnitType()]
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
