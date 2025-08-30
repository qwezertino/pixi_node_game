package types

import (
	"sync/atomic"
	"time"
)

// Player представляет игрока в системе
type Player struct {
	ID          uint32 // Atomic access
	X           uint32 // Atomic access (stores uint16 value)
	Y           uint32 // Atomic access (stores uint16 value)
	VX          int8   // Vector X: -1, 0, 1
	VY          int8   // Vector Y: -1, 0, 1
	FacingRight uint32 // Atomic bool (0/1)
	State       uint32 // Atomic player state
	ClientTick  uint32 // Atomic client tick for reconciliation

	// Viewport для оптимизации broadcasting
	ViewportWidth  uint16
	ViewportHeight uint16

	// Network connection (set separately to avoid atomic operations)
	ConnPtr uintptr // Pointer to websocket connection

	// Timestamps для performance tracking
	LastUpdate   int64 // Atomic timestamp
	LastActivity int64 // Atomic timestamp
	JoinTime     time.Time

	// Metrics
	MessageCount uint64 // Atomic counter
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
	EventDisconnect
	EventViewportUpdate
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

// ViewportBounds определяет границы viewport'а
type ViewportBounds struct {
	MinX uint16
	MinY uint16
	MaxX uint16
	MaxY uint16
}

// PerformanceMetrics содержит метрики производительности
type PerformanceMetrics struct {
	ConnectedPlayers  uint32
	TickDuration      time.Duration
	AverageTickTime   time.Duration
	MaxTickTime       time.Duration
	EventsPerSecond   uint64
	MessagesPerSecond uint64
	MemoryUsage       uint64
	GoroutineCount    int
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

func (p *Player) GetClientTick() uint32 {
	return atomic.LoadUint32(&p.ClientTick)
}

func (p *Player) SetClientTick(tick uint32) {
	atomic.StoreUint32(&p.ClientTick, tick)
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

// ToState преобразует Player в PlayerState для сериализации
func (p *Player) ToState() PlayerState {
	return PlayerState{
		ID:          p.ID,
		X:           p.GetX(),
		Y:           p.GetY(),
		VX:          p.VX,
		VY:          p.VY,
		FacingRight: p.GetFacingRight(),
		State:       p.GetState(),
		ClientTick:  p.GetClientTick(),
	}
}
