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
)

// ProtocolVersion 6 adds a time-dilation factor to every world-state frame (EVE-style
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
const ProtocolVersion = 6

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

		flags := uint8(player.State & 0x7F)
		if player.FacingRight {
			flags |= 0x80
		}
		dst = append(dst, flags)
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
	Direction      bool // FacingRight
	InputSequence  uint32
	Nonce          uint32
}

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
		if data[1]&0xf0 != 0 || data[1]&0x03 == 0x03 || (data[1]>>2)&0x03 == 0x03 {
			return ClientMessage{}, fmt.Errorf("move message has invalid vector")
		}
		movement := UnpackMovement(data[1])
		msg.MovementVector = movement
		msg.InputSequence = binary.LittleEndian.Uint32(data[2:6])

	case MessageDirection:
		if len(data) != 2 || (data[1] != 1 && data[1] != 0xff) {
			return ClientMessage{}, fmt.Errorf("direction message is invalid")
		}
		msg.Direction = data[1] == 1

	case MessageAttack:
		if len(data) != 9 {
			return ClientMessage{}, fmt.Errorf("attack message has invalid length")
		}

	case MessageAttackEnd:
		if len(data) != 1 {
			return ClientMessage{}, fmt.Errorf("attack-end message has invalid length")
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

	flags := uint8(player.State & 0x7F)
	if player.FacingRight {
		flags |= 0x80
	}
	buffer[offset] = flags

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
// Format: type(1) + protocolVersion(1) + tickRateHz(2) + playerID(4) = 8 bytes.
func (bp *BinaryProtocol) EncodeWelcome(playerID uint32, tickRate uint16) []byte {
	buffer := make([]byte, 8)
	buffer[0] = MessageWelcome
	buffer[1] = ProtocolVersion
	binary.LittleEndian.PutUint16(buffer[2:], tickRate)
	binary.LittleEndian.PutUint32(buffer[4:], playerID)
	return buffer
}
