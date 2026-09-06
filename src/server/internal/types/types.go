package types

import (
	"sync"
	"sync/atomic"
	"time"
)

type MovementInput struct {
	Sequence uint32
	DX       int8
	DY       int8
	// Sprint is the client's held-Shift intent for this sample. Whether it actually
	// speeds the player up depends on stamina at the moment the tick consumes it
	// (see GameWorld.updatePlayerPosition) — this only carries the request.
	Sprint bool
}

// Player.State values (see Player.State / ToState). Only 6 bits are wire-encoded
// (see protocol.ProtocolVersion doc), so values 3-63 remain free for future states.
const (
	StateIdle      uint8 = 0
	StateAttacking uint8 = 1
	StateBlocking  uint8 = 2
)

// InputResult explains why an input sample was not accepted.
type InputResult uint8

const (
	InputAccepted InputResult = iota
	InputStale
	InputInvalid
	InputGap
)

func (r InputResult) String() string {
	switch r {
	case InputAccepted:
		return "accepted"
	case InputStale:
		return "stale_sequence"
	case InputInvalid:
		return "invalid_input"
	case InputGap:
		return "sequence_gap"
	default:
		return "unknown"
	}
}

// UnitAssignment associates a player with their unit type and current HP/stamina —
// replicated separately from PlayerState (see units.Definition) on the low-frequency
// roster channel rather than every tick. This is a reasonable trade only because
// nothing drains HP or stamina yet: both sit at their unit's max from the moment of
// spawn (stamina's regen tick is a no-op once capped), so "stale until the next
// roster broadcast" costs nothing today. Once combat can actually change these,
// replicating them here will be too infrequent and they'll need to move to a
// per-tick delta path instead, the same way position/velocity work.
type UnitAssignment struct {
	ID       uint32
	UnitType uint8
	// CurrentHP: plain integer, no fractional part exists in unit data.
	CurrentHP uint16
	// CurrentStamina: fixed-point x100 ("centi-stamina") so fractional per-tick regen
	// (e.g. 8 stamina/sec at 20 ticks/sec = 0.4/tick) survives integer arithmetic.
	CurrentStamina uint16
}

// Player представляет игрока в системе
type Player struct {
	ID                   uint32 // Atomic access
	X                    uint32 // Atomic access (stores uint16 value)
	Y                    uint32 // Atomic access (stores uint16 value)
	VX                   uint32 // Atomic access (stores int8: -1, 0, 1)
	VY                   uint32 // Atomic access (stores int8: -1, 0, 1)
	Direction            uint32 // Atomic access (stores uint8 0-3: right/left/down/up, see protocol.DirectionRight etc.)
	State                uint32 // Atomic player state
	UnitType             uint32 // Atomic access (stores uint8: units.Definition.TypeID), set once at spawn
	HP                   uint32 // Atomic access — current HP, set to units.Definition.HP at spawn, never drained yet
	StaminaCenti         uint32 // Atomic access — current stamina x100 ("centi-stamina"), see UnitAssignment
	AppliedInputSequence uint32
	MovementAckX         uint32 // Position after AppliedInputSequence was simulated
	MovementAckY         uint32
	AttackStartTick      uint32 // Atomic worldTick attack started on (0 = not attacking); tick-based so time dilation slows attacks the same way it slows movement
	Sprint               uint32 // Atomic access (stores bool 0/1) — last requested sprint intent, see MovementInput.Sprint
	SprintingNow         uint32 // Atomic access (stores bool 0/1) — actually sprinting this tick, see PlayerState.Sprinting
	// MoveRemainderMilli is the fractional part (1/1000 world unit) of this player's
	// movement left over from the last tick — GDD §60 World Coordinate Resolution: a
	// unit's moveSpeed (m/s) times the world's units-per-meter resolution doesn't
	// always land on a whole world-unit-per-tick step, so the remainder accumulates
	// here and flushes a whole unit once it crosses 1000 (see game/world.go
	// updatePlayerPosition) — same fixed-point technique as centi-stamina.
	MoveRemainderMilli uint32

	// Combo (GDD §54-adjacent, no doc section of its own yet): ComboStep is the
	// step (1-indexed) executed by the most recent swing. ComboExpireTick is the
	// last worldTick a new swing may still continue the chain instead of
	// resetting to step 1 — set past the current swing's own duration so a press
	// during recovery, or shortly after it ends, both count. PendingComboInput
	// buffers a press that arrived mid-swing so it fires the next step the
	// instant the current swing's cooldown ends (see game/world.go executeAttack /
	// runTickWorker) instead of requiring a frame-perfect second click.
	ComboStep         uint32 // Atomic access (stores uint8)
	ComboExpireTick   uint32 // Atomic worldTick
	PendingComboInput uint32 // Atomic access (stores bool 0/1)

	// Timestamps для performance tracking
	LastUpdate   int64 // Atomic timestamp
	LastActivity int64 // Atomic timestamp
	JoinTime     time.Time

	// Metrics
	MessageCount uint64 // Atomic counter

	inputMu              sync.Mutex
	pendingInput         MovementInput
	hasPendingInput      bool
	lastReceivedInput    uint32
	hasLastReceivedInput bool
}

// GameEvent представляет игровое событие
type GameEvent struct {
	PlayerID      uint32
	Type          EventType
	VectorX       int8
	VectorY       int8
	Direction     uint8 // 0-3: right/left/down/up
	InputSequence uint32
	Timestamp     int64
}

// EventType определяет тип события
type EventType uint8

const (
	EventMove EventType = iota
	EventAttack
	EventFace
)

// PlayerState содержит состояние игрока для сериализации
type PlayerState struct {
	ID        uint32
	X         uint16
	Y         uint16
	VX        int8
	VY        int8
	Direction uint8 // 0-3: right/left/down/up
	State     uint8
	// Sprinting is whether THIS tick's step actually used sprint speed (moving,
	// Sprint requested, stamina available) — distinct from the player's held-Shift
	// intent (see Player.Sprint), which can be true while stationary or out of
	// stamina. Drives the client's walk-vs-run animation pick (GDD §54).
	Sprinting bool
	// ComboStep (1-indexed) is which swing of the combo chain this attack is —
	// read by the client only at the rising edge into StateAttacking (see
	// game/world.go executeAttack), so a stale value while not attacking is never
	// observed. Picks attack1/attack2/... instead of the old random cosmetic pick.
	ComboStep uint8
}

// PerformanceMetrics содержит метрики производительности
type PerformanceMetrics struct {
	ConnectedPlayers uint32
	TickDuration     time.Duration
}

// Atomic операции для Player
func (p *Player) GetX() uint16 {
	return uint16(atomic.LoadUint32(&p.X))
}

func (p *Player) SetX(x uint16) {
	atomic.StoreUint32(&p.X, uint32(x))
}

func (p *Player) GetY() uint16 {
	return uint16(atomic.LoadUint32(&p.Y))
}

func (p *Player) SetY(y uint16) {
	atomic.StoreUint32(&p.Y, uint32(y))
}

func (p *Player) GetDirection() uint8 {
	return uint8(atomic.LoadUint32(&p.Direction))
}

func (p *Player) SetDirection(direction uint8) {
	atomic.StoreUint32(&p.Direction, uint32(direction))
}

func (p *Player) GetUnitType() uint8 {
	return uint8(atomic.LoadUint32(&p.UnitType))
}

func (p *Player) SetUnitType(unitType uint8) {
	atomic.StoreUint32(&p.UnitType, uint32(unitType))
}

func (p *Player) GetHP() uint16 {
	return uint16(atomic.LoadUint32(&p.HP))
}

func (p *Player) SetHP(hp uint16) {
	atomic.StoreUint32(&p.HP, uint32(hp))
}

func (p *Player) GetStaminaCenti() uint16 {
	return uint16(atomic.LoadUint32(&p.StaminaCenti))
}

func (p *Player) SetStaminaCenti(centi uint16) {
	atomic.StoreUint32(&p.StaminaCenti, uint32(centi))
}

// RegenStamina adds perTickCenti to current stamina, clamped at maxCenti. A CAS loop
// rather than load-then-store: concurrent regen calls for the same player shouldn't
// happen (each player is only ever processed by one tick worker per tick), but this
// makes that guarantee unnecessary to maintain by construction.
func (p *Player) RegenStamina(perTickCenti, maxCenti uint16) {
	for {
		cur := atomic.LoadUint32(&p.StaminaCenti)
		if cur >= uint32(maxCenti) {
			return
		}
		next := min(cur+uint32(perTickCenti), uint32(maxCenti))
		if atomic.CompareAndSwapUint32(&p.StaminaCenti, cur, next) {
			return
		}
	}
}

// TrySpendStaminaCenti atomically deducts costCenti if (and only if) the player has
// at least that much — an all-or-nothing spend, used for one-shot costs like an
// attack swing where a partial charge makes no sense.
func (p *Player) TrySpendStaminaCenti(costCenti uint16) bool {
	if costCenti == 0 {
		return true
	}
	for {
		cur := atomic.LoadUint32(&p.StaminaCenti)
		if cur < uint32(costCenti) {
			return false
		}
		next := cur - uint32(costCenti)
		if atomic.CompareAndSwapUint32(&p.StaminaCenti, cur, next) {
			return true
		}
	}
}

// SpendStaminaUpTo atomically deducts up to costCenti, clamped at zero — used for
// continuous per-tick drains (block, sprint) where the caller has already decided
// the action is active and just needs the cost applied, not gated.
func (p *Player) SpendStaminaUpTo(costCenti uint16) {
	if costCenti == 0 {
		return
	}
	for {
		cur := atomic.LoadUint32(&p.StaminaCenti)
		if cur == 0 {
			return
		}
		next := uint32(0)
		if cur > uint32(costCenti) {
			next = cur - uint32(costCenti)
		}
		if atomic.CompareAndSwapUint32(&p.StaminaCenti, cur, next) {
			return
		}
	}
}

func (p *Player) GetState() uint8 {
	return uint8(atomic.LoadUint32(&p.State))
}

func (p *Player) SetState(state uint8) {
	atomic.StoreUint32(&p.State, uint32(state))
}

func (p *Player) GetVX() int8 {
	return int8(atomic.LoadUint32(&p.VX))
}

func (p *Player) SetVX(vx int8) {
	atomic.StoreUint32(&p.VX, uint32(vx))
}

func (p *Player) GetVY() int8 {
	return int8(atomic.LoadUint32(&p.VY))
}

func (p *Player) SetVY(vy int8) {
	atomic.StoreUint32(&p.VY, uint32(vy))
}

func (p *Player) GetSprint() bool {
	return atomic.LoadUint32(&p.Sprint) != 0
}

func (p *Player) SetSprint(sprint bool) {
	v := uint32(0)
	if sprint {
		v = 1
	}
	atomic.StoreUint32(&p.Sprint, v)
}

func (p *Player) GetMoveRemainderMilli() uint32 {
	return atomic.LoadUint32(&p.MoveRemainderMilli)
}

func (p *Player) SetMoveRemainderMilli(v uint32) {
	atomic.StoreUint32(&p.MoveRemainderMilli, v)
}

func (p *Player) GetSprintingNow() bool {
	return atomic.LoadUint32(&p.SprintingNow) != 0
}

func (p *Player) SetSprintingNow(sprinting bool) {
	v := uint32(0)
	if sprinting {
		v = 1
	}
	atomic.StoreUint32(&p.SprintingNow, v)
}

func (p *Player) GetAppliedInputSequence() uint32 {
	return atomic.LoadUint32(&p.AppliedInputSequence)
}

func (p *Player) SetMovementAck(sequence uint32, x, y uint16) {
	atomic.StoreUint32(&p.MovementAckX, uint32(x))
	atomic.StoreUint32(&p.MovementAckY, uint32(y))
	atomic.StoreUint32(&p.AppliedInputSequence, sequence)
}

func (p *Player) GetMovementAckPosition() (uint16, uint16) {
	return uint16(atomic.LoadUint32(&p.MovementAckX)), uint16(atomic.LoadUint32(&p.MovementAckY))
}

// OfferMovementInput validates WebSocket order and overwrites the pending sample.
// If several samples arrive before one server tick, only the newest can affect that
// tick; old samples are not a backlog of movement steps.
func (p *Player) OfferMovementInput(input MovementInput) InputResult {
	p.inputMu.Lock()
	defer p.inputMu.Unlock()

	if p.hasLastReceivedInput {
		delta := input.Sequence - p.lastReceivedInput
		if delta == 0 || delta >= 1<<31 {
			return InputStale
		}
		if delta != 1 {
			return InputGap
		}
	}
	p.pendingInput = input
	p.hasPendingInput = true
	p.lastReceivedInput = input.Sequence
	p.hasLastReceivedInput = true
	return InputAccepted
}

func (p *Player) ConsumeLatestMovementInput() (MovementInput, bool) {
	p.inputMu.Lock()
	defer p.inputMu.Unlock()

	if !p.hasPendingInput {
		return MovementInput{}, false
	}
	input := p.pendingInput
	p.pendingInput = MovementInput{}
	p.hasPendingInput = false
	return input, true
}

func (p *Player) GetLastUpdate() int64 {
	return atomic.LoadInt64(&p.LastUpdate)
}

func (p *Player) SetLastUpdate(timestamp int64) {
	atomic.StoreInt64(&p.LastUpdate, timestamp)
}

func (p *Player) IncrementMessageCount() uint64 {
	return atomic.AddUint64(&p.MessageCount, 1)
}

func (p *Player) GetMessageCount() uint64 {
	return atomic.LoadUint64(&p.MessageCount)
}

func (p *Player) GetAttackStartTick() uint32 {
	return atomic.LoadUint32(&p.AttackStartTick)
}

func (p *Player) SetAttackStartTick(tick uint32) {
	atomic.StoreUint32(&p.AttackStartTick, tick)
}

func (p *Player) GetComboStep() uint8 {
	return uint8(atomic.LoadUint32(&p.ComboStep))
}

func (p *Player) SetComboStep(step uint8) {
	atomic.StoreUint32(&p.ComboStep, uint32(step))
}

func (p *Player) GetComboExpireTick() uint32 {
	return atomic.LoadUint32(&p.ComboExpireTick)
}

func (p *Player) SetComboExpireTick(tick uint32) {
	atomic.StoreUint32(&p.ComboExpireTick, tick)
}

func (p *Player) GetPendingComboInput() bool {
	return atomic.LoadUint32(&p.PendingComboInput) != 0
}

func (p *Player) SetPendingComboInput(pending bool) {
	v := uint32(0)
	if pending {
		v = 1
	}
	atomic.StoreUint32(&p.PendingComboInput, v)
}

// ToState преобразует Player в PlayerState для сериализации
func (p *Player) ToState() PlayerState {
	return PlayerState{
		ID:        p.ID,
		X:         p.GetX(),
		Y:         p.GetY(),
		VX:        p.GetVX(),
		VY:        p.GetVY(),
		Direction: p.GetDirection(),
		State:     p.GetState(),
		Sprinting: p.GetSprintingNow(),
		ComboStep: p.GetComboStep(),
	}
}
