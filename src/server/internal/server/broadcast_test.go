package server

import (
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gobwas/ws"
)

func TestWriteJobFrameEncodesMovementAck(t *testing.T) {
	job := writeJob{ack: true, ackID: 99, ackX: 123, ackY: 456, ackSeq: 789}
	var buf [15]byte
	got := writeJobFrame(&job, &buf)

	if len(got) != 15 || got[0] != 0x82 || got[1] != 13 || got[2] != 8 {
		t.Fatalf("unexpected ACK frame prefix: %v", got[:3])
	}
	if binary.LittleEndian.Uint32(got[3:7]) != 99 ||
		binary.LittleEndian.Uint16(got[7:9]) != 123 ||
		binary.LittleEndian.Uint16(got[9:11]) != 456 ||
		binary.LittleEndian.Uint32(got[11:15]) != 789 {
		t.Fatalf("unexpected ACK frame payload: %v", got[2:])
	}
}

func TestSelectRecipientsHonorsHardLimitWithoutMutatingInput(t *testing.T) {
	s := &Server{
		activeStalenessNs: 100,
		idleStalenessNs:   100,
		activeWindowNs:    100,
	}
	conns := []*Connection{
		{lastWorldStateSentNs: 100},
		{lastWorldStateSentNs: 200},
		{lastWorldStateSentNs: 300},
	}
	original := append([]*Connection(nil), conns...)

	selected, overdue, pooled := s.selectRecipients(conns, 1000, 2)
	defer releaseRecipientSlice(selected, pooled)

	if len(selected) != 2 || overdue != 2 {
		t.Fatalf("selected=%d overdue=%d, want 2/2", len(selected), overdue)
	}
	for i := range conns {
		if conns[i] != original[i] {
			t.Fatalf("input slice mutated at index %d", i)
		}
	}
	seen := map[*Connection]bool{}
	for _, conn := range selected {
		if seen[conn] {
			t.Fatal("recipient selected more than once")
		}
		seen[conn] = true
	}
}

func TestSelectRecipientsFastPathDoesNotBorrowSlice(t *testing.T) {
	s := &Server{activeStalenessNs: 100, idleStalenessNs: 100, activeWindowNs: 100}
	conns := []*Connection{{}, {}}
	selected, _, pooled := s.selectRecipients(conns, 1000, 0)
	if pooled != nil || len(selected) != len(conns) || &selected[0] != &conns[0] {
		t.Fatal("all-recipient fast path should return the original slice")
	}
}

func TestUpdateAtomicMax(t *testing.T) {
	var value int64
	updateAtomicMax(&value, 20)
	updateAtomicMax(&value, 10)
	updateAtomicMax(&value, 30)
	if got := atomic.LoadInt64(&value); got != 30 {
		t.Fatalf("max = %d, want 30", got)
	}
}

func TestValidClientHeader(t *testing.T) {
	valid := ws.Header{Masked: true, Fin: true, OpCode: ws.OpBinary, Length: 6}
	if !validClientHeader(valid) {
		t.Fatal("valid binary client frame rejected")
	}
	for _, invalid := range []struct {
		name string
		h    ws.Header
	}{
		{"unmasked", ws.Header{Fin: true, OpCode: ws.OpBinary, Length: 6}},
		{"fragmented", ws.Header{Masked: true, OpCode: ws.OpBinary, Length: 6}},
		{"text", ws.Header{Masked: true, Fin: true, OpCode: ws.OpText, Length: 1}},
		{"oversized", ws.Header{Masked: true, Fin: true, OpCode: ws.OpBinary, Length: maxClientFramePayload + 1}},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			if validClientHeader(invalid.h) {
				t.Fatal("invalid header accepted")
			}
		})
	}
}

func TestReleaseConnSliceClearsPointers(t *testing.T) {
	backing := make([]*Connection, 0, 4)
	buf := &backing
	conns := append(backing, &Connection{}, &Connection{})

	releaseConnSlice(conns, buf)

	if len(*buf) != 0 {
		t.Fatalf("released slice len = %d, want 0", len(*buf))
	}
	// A pooled slice that still holds pointers keeps closed connections alive.
	full := (*buf)[:2]
	for i, conn := range full {
		if conn != nil {
			t.Fatalf("element %d still references %p", i, conn)
		}
	}
}

func TestReplicationIntervalNsQuantisesToTicks(t *testing.T) {
	const tick = int64(50 * time.Millisecond)

	cases := []struct {
		name          string
		batchNs, want int64
	}{
		{"exact multiple", 100 * int64(time.Millisecond), 100 * int64(time.Millisecond)},
		{"rounds down", 120 * int64(time.Millisecond), 100 * int64(time.Millisecond)},
		{"rounds up", 130 * int64(time.Millisecond), 150 * int64(time.Millisecond)},
		{"never below one tick", 5 * int64(time.Millisecond), tick},
		{"disabled stays disabled", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := replicationIntervalNs(tc.batchNs, tick); got != tc.want {
				t.Fatalf("replicationIntervalNs(%d) = %d, want %d", tc.batchNs, got, tc.want)
			}
		})
	}

	if got := replicationIntervalNs(100, 0); got != 100 {
		t.Fatalf("unknown tick rate must pass the interval through, got %d", got)
	}
}

// The regression this guards: a 100 ms interval evaluated on a jittery 50 ms tick grid
// used to alternate between 2 and 3 ticks, halving into an effective ~8 Hz and adding
// enough arrival jitter to pin the client's interpolation delay at its ceiling.
func TestReplicationDueHoldsCadenceUnderTickerJitter(t *testing.T) {
	const tick = int64(50 * time.Millisecond)
	interval := replicationIntervalNs(100*int64(time.Millisecond), tick)

	// Ticks drift slightly late, which is what pushed the naive comparison under
	// the threshold. Deterministic pattern, no randomness.
	jitter := []int64{0, -300 * 1000, 900 * 1000, -1200 * 1000, 400 * 1000}

	last := int64(0)
	emitted := []int{}
	for i := 1; i <= 40; i++ {
		now := int64(i)*tick + jitter[i%len(jitter)]
		if replicationDue(now, last, interval, tick) {
			emitted = append(emitted, i)
			last = now
		}
	}

	if len(emitted) == 0 {
		t.Fatal("no frames emitted")
	}
	for i := 1; i < len(emitted); i++ {
		if gap := emitted[i] - emitted[i-1]; gap != 2 {
			t.Fatalf("emitted at ticks %v: gap %d between #%d and #%d, want a steady 2",
				emitted, gap, emitted[i-1], emitted[i])
		}
	}
	if len(emitted) != 20 {
		t.Fatalf("emitted %d frames over 40 ticks, want 20", len(emitted))
	}
}

func TestReplicationDueAlwaysAllowsWhenDisabled(t *testing.T) {
	if !replicationDue(0, 0, 0, int64(50*time.Millisecond)) {
		t.Fatal("a zero interval must not gate emission (full sync path)")
	}
}

// Quantising replication to whole ticks can silently disable the write-pressure
// backoff: if the controller's ceiling rounds back to the base interval, raising the
// adaptive value changes nothing. The ceiling must buy at least one more tick.
func TestAdaptiveBatchCeilingSurvivesQuantisation(t *testing.T) {
	for _, tc := range []struct{ tickMs, baseMs int64 }{
		{50, 100}, // shipped defaults: 20 Hz tick, 100 ms replication
		{50, 50},
		{33, 100},
		{25, 100},
	} {
		tick := tc.tickMs * int64(time.Millisecond)
		base := time.Duration(tc.baseMs) * time.Millisecond

		ceiling := adaptiveBatchCeiling(base, tick)
		baseTicks := replicationIntervalNs(base.Nanoseconds(), tick)
		ceilingTicks := replicationIntervalNs(ceiling.Nanoseconds(), tick)

		if ceilingTicks <= baseTicks {
			t.Fatalf("tick=%dms base=%dms: ceiling quantises to %dns, base to %dns — backoff is a no-op",
				tc.tickMs, tc.baseMs, ceilingTicks, baseTicks)
		}
	}

	if got := adaptiveBatchCeiling(100*time.Millisecond, 0); got != maxAdaptiveBatchInterval {
		t.Fatalf("unknown tick rate must fall back to %v, got %v", maxAdaptiveBatchInterval, got)
	}
}

func TestShouldEmitFrame(t *testing.T) {
	cases := []struct {
		name     string
		fullSync bool
		changed  int
		velocity bool
		want     bool
	}{
		{"full sync always ships", true, 0, false, true},
		{"legacy suppresses an empty delta", false, 0, false, false},
		{"legacy ships a non-empty delta", false, 3, false, true},
		// The heartbeat: without it the client never learns the tick advanced and
		// every dead-reckoned player freezes until someone changes direction.
		{"velocity replication ships an empty delta as a heartbeat", false, 0, true, true},
		{"velocity replication ships records too", false, 3, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEmitFrame(tc.fullSync, tc.changed, tc.velocity); got != tc.want {
				t.Fatalf("shouldEmitFrame(%v,%d,%v) = %v, want %v",
					tc.fullSync, tc.changed, tc.velocity, got, tc.want)
			}
		})
	}
}
