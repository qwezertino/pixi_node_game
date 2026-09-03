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
}

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

// Player представляет игрока в системе
type Player struct {
	ID                   uint32 // Atomic access
	X                    uint32 // Atomic access (stores uint16 value)
	Y                    uint32 // Atomic access (stores uint16 value)
	VX                   uint32 // Atomic access (stores int8: -1, 0, 1)
	VY                   uint32 // Atomic access (stores int8: -1, 0, 1)
	FacingRight          uint32 // Atomic bool (0/1)
	State                uint32 // Atomic player state
	AppliedInputSequence uint32
	MovementAckX         uint32 // Position after AppliedInputSequence was simulated
	MovementAckY         uint32
	AttackStartTick      uint32 // Atomic worldTick attack started on (0 = not attacking); tick-based so time dilation slows attacks the same way it slows movement

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
	PlayerID    uint32
	Type        EventType
	VectorX     int8
	VectorY     int8
	FacingRight bool
	InputSequence uint32
	Timestamp   int64
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
	ID          uint32
	X           uint16
	Y           uint16
	VX          int8
	VY          int8
	FacingRight bool
	State       uint8
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

func (p *Player) GetFacingRight() bool {
	return atomic.LoadUint32(&p.FacingRight) == 1
}

func (p *Player) SetFacingRight(facing bool) {
	var val uint32
	if facing {
		val = 1
	}
	atomic.StoreUint32(&p.FacingRight, val)
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

// ToState преобразует Player в PlayerState для сериализации
func (p *Player) ToState() PlayerState {
	return PlayerState{
		ID:          p.ID,
		X:           p.GetX(),
		Y:           p.GetY(),
		VX:          p.GetVX(),
		VY:          p.GetVY(),
		FacingRight: p.GetFacingRight(),
		State:       p.GetState(),
	}
}
