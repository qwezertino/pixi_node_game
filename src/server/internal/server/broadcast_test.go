package server

import (
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gobwas/ws"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/game"
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

func TestWriteJobFrameEncodesPong(t *testing.T) {
	job := writeJob{pong: true, pongNonce: 0x12345678}
	var buf [15]byte
	got := writeJobFrame(&job, &buf)

	if len(got) != 7 || got[0] != 0x82 || got[1] != 5 || got[2] != 18 {
		t.Fatalf("unexpected PONG frame prefix: %v", got[:3])
	}
	if nonce := binary.LittleEndian.Uint32(got[3:]); nonce != 0x12345678 {
		t.Fatalf("PONG nonce = %#x", nonce)
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

// tuneTimeDilation reads/writes GameWorld's real ticker via SetTickInterval, so these
// tests exercise it through a live GameWorld rather than a bare Server struct.
func newDilationTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{
		Game: config.GameConfig{TickRate: 20},
		World: config.WorldConfig{
			Width: 1000, Height: 1000, MaxX: 1000, MaxY: 1000,
		},
		Net: config.NetworkConfig{MaxConnections: 64},
	}
	gw := game.NewGameWorld(cfg)
	t.Cleanup(gw.Stop)
	// gameLoop() creates the actual *time.Ticker in its own goroutine; SetTickInterval
	// silently no-ops until it exists. In production this can never race —
	// SetTickInterval is only ever called from inside tick(), which only runs after
	// the ticker already exists — but this test calls it immediately after
	// construction, so give the goroutine a moment to reach that line.
	time.Sleep(10 * time.Millisecond)
	return &Server{cfg: cfg, gameWorld: gw, dilationBps: dilationBpsFull}
}

func TestTuneTimeDilationStepsDownUnderPressure(t *testing.T) {
	s := newDilationTestServer(t)

	// A single severe tick must NOT move the needle — debounce requires
	// dilationDebounceSevereTicks consecutive ticks before a step-down lands.
	for i := 0; i < dilationDebounceSevereTicks-1; i++ {
		s.tuneTimeDilation(100*time.Millisecond, 0, 0)
	}
	if got := atomic.LoadInt64(&s.dilationBps); got != dilationBpsFull {
		t.Fatalf("dilationBps = %d, want unchanged %d before debounce threshold", got, dilationBpsFull)
	}

	s.tuneTimeDilation(100*time.Millisecond, 0, 0)

	if got := atomic.LoadInt64(&s.dilationBps); got != dilationBpsFull-1000 {
		t.Fatalf("dilationBps = %d, want %d (one severe step down)", got, dilationBpsFull-1000)
	}
	// The tick interval must have actually grown — this is what distinguishes time
	// dilation from the old batch-interval backoff, which never touched the tick rate.
	nominal := s.gameWorld.GetNominalTickInterval()
	if got := s.gameWorld.GetTickInterval(); got <= nominal {
		t.Fatalf("tick interval = %v, want > nominal %v under severe pressure", got, nominal)
	}
}

func TestTuneTimeDilationDebounceResetsOnClearTick(t *testing.T) {
	s := newDilationTestServer(t)

	// One severe tick short of the debounce threshold...
	for i := 0; i < dilationDebounceSevereTicks-1; i++ {
		s.tuneTimeDilation(100*time.Millisecond, 0, 0)
	}
	// ...then a clear tick must reset the streak, not merely pause it.
	s.tuneTimeDilation(0, 0, 0)
	if got := atomic.LoadInt64(&s.dilationSevereStreak); got != 0 {
		t.Fatalf("severe streak = %d, want reset to 0 after a clear tick", got)
	}

	for i := 0; i < dilationDebounceSevereTicks-1; i++ {
		s.tuneTimeDilation(100*time.Millisecond, 0, 0)
	}
	if got := atomic.LoadInt64(&s.dilationBps); got != dilationBpsFull {
		t.Fatalf("dilationBps = %d, want unchanged %d — streak must have restarted from zero", got, dilationBpsFull)
	}
}

func TestTuneTimeDilationRecoversSlowlyWhenClear(t *testing.T) {
	s := newDilationTestServer(t)
	atomic.StoreInt64(&s.dilationBps, dilationBpsFull-2000)
	s.gameWorld.SetTickInterval(s.gameWorld.GetNominalTickInterval() * 10000 / (dilationBpsFull - 2000))

	s.tuneTimeDilation(0, 0, 0)

	got := atomic.LoadInt64(&s.dilationBps)
	if got != dilationBpsFull-1800 {
		t.Fatalf("dilationBps = %d, want %d (a single +2%% recovery step)", got, dilationBpsFull-1800)
	}
}

func TestTuneTimeDilationFloorsAtFloor(t *testing.T) {
	s := newDilationTestServer(t)
	atomic.StoreInt64(&s.dilationBps, minDilationBps)

	for i := 0; i < dilationDebounceSevereTicks; i++ {
		s.tuneTimeDilation(200*time.Millisecond, 0, 0)
	}

	if got := atomic.LoadInt64(&s.dilationBps); got != minDilationBps {
		t.Fatalf("dilationBps = %d, want floor %d", got, minDilationBps)
	}
}

// No write/fanout pressure at all — only a tick that overran its own budget, EVE's
// classic trigger — must still step dilation down once the debounce threshold is met.
func TestTuneTimeDilationTriggersOnComputeOverrunAlone(t *testing.T) {
	s := newDilationTestServer(t)
	nominal := s.gameWorld.GetNominalTickInterval()

	for i := 0; i < dilationDebounceSevereTicks; i++ {
		s.tuneTimeDilation(0, 0, nominal+nominal/2+time.Millisecond)
	}

	if got := atomic.LoadInt64(&s.dilationBps); got != dilationBpsFull-1000 {
		t.Fatalf("dilationBps = %d, want %d (compute overrun alone must trigger a severe step)", got, dilationBpsFull-1000)
	}
}

// A moderate-severity pressure signal must debounce independently of severe: a burst
// of moderate ticks alone should not trip the (unrelated) severe streak counter.
func TestTuneTimeDilationModerateDebounceIsIndependentOfSevere(t *testing.T) {
	s := newDilationTestServer(t)

	for i := 0; i < dilationDebounceModerateTicks-1; i++ {
		s.tuneTimeDilation(40*time.Millisecond, 0, 0)
	}
	if got := atomic.LoadInt64(&s.dilationBps); got != dilationBpsFull {
		t.Fatalf("dilationBps = %d, want unchanged %d before moderate debounce threshold", got, dilationBpsFull)
	}

	s.tuneTimeDilation(40*time.Millisecond, 0, 0)

	if got := atomic.LoadInt64(&s.dilationBps); got != dilationBpsFull-500 {
		t.Fatalf("dilationBps = %d, want %d (one moderate step down)", got, dilationBpsFull-500)
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
