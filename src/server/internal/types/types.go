package types

import (
	"sync"
	"sync/atomic"
	"time"
)

const PlayerInputQueueCapacity = 256

type MovementInput struct {
	Sequence uint32
	DX       int8
	DY       int8
}

// InputResult explains why a movement step was not queued. The distinction matters:
// a stale sequence is routinely produced by retransmits and reordering middleboxes and
// must not cost the player their session, while a full ring means the client is more
// than PlayerInputQueueCapacity steps behind and can no longer be reconciled.
type InputResult uint8

const (
	InputAccepted InputResult = iota
	InputStale
	InputQueueFull
	InputInvalid
)

// Player представляет игрока в системе
type Player struct {
	ID                uint32 // Atomic access
	X                 uint32 // Atomic access (stores uint16 value)
	Y                 uint32 // Atomic access (stores uint16 value)
	VX                uint32 // Atomic access (stores int8: -1, 0, 1)
	VY                uint32 // Atomic access (stores int8: -1, 0, 1)
	FacingRight       uint32 // Atomic bool (0/1)
	State             uint32 // Atomic player state
	ClientTick        uint32 // Atomic client tick for reconciliation
	AppliedClientTick uint32 // Latest client tick captured and applied by world simulation
	AttackStartTime   int64  // Atomic nanosecond timestamp of attack start (0 = not attacking)

	// Timestamps для performance tracking
	LastUpdate   int64 // Atomic timestamp
	LastActivity int64 // Atomic timestamp
	JoinTime     time.Time

	// Metrics
	MessageCount uint64 // Atomic counter

	inputMu            sync.Mutex
	inputQueue         [PlayerInputQueueCapacity]MovementInput
	inputHead          uint16
	inputTail          uint16
	inputCount         uint16
	lastQueuedInput    uint32
	hasLastQueuedInput bool
}

// GameEvent представляет игровое событие
type GameEvent struct {
	PlayerID    uint32
	Type        EventType
	VectorX     int8
	VectorY     int8
	FacingRight bool
	ClientTick  uint32
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
	ClientTick  uint32
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

func (p *Player) GetClientTick() uint32 {
	return atomic.LoadUint32(&p.ClientTick)
}

func (p *Player) SetClientTick(tick uint32) {
	atomic.StoreUint32(&p.ClientTick, tick)
}

func (p *Player) GetAppliedClientTick() uint32 {
	return atomic.LoadUint32(&p.AppliedClientTick)
}

func (p *Player) SetAppliedClientTick(tick uint32) {
	atomic.StoreUint32(&p.AppliedClientTick, tick)
}

// EnqueueMovementInput preserves every predicted movement step in WebSocket order.
// The fixed-size, pointer-free ring avoids hot-path allocations and bounds memory.
func (p *Player) EnqueueMovementInput(input MovementInput) InputResult {
	p.inputMu.Lock()
	defer p.inputMu.Unlock()

	if p.hasLastQueuedInput {
		delta := input.Sequence - p.lastQueuedInput
		if delta == 0 || delta >= 1<<31 {
			return InputStale
		}
	}
	if p.inputCount == PlayerInputQueueCapacity {
		return InputQueueFull
	}

	p.inputQueue[p.inputTail] = input
	p.inputTail = (p.inputTail + 1) % PlayerInputQueueCapacity
	p.inputCount++
	p.lastQueuedInput = input.Sequence
	p.hasLastQueuedInput = true
	return InputAccepted
}

func (p *Player) DequeueMovementInput() (MovementInput, bool) {
	p.inputMu.Lock()
	defer p.inputMu.Unlock()

	if p.inputCount == 0 {
		return MovementInput{}, false
	}
	input := p.inputQueue[p.inputHead]
	p.inputQueue[p.inputHead] = MovementInput{}
	p.inputHead = (p.inputHead + 1) % PlayerInputQueueCapacity
	p.inputCount--
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

func (p *Player) GetAttackStartTime() int64 {
	return atomic.LoadInt64(&p.AttackStartTime)
}

func (p *Player) SetAttackStartTime(t int64) {
	atomic.StoreInt64(&p.AttackStartTime, t)
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
		ClientTick:  p.GetAppliedClientTick(),
	}
}
