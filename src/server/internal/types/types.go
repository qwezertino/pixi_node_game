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

	Sprint bool
}

const (
	StateIdle      uint8 = 0
	StateAttacking uint8 = 1
	StateBlocking  uint8 = 2
)

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

type UnitAssignment struct {
	ID       uint32
	UnitType uint8

	CurrentHP uint16

	CurrentStamina uint16
}

type Player struct {
	ID                   uint32
	X                    uint32
	Y                    uint32
	VX                   uint32
	VY                   uint32
	Direction            uint32
	State                uint32
	UnitType             uint32
	HP                   uint32
	StaminaCenti         uint32
	AppliedInputSequence uint32
	MovementAckX         uint32
	MovementAckY         uint32
	AttackStartTick      uint32
	Sprint               uint32
	SprintingNow         uint32

	MoveRemainderMilli uint32

	ComboStep         uint32
	ComboExpireTick   uint32
	PendingComboInput uint32

	LastUpdate   int64
	LastActivity int64
	JoinTime     time.Time

	MessageCount uint64

	inputMu              sync.Mutex
	pendingInput         MovementInput
	hasPendingInput      bool
	lastReceivedInput    uint32
	hasLastReceivedInput bool
}

type GameEvent struct {
	PlayerID      uint32
	Type          EventType
	VectorX       int8
	VectorY       int8
	Direction     uint8
	InputSequence uint32
	Timestamp     int64
}

type EventType uint8

const (
	EventMove EventType = iota
	EventAttack
	EventFace
)

type PlayerState struct {
	ID        uint32
	X         uint16
	Y         uint16
	VX        int8
	VY        int8
	Direction uint8
	State     uint8

	Sprinting bool

	ComboStep uint8
}

type PerformanceMetrics struct {
	ConnectedPlayers uint32
	TickDuration     time.Duration
}

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
