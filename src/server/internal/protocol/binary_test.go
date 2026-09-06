package protocol

import (
	"encoding/binary"
	"testing"

	"pixi_game_server/internal/types"
)

func TestEncodeWelcome(t *testing.T) {
	bp := &BinaryProtocol{}
	got := bp.EncodeWelcome(0x12345678, 20, 3)
	if len(got) != 9 {
		t.Fatalf("welcome length = %d, want 9", len(got))
	}
	if got[0] != MessageWelcome || got[1] != ProtocolVersion {
		t.Fatalf("unexpected welcome prefix: %v", got[:2])
	}
	if tickRate := binary.LittleEndian.Uint16(got[2:4]); tickRate != 20 {
		t.Fatalf("tick rate = %d, want 20", tickRate)
	}
	if id := binary.LittleEndian.Uint32(got[4:8]); id != 0x12345678 {
		t.Fatalf("player ID = %#x, want %#x", id, uint32(0x12345678))
	}
	if got[8] != 3 {
		t.Fatalf("unit type = %d, want 3", got[8])
	}
}

func TestEncodeUnitRoster(t *testing.T) {
	bp := &BinaryProtocol{}
	entries := []types.UnitAssignment{
		{ID: 9000, UnitType: 5, CurrentHP: 95, CurrentStamina: 10000},
		{ID: 1, UnitType: 1, CurrentHP: 90, CurrentStamina: 9950},
		{ID: 2, UnitType: 9, CurrentHP: 180, CurrentStamina: 13000},
	}
	got := bp.EncodeUnitRoster(entries)
	if got[0] != MessageUnitRoster {
		t.Fatalf("unexpected message type: %d", got[0])
	}

	offset := 1
	count, shift := uint32(0), uint(0)
	for {
		b := got[offset]
		offset++
		count |= uint32(b&0x7f) << shift
		if b&0x80 == 0 {
			break
		}
		shift += 7
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}

	want := map[uint32]types.UnitAssignment{
		1:    {UnitType: 1, CurrentHP: 90, CurrentStamina: 9950},
		2:    {UnitType: 9, CurrentHP: 180, CurrentStamina: 13000},
		9000: {UnitType: 5, CurrentHP: 95, CurrentStamina: 10000},
	}
	prevID := uint32(0)
	decoded := map[uint32]types.UnitAssignment{}
	for i := 0; i < 3; i++ {
		delta, shift := uint32(0), uint(0)
		for {
			b := got[offset]
			offset++
			delta |= uint32(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		id := prevID + delta
		prevID = id
		unitType := got[offset]
		offset++
		hp := binary.LittleEndian.Uint16(got[offset:])
		offset += 2
		stamina := binary.LittleEndian.Uint16(got[offset:])
		offset += 2
		decoded[id] = types.UnitAssignment{UnitType: unitType, CurrentHP: hp, CurrentStamina: stamina}
	}
	if offset != len(got) {
		t.Fatalf("roster has %d trailing bytes", len(got)-offset)
	}
	for id, want := range want {
		if decoded[id] != want {
			t.Fatalf("player %d = %+v, want %+v", id, decoded[id], want)
		}
	}
}

func TestDecodeMove(t *testing.T) {
	bp := &BinaryProtocol{}
	buf := make([]byte, 6)
	buf[0] = MessageMove
	buf[1] = PackMovement(-1, 1)
	binary.LittleEndian.PutUint32(buf[2:6], 77)

	got, err := bp.DecodeClientMessage(buf)
	if err != nil {
		t.Fatalf("valid MOVE rejected: %v", err)
	}
	if got.InputSequence != 77 ||
		got.MovementVector.DX != -1 || got.MovementVector.DY != 1 {
		t.Fatalf("decoded MOVE = %+v", got)
	}
	if _, err := bp.DecodeClientMessage(buf[:5]); err == nil {
		t.Fatal("truncated MOVE was accepted")
	}
}

func TestDecodePing(t *testing.T) {
	bp := &BinaryProtocol{}
	buf := []byte{MessagePing, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(buf[1:], 0xdeadbeef)
	got, err := bp.DecodeClientMessage(buf)
	if err != nil {
		t.Fatalf("valid PING rejected: %v", err)
	}
	if got.Type != MessagePing || got.Nonce != 0xdeadbeef {
		t.Fatalf("decoded PING = %+v", got)
	}
	if _, err := bp.DecodeClientMessage(buf[:4]); err == nil {
		t.Fatal("truncated PING was accepted")
	}
}

func TestEncodeMovementAck(t *testing.T) {
	bp := &BinaryProtocol{}
	got := bp.EncodeMovementAck(5, 100, 200, 9)
	if len(got) != 13 || got[0] != MessageMovementAck {
		t.Fatalf("ACK prefix/length = %v/%d", got[0], len(got))
	}
	if id := binary.LittleEndian.Uint32(got[1:5]); id != 5 {
		t.Fatalf("ACK player id = %d", id)
	}
	if x := binary.LittleEndian.Uint16(got[5:7]); x != 100 {
		t.Fatalf("ACK x = %d", x)
	}
	if y := binary.LittleEndian.Uint16(got[7:9]); y != 200 {
		t.Fatalf("ACK y = %d", y)
	}
	if seq := binary.LittleEndian.Uint32(got[9:13]); seq != 9 {
		t.Fatalf("ACK input sequence = %d", seq)
	}
}

// decodeWorldState mirrors the client decoder (binaryProtocol.ts) so that a change to
// the wire format has to be made on both sides for this test to stay green.
func decodeWorldState(t *testing.T, buf []byte) (uint8, uint32, uint32, uint16, []types.PlayerState) {
	t.Helper()
	if len(buf) < worldStateHeaderSize {
		t.Fatalf("frame shorter than header: %d", len(buf))
	}
	msgType := buf[0]
	sequence := binary.LittleEndian.Uint32(buf[1:5])
	worldTick := binary.LittleEndian.Uint32(buf[5:9])
	count := binary.LittleEndian.Uint32(buf[9:13])
	dilationBps := binary.LittleEndian.Uint16(buf[13:15])

	offset := worldStateHeaderSize
	prevID := uint32(0)
	players := make([]types.PlayerState, 0, count)
	for i := uint32(0); i < count; i++ {
		var delta uint32
		var shift uint
		for {
			if offset >= len(buf) {
				t.Fatal("truncated varint")
			}
			b := buf[offset]
			offset++
			delta |= uint32(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		// X(2) + Y(2) + VX(1) + VY(1) + flags(1)
		const recordTail = 7
		if offset+recordTail > len(buf) {
			t.Fatal("truncated player record")
		}
		id := prevID + delta
		prevID = id

		flags := buf[offset+6]
		players = append(players, types.PlayerState{
			ID:        id,
			X:         binary.LittleEndian.Uint16(buf[offset:]),
			Y:         binary.LittleEndian.Uint16(buf[offset+2:]),
			VX:        int8(buf[offset+4]),
			VY:        int8(buf[offset+5]),
			State:     flags & flagsStateMask,
			Sprinting: flags&flagsSprinting != 0,
			Direction: (flags >> 6) & 0x03,
		})
		offset += recordTail
	}
	if offset != len(buf) {
		t.Fatalf("frame has %d trailing bytes", len(buf)-offset)
	}
	return msgType, sequence, worldTick, dilationBps, players
}

func TestAppendWorldStateRoundTrip(t *testing.T) {
	bp := &BinaryProtocol{}
	// Deliberately unsorted, with a large gap that forces a multi-byte varint.
	players := []types.PlayerState{
		{ID: 9000, X: 5, Y: 6, VX: 1, VY: -1, State: 1},
		{ID: 1, X: 10, Y: 20, VX: -1, VY: 1, Direction: DirectionLeft, Sprinting: true},
		{ID: 2, X: 30, Y: 40},
		{ID: 65535, X: 6000, Y: 3000, Direction: DirectionUp, State: 2},
	}
	want := map[uint32]types.PlayerState{}
	for _, p := range players {
		want[p.ID] = p
	}

	got := bp.AppendGameState(nil, players, 42, 7777, 8500)
	msgType, sequence, worldTick, dilationBps, decoded := decodeWorldState(t, got)

	if msgType != MessageGameState || sequence != 42 || worldTick != 7777 || dilationBps != 8500 {
		t.Fatalf("header = type %d seq %d tick %d dilation %d", msgType, sequence, worldTick, dilationBps)
	}
	if len(decoded) != len(want) {
		t.Fatalf("decoded %d players, want %d", len(decoded), len(want))
	}
	for _, p := range decoded {
		expected, ok := want[p.ID]
		if !ok {
			t.Fatalf("decoded unknown player %d", p.ID)
		}
		if p != expected {
			t.Fatalf("player %d = %+v, want %+v", p.ID, p, expected)
		}
	}
}

func TestAppendWorldStatePreservesFramePrefix(t *testing.T) {
	bp := &BinaryProtocol{}
	prefix := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0} // reserved WS header bytes
	players := []types.PlayerState{{ID: 1, X: 1, Y: 2}, {ID: 2, X: 3, Y: 4}}

	got := bp.AppendDeltaGameState(append([]byte(nil), prefix...), players, 7, 99, 10000)
	if len(got) <= len(prefix) {
		t.Fatal("payload was not appended after the prefix")
	}
	msgType, sequence, worldTick, dilationBps, decoded := decodeWorldState(t, got[len(prefix):])
	if msgType != MessageDeltaGameState || sequence != 7 || worldTick != 99 || dilationBps != 10000 || len(decoded) != 2 {
		t.Fatalf("type=%d seq=%d tick=%d dilation=%d players=%d", msgType, sequence, worldTick, dilationBps, len(decoded))
	}

	// Dense consecutive IDs are the case varint encoding exists for: 8 bytes per
	// record (1-byte ID delta + 7) instead of the 11 a fixed uint32 ID would cost.
	const denseRecordSize = 8
	if size := len(got) - len(prefix); size != worldStateHeaderSize+2*denseRecordSize {
		t.Fatalf("dense-ID frame = %d bytes, want %d", size, worldStateHeaderSize+2*denseRecordSize)
	}
}

func TestAppendUvarint(t *testing.T) {
	cases := []struct {
		value uint32
		want  []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{300, []byte{0xac, 0x02}},
		{^uint32(0), []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
	}
	for _, tc := range cases {
		got := appendUvarint(nil, tc.value)
		if string(got) != string(tc.want) {
			t.Fatalf("appendUvarint(%d) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestDecodeSyncRequestRejectsTrailingData(t *testing.T) {
	bp := &BinaryProtocol{}
	if _, err := bp.DecodeClientMessage([]byte{MessageSyncRequest}); err != nil {
		t.Fatalf("valid sync request rejected: %v", err)
	}
	if _, err := bp.DecodeClientMessage([]byte{MessageSyncRequest, 0}); err == nil {
		t.Fatal("sync request with trailing data was accepted")
	}
}
