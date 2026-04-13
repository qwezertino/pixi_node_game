package protocol

import (
	"encoding/binary"
	"fmt"

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
)

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
func (bp *BinaryProtocol) DecodeClientMessage(data []byte) (*ClientMessage, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("message too short")
	}

	msg := &ClientMessage{
		Type: data[0],
	}

	switch msg.Type {
	case MessageMove:
		if len(data) < 6 {
			return nil, fmt.Errorf("move message too short")
		}
		movement := UnpackMovement(data[1])
		msg.MovementVector = movement
		msg.InputSequence = binary.LittleEndian.Uint32(data[2:6])

	case MessageDirection:
		if len(data) < 2 {
			return nil, fmt.Errorf("direction message too short")
		}
		msg.Direction = data[1] == 1

	case MessageAttack, MessageAttackEnd:
		// No additional data needed for these messages

	case MessageViewportUpdate:
		// Accepted but not processed — viewport-based culling not yet implemented.

	default:
		return nil, fmt.Errorf("unknown message type: %d", msg.Type)
	}

	return msg, nil
}

// EncodeGameState кодирует состояние игры для отправки клиенту
func (bp *BinaryProtocol) EncodeGameState(players []types.PlayerState) []byte {
	return bp.AppendGameState(nil, players)
}

// AppendGameState encodes full game state and appends it to dst (preserves existing content).
// When dst has a header prefix (e.g. 10 reserved WS frame bytes), the payload is written
// after those bytes — dst[len(dst):len(dst)+payloadSize] — with no allocation if
// cap(dst) is sufficient (ring slot pre-allocated to 64 KB).
func (bp *BinaryProtocol) AppendGameState(dst []byte, players []types.PlayerState) []byte {
	// Header: message type (1) + player count (4) = 5 bytes
	playerSize := 11 // ID(4) + X(2) + Y(2) + VX(1) + VY(1) + Flags(1) = 11 bytes
	startOffset := len(dst)
	payloadSize := 5 + len(players)*playerSize
	totalSize := startOffset + payloadSize

	if cap(dst) < totalSize {
		newDst := make([]byte, totalSize, totalSize+payloadSize)
		copy(newDst, dst)
		dst = newDst
	} else {
		dst = dst[:totalSize]
	}

	offset := startOffset

	// Message type
	dst[offset] = MessageGameState
	offset++

	// Player count
	binary.LittleEndian.PutUint32(dst[offset:], uint32(len(players)))
	offset += 4

	// Players data
	for _, player := range players {
		binary.LittleEndian.PutUint32(dst[offset:], player.ID)
		offset += 4
		binary.LittleEndian.PutUint16(dst[offset:], player.X)
		offset += 2
		binary.LittleEndian.PutUint16(dst[offset:], player.Y)
		offset += 2
		dst[offset] = uint8(player.VX)
		offset++
		dst[offset] = uint8(player.VY)
		offset++
		flags := uint8(player.State & 0x7F)
		if player.FacingRight {
			flags |= 0x80
		}
		dst[offset] = flags
		offset++
	}

	return dst
}

// EncodeDeltaGameState кодирует дельту — только изменившихся игроков.
// Формат идентичен EncodeGameState (11 байт/игрок), но тип сообщения = MessageDeltaGameState.
// Клиент мёржит дельту в своё состояние вместо полной замены.
func (bp *BinaryProtocol) EncodeDeltaGameState(players []types.PlayerState) []byte {
	return bp.AppendDeltaGameState(nil, players)
}

// AppendDeltaGameState encodes a delta game state and appends it to dst (preserves existing content).
// Формат идентичен AppendGameState (11 байт/игрок), но тип сообщения = MessageDeltaGameState.
// Клиент мёржит дельту в своё состояние вместо полной замены.
func (bp *BinaryProtocol) AppendDeltaGameState(dst []byte, players []types.PlayerState) []byte {
	playerSize := 11
	startOffset := len(dst)
	payloadSize := 5 + len(players)*playerSize
	totalSize := startOffset + payloadSize

	if cap(dst) < totalSize {
		newDst := make([]byte, totalSize, totalSize+payloadSize)
		copy(newDst, dst)
		dst = newDst
	} else {
		dst = dst[:totalSize]
	}

	offset := startOffset

	dst[offset] = MessageDeltaGameState
	offset++

	binary.LittleEndian.PutUint32(dst[offset:], uint32(len(players)))
	offset += 4

	for _, player := range players {
		binary.LittleEndian.PutUint32(dst[offset:], player.ID)
		offset += 4
		binary.LittleEndian.PutUint16(dst[offset:], player.X)
		offset += 2
		binary.LittleEndian.PutUint16(dst[offset:], player.Y)
		offset += 2
		dst[offset] = uint8(player.VX)
		offset++
		dst[offset] = uint8(player.VY)
		offset++
		flags := uint8(player.State & 0x7F)
		if player.FacingRight {
			flags |= 0x80
		}
		dst[offset] = flags
		offset++
	}

	return dst
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
	// Header: message type (1) + player ID (4) + position (4) + input sequence (4) = 13 bytes
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
	offset += 4

	return buffer
}
