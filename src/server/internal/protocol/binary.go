package protocol

import (
	"encoding/binary"
	"fmt"
	"slices"

	"pixi_game_server/internal/types"
)

// Message types совместимые с artillery-processor.cjs
const (
	MessageJoin           = 1  // JOIN
	MessageLeave          = 2  // LEAVE
	MessageMove           = 3  // MOVE
	MessageDirection      = 4  // DIRECTION
	MessageAttack         = 5  // ATTACK
	MessageAttackEnd      = 6  // ATTACK_END
	MessageViewportUpdate = 13 // Custom viewport (separate from attack)
	MessageBlockStart     = 20 // BLOCK_START (RMB pressed)
	MessageBlockEnd       = 21 // BLOCK_END (RMB released)

	// Server -> Client messages
	MessageGameState      = 7  // GAME_STATE (full)
	MessageMovementAck    = 8  // MOVEMENT_ACK
	MessagePlayerJoined   = 11 // PLAYER_JOINED
	MessagePlayerLeft     = 12 // PLAYER_LEFT
	MessageDeltaGameState = 14 // DELTA_GAME_STATE (only changed players)
	MessageWelcome        = 15 // WELCOME (protocol version + authoritative self ID + tick rate)
	MessageSyncRequest    = 16 // SYNC_REQUEST (client detected a delta sequence gap)
	MessagePing           = 17 // PING (application RTT nonce)
	MessagePong           = 18 // PONG (echoes application RTT nonce)
	MessageUnitRoster     = 19 // UNIT_ROSTER (playerID -> unit type, sent on connect and once per full-sync)
)

// Direction wire codes for MessageDirection and the per-player flags byte (see
// appendWorldState) — must match DIRECTIONS order in the client's animationLayout.ts.
const (
	DirectionRight uint8 = 0
	DirectionLeft  uint8 = 1
	DirectionDown  uint8 = 2
	DirectionUp    uint8 = 3
)

// Per-player flags byte layout (see appendWorldState/EncodePlayerJoined):
// bits 0-1 core state (types.StateIdle/Attacking/Blocking), bit 2 sprinting
// (PlayerState.Sprinting — GDD §54 walk-vs-run), bits 3-4 combo step (0-3, i.e.
// step 1-4 — PlayerState.ComboStep, see version 12 below), bit 5 reserved, bits
// 6-7 direction code (DirectionRight etc.).
const (
	flagsStateMask      uint8 = 0x03
	flagsSprinting      uint8 = 0x04
	flagsComboStepMask  uint8 = 0x18
	flagsComboStepShift       = 3
	// maxWireComboStep is the largest step flagsComboStepMask can carry (2 bits).
	// A unit whose comboSteps ever exceeds this needs another protocol bump.
	maxWireComboStep uint8 = 4
)

// playerFlags packs one player's state/sprint/comboStep/direction into the wire
// flags byte. comboStep is 1-indexed (0 or 1 both mean "step 1" on the wire —
// 0 is never sent by the server, ComboStep starts at 1 on the first swing).
func playerFlags(state uint8, sprinting bool, comboStep uint8, direction uint8) uint8 {
	if comboStep == 0 {
		comboStep = 1
	}
	if comboStep > maxWireComboStep {
		comboStep = maxWireComboStep
	}
	flags := state&flagsStateMask | (direction&0x03)<<6
	flags |= (comboStep - 1) << flagsComboStepShift & flagsComboStepMask
	if sprinting {
		flags |= flagsSprinting
	}
	return flags
}

// ProtocolVersion 12 replicates which step of its combo chain an attack is
// (GDD §54-adjacent combo system) — repurposes 2 of the 3 previously-reserved
// bits in the per-player flags byte (bits 3-4, up to 4 steps without another
// version bump). The client uses this instead of randomly picking attack1/attack2
// on every swing (see AnimationController.startAttackAnimation).
// Version 11 replicates whether a player is actually sprinting this tick
// (GDD §54: walk and run are distinct animations, sprint should visibly swap one
// for the other for every client, not just predict it locally) — repurposes 1 of
// the 4 previously-unused bits in the per-player flags byte's state field: bits
// 0-1 are now the core state (0/1/2, same values as before), bit 2 is the new
// "sprinting" flag, bits 3-5 stay reserved. Direction (bits 6-7) is unchanged.
// Version 10 adds sprint and block (GDD §54/§57): MOVE's packed movement
// byte gains a third bit (0x10, alongside the existing 2-bit dx/dy fields) carrying
// the client's held-Shift intent for that sample, and two new zero-payload messages
// BLOCK_START/BLOCK_END (RMB press/release) drive a new player State value,
// StateBlocking (2) — State was already 6 bits wide (see version 8 below) with only
// 0/1 in use, so this needed no wire format change beyond the new value itself.
// Version 9 adds current HP/stamina to UNIT_ROSTER entries (see
// types.UnitAssignment) — same low-frequency channel as unit type, not the per-tick
// world state, since nothing drains either yet so they never change after spawn.
// Version 8 adds up/down facing: the flags byte in every world-state record
// now packs a 2-bit direction code (bits 6-7, see DirectionRight etc.) instead of a
// single facing-right bit, so State only gets 6 bits (0-63) instead of 7 — plenty,
// since it currently only uses 0/1. MessageDirection's payload changed the same way,
// from a signed -1/1 byte to an unsigned 0-3 code.
// Version 7 adds unit type: WELCOME carries the connecting client's assigned
// unit (units.Definition.TypeID), and a new UNIT_ROSTER message replicates other
// players' unit types. Unlike position/velocity, unit type never changes after spawn,
// so it rides its own low-frequency channel instead of every world-state record.
// Version 6 adds a time-dilation factor to every world-state frame (EVE-style
// TiDi: the server slows its own tick rate under pressure instead of silently
// throttling replication, and the client needs the current factor to scale its local
// prediction step so it doesn't run ahead of a dilated server).
// Version 5 uses fixed-rate input samples. MOVE carries the current vector
// and sequence; the server applies only the newest sample available for a tick and
// ACKs the position after that authoritative step.
// Version 3 carries the simulation tick in every world-state frame. A client
// dead-reckons players the frame omits, and needs the exact number of elapsed steps to
// do so — wall-clock arrival time is not precise enough and does not survive a frame
// the fanout shed. Version 2 introduced varint-delta player IDs: records are sorted by
// ID so each delta is positive and, for the dense IDs the server hands out, almost
// always fits in one byte instead of four.
const ProtocolVersion = 12

// worldStateHeaderSize — type(1) + stateSequence(4) + worldTick(4) + playerCount(4) +
// dilationBps(2).
const worldStateHeaderSize = 15

// maxPlayerRecordSize — varint ID (≤5) + X(2) + Y(2) + VX(1) + VY(1) + flags(1).
const maxPlayerRecordSize = 12

// appendUvarint writes v as LEB128: 7 payload bits per byte, high bit marks continuation.
func appendUvarint(dst []byte, v uint32) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// appendWorldState serialises a world-state frame. It sorts players in place: both
// callers pass a scratch slice owned by the current tick, and delta-encoding IDs
// requires ascending order.
func appendWorldState(dst []byte, messageType uint8, players []types.PlayerState, stateSequence, worldTick uint32, dilationBps uint16) []byte {
	slices.SortFunc(players, func(a, b types.PlayerState) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})

	startOffset := len(dst)
	worstCase := startOffset + worldStateHeaderSize + len(players)*maxPlayerRecordSize
	if cap(dst) < worstCase {
		newDst := make([]byte, startOffset, worstCase+worstCase-startOffset)
		copy(newDst, dst)
		dst = newDst
	}

	dst = append(dst, messageType)
	dst = binary.LittleEndian.AppendUint32(dst, stateSequence)
	dst = binary.LittleEndian.AppendUint32(dst, worldTick)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(players)))
	dst = binary.LittleEndian.AppendUint16(dst, dilationBps)

	prevID := uint32(0)
	for _, player := range players {
		// The first record carries the absolute ID (prevID starts at 0); the rest
		// carry the gap to the previous one, which is always ≥1 after sorting.
		dst = appendUvarint(dst, player.ID-prevID)
		prevID = player.ID

		dst = binary.LittleEndian.AppendUint16(dst, player.X)
		dst = binary.LittleEndian.AppendUint16(dst, player.Y)
		dst = append(dst, uint8(player.VX), uint8(player.VY))

		dst = append(dst, playerFlags(player.State, player.Sprinting, player.ComboStep, player.Direction))
	}

	return dst
}

// BinaryProtocol обрабатывает сериализацию/десериализацию сообщений
type BinaryProtocol struct{}

// MovementVector представляет движение игрока
type MovementVector struct {
	DX int8
	DY int8
}

// ClientMessage представляет сообщение от клиента
type ClientMessage struct {
	Type           uint8
	MovementVector MovementVector
	Sprint         bool  // MOVE only — held-Shift intent, see MoveSprintBit
	Direction      uint8 // 0-3, see DirectionRight etc.
	InputSequence  uint32
	Nonce          uint32
}

// MoveSprintBit is bit 4 of a MOVE message's packed movement byte (bits 0-3 are
// dx/dy, see PackMovement) — set when the client is holding Shift for that sample.
const MoveSprintBit = 0x10

// PackMovement упаковывает движение в один байт (совместимо с artillery-processor.cjs)
func PackMovement(dx, dy int8) uint8 {
	packed := uint8(0)
	packed |= uint8(dx+1) & 0x03        // dx: -1->0, 0->1, 1->2 (2 bits)
	packed |= (uint8(dy+1) & 0x03) << 2 // dy: same, shifted 2 bits
	return packed
}

// UnpackMovement распаковывает движение из байта
func UnpackMovement(packed uint8) MovementVector {
	dx := int8(packed&0x03) - 1      // Extract bits 0-1, convert back to -1,0,1
	dy := int8((packed>>2)&0x03) - 1 // Extract bits 2-3, convert back to -1,0,1
	return MovementVector{DX: dx, DY: dy}
}

// DecodeClientMessage декодирует сообщение от клиента
// DecodeClientMessage parses a raw WebSocket payload into a ClientMessage.
// Returns a value type (not a pointer) to avoid a heap allocation on the hot path.
func (bp *BinaryProtocol) DecodeClientMessage(data []byte) (ClientMessage, error) {
	if len(data) < 1 {
		return ClientMessage{}, fmt.Errorf("message too short")
	}

	msg := ClientMessage{
		Type: data[0],
	}

	switch msg.Type {
	case MessageMove:
		if len(data) != 6 {
			return ClientMessage{}, fmt.Errorf("move message has invalid length")
		}
		if data[1]&0xe0 != 0 || data[1]&0x03 == 0x03 || (data[1]>>2)&0x03 == 0x03 {
			return ClientMessage{}, fmt.Errorf("move message has invalid vector")
		}
		movement := UnpackMovement(data[1])
		msg.MovementVector = movement
		msg.Sprint = data[1]&MoveSprintBit != 0
		msg.InputSequence = binary.LittleEndian.Uint32(data[2:6])

	case MessageDirection:
		if len(data) != 2 || data[1] > DirectionUp {
			return ClientMessage{}, fmt.Errorf("direction message is invalid")
		}
		msg.Direction = data[1]

	case MessageAttack:
		if len(data) != 9 {
			return ClientMessage{}, fmt.Errorf("attack message has invalid length")
		}

	case MessageAttackEnd:
		if len(data) != 1 {
			return ClientMessage{}, fmt.Errorf("attack-end message has invalid length")
		}

	case MessageBlockStart:
		if len(data) != 1 {
			return ClientMessage{}, fmt.Errorf("block-start message has invalid length")
		}

	case MessageBlockEnd:
		if len(data) != 1 {
			return ClientMessage{}, fmt.Errorf("block-end message has invalid length")
		}

	case MessageSyncRequest:
		if len(data) != 1 {
			return ClientMessage{}, fmt.Errorf("sync request has invalid length")
		}

	case MessagePing:
		if len(data) != 5 {
			return ClientMessage{}, fmt.Errorf("ping message has invalid length")
		}
		msg.Nonce = binary.LittleEndian.Uint32(data[1:5])

	case MessageViewportUpdate:
		if len(data) != 5 {
			return ClientMessage{}, fmt.Errorf("viewport message has invalid length")
		}

	default:
		return ClientMessage{}, fmt.Errorf("unknown message type: %d", msg.Type)
	}

	return msg, nil
}

// EncodeGameState кодирует состояние игры для отправки клиенту
func (bp *BinaryProtocol) EncodeGameState(players []types.PlayerState, stateSequence, worldTick uint32, dilationBps uint16) []byte {
	return bp.AppendGameState(nil, players, stateSequence, worldTick, dilationBps)
}

// AppendGameState encodes full game state and appends it to dst (preserves existing
// content). When dst has a header prefix (e.g. 10 reserved WS frame bytes), the payload
// is written after those bytes. Reorders players by ID — see appendWorldState.
// dilationBps is the current time-dilation factor (10000 = 100%, nominal tick rate).
func (bp *BinaryProtocol) AppendGameState(dst []byte, players []types.PlayerState, stateSequence, worldTick uint32, dilationBps uint16) []byte {
	return appendWorldState(dst, MessageGameState, players, stateSequence, worldTick, dilationBps)
}

// EncodeDeltaGameState кодирует дельту — только изменившихся игроков.
func (bp *BinaryProtocol) EncodeDeltaGameState(players []types.PlayerState, stateSequence, worldTick uint32, dilationBps uint16) []byte {
	return bp.AppendDeltaGameState(nil, players, stateSequence, worldTick, dilationBps)
}

// AppendDeltaGameState encodes a delta game state and appends it to dst. Format is
// identical to AppendGameState, but the message type tells the client to merge the
// records into its existing state instead of replacing it.
func (bp *BinaryProtocol) AppendDeltaGameState(dst []byte, players []types.PlayerState, stateSequence, worldTick uint32, dilationBps uint16) []byte {
	return appendWorldState(dst, MessageDeltaGameState, players, stateSequence, worldTick, dilationBps)
}

// EncodePlayerJoined кодирует сообщение о присоединении игрока
func (bp *BinaryProtocol) EncodePlayerJoined(player types.PlayerState) []byte {
	buffer := make([]byte, 12) // 1 + 11 bytes
	offset := 0

	buffer[offset] = MessagePlayerJoined
	offset++

	// Same as in game state but for single player
	binary.LittleEndian.PutUint32(buffer[offset:], player.ID)
	offset += 4
	binary.LittleEndian.PutUint16(buffer[offset:], player.X)
	offset += 2
	binary.LittleEndian.PutUint16(buffer[offset:], player.Y)
	offset += 2
	buffer[offset] = uint8(player.VX)
	offset++
	buffer[offset] = uint8(player.VY)
	offset++

	buffer[offset] = playerFlags(player.State, player.Sprinting, player.ComboStep, player.Direction)

	return buffer
}

// EncodePlayerLeft кодирует сообщение об отключении игрока
func (bp *BinaryProtocol) EncodePlayerLeft(playerID uint32) []byte {
	buffer := make([]byte, 5) // 1 + 4 bytes
	buffer[0] = MessagePlayerLeft
	binary.LittleEndian.PutUint32(buffer[1:], playerID)
	return buffer
}

// EncodeMovementAck кодирует подтверждение движения для отправки клиенту
func (bp *BinaryProtocol) EncodeMovementAck(playerID uint32, x, y uint16, inputSequence uint32) []byte {
	// type(1) + player ID(4) + position(4) + input sequence(4) = 13 bytes
	buffer := make([]byte, 13)
	offset := 0

	// Message type
	buffer[offset] = MessageMovementAck
	offset++

	// Player ID (4 bytes)
	binary.LittleEndian.PutUint32(buffer[offset:], playerID)
	offset += 4

	// Position X (2 bytes)
	binary.LittleEndian.PutUint16(buffer[offset:], x)
	offset += 2

	// Position Y (2 bytes)
	binary.LittleEndian.PutUint16(buffer[offset:], y)
	offset += 2

	// Input sequence (4 bytes)
	binary.LittleEndian.PutUint32(buffer[offset:], inputSequence)

	return buffer
}

// EncodeWelcome identifies the connection explicitly. Inferring the local player from
// map/order state is racy when multiple clients connect at the same time.
// Format: type(1) + protocolVersion(1) + tickRateHz(2) + playerID(4) + unitType(1) = 9 bytes.
func (bp *BinaryProtocol) EncodeWelcome(playerID uint32, tickRate uint16, unitType uint8) []byte {
	buffer := make([]byte, 9)
	buffer[0] = MessageWelcome
	buffer[1] = ProtocolVersion
	binary.LittleEndian.PutUint16(buffer[2:], tickRate)
	binary.LittleEndian.PutUint32(buffer[4:], playerID)
	buffer[8] = unitType
	return buffer
}

// EncodeUnitRoster encodes a batch of unit assignments (type + current HP/stamina).
// Sent once to a newly connected client (covering every player already in the
// world) and rebroadcast to everyone once per full-sync cycle, so clients that
// joined since the last roster learn about them without a dedicated per-join
// fanout (see server.broadcastTick). See types.UnitAssignment for why HP/stamina
// are safe to replicate at this cadence today.
// Format: type(1) + count(varint) + count * [idDelta(varint) + unitType(1) + hp(u16) + staminaCenti(u16)].
// IDs are delta-encoded against the previous entry the same way world-state records
// are — entries are sorted ascending first. hp/staminaCenti are little-endian.
func (bp *BinaryProtocol) EncodeUnitRoster(entries []types.UnitAssignment) []byte {
	slices.SortFunc(entries, func(a, b types.UnitAssignment) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})

	dst := make([]byte, 0, 2+len(entries)*7)
	dst = append(dst, MessageUnitRoster)
	dst = appendUvarint(dst, uint32(len(entries)))

	prevID := uint32(0)
	for _, e := range entries {
		dst = appendUvarint(dst, e.ID-prevID)
		prevID = e.ID
		dst = append(dst, e.UnitType)
		dst = binary.LittleEndian.AppendUint16(dst, e.CurrentHP)
		dst = binary.LittleEndian.AppendUint16(dst, e.CurrentStamina)
	}
	return dst
}
