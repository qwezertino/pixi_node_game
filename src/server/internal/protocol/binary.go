package protocol

import (
	"encoding/binary"
	"fmt"
	"slices"

	"pixi_game_server/internal/types"
)

const (
	MessageJoin           = 1
	MessageLeave          = 2
	MessageMove           = 3
	MessageDirection      = 4
	MessageAttack         = 5
	MessageAttackEnd      = 6
	MessageViewportUpdate = 13
	MessageBlockStart     = 20
	MessageBlockEnd       = 21

	MessageGameState      = 7
	MessageMovementAck    = 8
	MessagePlayerJoined   = 11
	MessagePlayerLeft     = 12
	MessageDeltaGameState = 14
	MessageWelcome        = 15
	MessageSyncRequest    = 16
	MessagePing           = 17
	MessagePong           = 18
	MessageUnitRoster     = 19
)

const (
	DirectionRight uint8 = 0
	DirectionLeft  uint8 = 1
	DirectionDown  uint8 = 2
	DirectionUp    uint8 = 3
)

const (
	flagsStateMask      uint8 = 0x03
	flagsSprinting      uint8 = 0x04
	flagsComboStepMask  uint8 = 0x18
	flagsComboStepShift       = 3

	maxWireComboStep uint8 = 4
)

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

const ProtocolVersion = 12

const worldStateHeaderSize = 15

const maxPlayerRecordSize = 12

func appendUvarint(dst []byte, v uint32) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

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

		dst = appendUvarint(dst, player.ID-prevID)
		prevID = player.ID

		dst = binary.LittleEndian.AppendUint16(dst, player.X)
		dst = binary.LittleEndian.AppendUint16(dst, player.Y)
		dst = append(dst, uint8(player.VX), uint8(player.VY))

		dst = append(dst, playerFlags(player.State, player.Sprinting, player.ComboStep, player.Direction))
	}

	return dst
}

type BinaryProtocol struct{}

type MovementVector struct {
	DX int8
	DY int8
}

type ClientMessage struct {
	Type           uint8
	MovementVector MovementVector
	Sprint         bool
	Direction      uint8
	InputSequence  uint32
	Nonce          uint32
}

const MoveSprintBit = 0x10

func PackMovement(dx, dy int8) uint8 {
	packed := uint8(0)
	packed |= uint8(dx+1) & 0x03
	packed |= (uint8(dy+1) & 0x03) << 2
	return packed
}

func UnpackMovement(packed uint8) MovementVector {
	dx := int8(packed&0x03) - 1
	dy := int8((packed>>2)&0x03) - 1
	return MovementVector{DX: dx, DY: dy}
}

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

func (bp *BinaryProtocol) EncodeGameState(players []types.PlayerState, stateSequence, worldTick uint32, dilationBps uint16) []byte {
	return bp.AppendGameState(nil, players, stateSequence, worldTick, dilationBps)
}

func (bp *BinaryProtocol) AppendGameState(dst []byte, players []types.PlayerState, stateSequence, worldTick uint32, dilationBps uint16) []byte {
	return appendWorldState(dst, MessageGameState, players, stateSequence, worldTick, dilationBps)
}

func (bp *BinaryProtocol) EncodeDeltaGameState(players []types.PlayerState, stateSequence, worldTick uint32, dilationBps uint16) []byte {
	return bp.AppendDeltaGameState(nil, players, stateSequence, worldTick, dilationBps)
}

func (bp *BinaryProtocol) AppendDeltaGameState(dst []byte, players []types.PlayerState, stateSequence, worldTick uint32, dilationBps uint16) []byte {
	return appendWorldState(dst, MessageDeltaGameState, players, stateSequence, worldTick, dilationBps)
}

func (bp *BinaryProtocol) EncodePlayerJoined(player types.PlayerState) []byte {
	buffer := make([]byte, 12)
	offset := 0

	buffer[offset] = MessagePlayerJoined
	offset++

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

func (bp *BinaryProtocol) EncodePlayerLeft(playerID uint32) []byte {
	buffer := make([]byte, 5)
	buffer[0] = MessagePlayerLeft
	binary.LittleEndian.PutUint32(buffer[1:], playerID)
	return buffer
}

func (bp *BinaryProtocol) EncodeMovementAck(playerID uint32, x, y uint16, inputSequence uint32) []byte {

	buffer := make([]byte, 13)
	offset := 0

	buffer[offset] = MessageMovementAck
	offset++

	binary.LittleEndian.PutUint32(buffer[offset:], playerID)
	offset += 4

	binary.LittleEndian.PutUint16(buffer[offset:], x)
	offset += 2

	binary.LittleEndian.PutUint16(buffer[offset:], y)
	offset += 2

	binary.LittleEndian.PutUint32(buffer[offset:], inputSequence)

	return buffer
}

func (bp *BinaryProtocol) EncodeWelcome(playerID uint32, tickRate uint16, unitType uint8) []byte {
	buffer := make([]byte, 9)
	buffer[0] = MessageWelcome
	buffer[1] = ProtocolVersion
	binary.LittleEndian.PutUint16(buffer[2:], tickRate)
	binary.LittleEndian.PutUint32(buffer[4:], playerID)
	buffer[8] = unitType
	return buffer
}

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
