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
	MessageGameState    = 7  // GAME_STATE
	MessageMovementAck  = 8  // MOVEMENT_ACK
	MessageCorrection   = 9  // CORRECTION
	MessageInitialState = 10 // INITIAL_STATE
	MessagePlayerJoined = 11 // PLAYER_JOINED
	MessagePlayerLeft   = 12 // PLAYER_LEFT
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
	ViewportWidth  uint16
	ViewportHeight uint16
	Position       struct { // Player position when sending movement
		X uint32
		Y uint32
	}
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
		if len(data) < 14 { // Updated from 6 to 14 (6 + 8 for position)
			return nil, fmt.Errorf("move message too short")
		}
		movement := UnpackMovement(data[1])
		msg.MovementVector = movement
		msg.InputSequence = binary.LittleEndian.Uint32(data[2:6])

		// Decode position (x, y as uint32)
		msg.Position.X = binary.LittleEndian.Uint32(data[6:10])
		msg.Position.Y = binary.LittleEndian.Uint32(data[10:14])

	case MessageDirection:
		if len(data) < 2 {
			return nil, fmt.Errorf("direction message too short")
		}
		msg.Direction = data[1] == 1

	case MessageAttack, MessageAttackEnd:
		// No additional data needed for these messages

	case MessageViewportUpdate:
		if len(data) < 5 {
			return nil, fmt.Errorf("viewport message too short")
		}
		msg.ViewportWidth = binary.LittleEndian.Uint16(data[1:3])
		msg.ViewportHeight = binary.LittleEndian.Uint16(data[3:5])

	default:
		return nil, fmt.Errorf("unknown message type: %d", msg.Type)
	}

	return msg, nil
}

// EncodeGameState кодирует состояние игры для отправки клиенту
func (bp *BinaryProtocol) EncodeGameState(players []types.PlayerState) []byte {
	// Header: message type (1) + player count (4) = 5 bytes
	headerSize := 5
	playerSize := 11 // ID(4) + X(2) + Y(2) + VX(1) + VY(1) + Flags(1) = 11 bytes
	totalSize := headerSize + len(players)*playerSize

	buffer := make([]byte, totalSize)
	offset := 0

	// Message type
	buffer[offset] = MessageGameState
	offset++

	// Player count
	binary.LittleEndian.PutUint32(buffer[offset:], uint32(len(players)))
	offset += 4

	// Players data
	for _, player := range players {
		// Player ID (4 bytes)
		binary.LittleEndian.PutUint32(buffer[offset:], player.ID)
		offset += 4

		// Position X (2 bytes)
		binary.LittleEndian.PutUint16(buffer[offset:], player.X)
		offset += 2

		// Position Y (2 bytes)
		binary.LittleEndian.PutUint16(buffer[offset:], player.Y)
		offset += 2

		// Vector X (1 byte, signed)
		buffer[offset] = uint8(player.VX)
		offset++

		// Vector Y (1 byte, signed)
		buffer[offset] = uint8(player.VY)
		offset++

		// Flags: FacingRight (1 bit) + State (7 bits)
		flags := uint8(player.State & 0x7F) // 7 bits for state
		if player.FacingRight {
			flags |= 0x80 // Set bit 7 for FacingRight
		}
		buffer[offset] = flags
		offset++
	}

	return buffer
}

// EncodeDeltaUpdate кодирует дельта-обновление для эффективности
func (bp *BinaryProtocol) EncodeDeltaUpdate(updates []types.PlayerState) []byte {
	// Simplified delta encoding - only positions that changed
	headerSize := 5
	deltaSize := 7 // ID(4) + X(2) + Y(2) = 7 bytes per update
	totalSize := headerSize + len(updates)*deltaSize

	buffer := make([]byte, totalSize)
	offset := 0

	// Message type for delta
	buffer[offset] = 0x20 // Delta update type
	offset++

	// Update count
	binary.LittleEndian.PutUint32(buffer[offset:], uint32(len(updates)))
	offset += 4

	// Updates data
	for _, update := range updates {
		binary.LittleEndian.PutUint32(buffer[offset:], update.ID)
		offset += 4
		binary.LittleEndian.PutUint16(buffer[offset:], update.X)
		offset += 2
		binary.LittleEndian.PutUint16(buffer[offset:], update.Y)
		offset += 2
	}

	return buffer
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

// EncodePlayerMovement кодирует сообщение о движении игрока
func (bp *BinaryProtocol) EncodePlayerMovement(playerID uint32, dx, dy int8) []byte {
	buffer := make([]byte, 7) // 1 + 4 + 1 + 1
	offset := 0

	// Message type (1 byte) - использует тип из TypeScript
	buffer[offset] = 255 // Custom type for player movement broadcast
	offset++

	// Player ID (4 bytes)
	binary.LittleEndian.PutUint32(buffer[offset:], playerID)
	offset += 4

	// Movement vector DX (1 byte)
	buffer[offset] = byte(dx)
	offset++

	// Movement vector DY (1 byte)
	buffer[offset] = byte(dy)
	offset++

	return buffer
}

// EncodePlayerDirection кодирует сообщение о повороте игрока
func (bp *BinaryProtocol) EncodePlayerDirection(playerID uint32, facingRight bool) []byte {
	buffer := make([]byte, 6) // 1 + 4 + 1
	offset := 0

	// Message type (1 byte)
	buffer[offset] = 254 // Custom type for player direction broadcast
	offset++

	// Player ID (4 bytes)
	binary.LittleEndian.PutUint32(buffer[offset:], playerID)
	offset += 4

	// Direction (1 byte) - 0 for left, 1 for right
	if facingRight {
		buffer[offset] = 1
	} else {
		buffer[offset] = 0
	}
	offset++

	return buffer
}

// EncodePlayerAttack кодирует сообщение об атаке игрока
func (bp *BinaryProtocol) EncodePlayerAttack(playerID uint32, x, y uint16) []byte {
	buffer := make([]byte, 9) // 1 + 4 + 2 + 2
	offset := 0

	// Message type (1 byte)
	buffer[offset] = 253 // Custom type for player attack broadcast
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

	return buffer
}
