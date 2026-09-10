package server

import (
	"encoding/binary"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/game"
	"pixi_game_server/internal/metrics"
	"pixi_game_server/internal/types"
)

func histogramSampleCount(t *testing.T, h prometheus.Histogram) uint64 {
	t.Helper()
	var m dto.Metric
	if err := h.Write(&m); err != nil {
		t.Fatalf("write histogram: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

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

func testLiveNet(t *testing.T) *config.LiveNet {
	t.Helper()
	return config.NewLiveNet(&config.LiveNetConfig{
		WorldStateActiveStalenessNs: 100,
		WorldStateIdleStalenessNs:   100,
		WorldStateActiveWindowNs:    100,
		FanoutMinRecipientsPerTick:  1,
		FanoutTarget:                12 * time.Millisecond,
		SpawnMinX:                   0,
		SpawnMaxX:                   1,
		SpawnMinY:                   0,
		SpawnMaxY:                   1,
	})
}

func TestSelectRecipientsHonorsHardLimitWithoutMutatingInput(t *testing.T) {
	s := &Server{live: testLiveNet(t)}
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
	s := &Server{live: testLiveNet(t)}
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

	full := (*buf)[:2]
	for i, conn := range full {
		if conn != nil {
			t.Fatalf("element %d still references %p", i, conn)
		}
	}
}

func newDilationTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{
		Game: config.GameConfig{TickRate: 20},
		World: config.WorldConfig{
			Width: 1000, Height: 1000, MaxX: 1000, MaxY: 1000,
		},
		Net: config.NetworkConfig{MaxConnections: 64},
	}
	live := config.NewLiveNet(config.BuildLiveNetConfig(cfg))
	gw := game.NewGameWorld(cfg, live)
	t.Cleanup(gw.Stop)

	time.Sleep(10 * time.Millisecond)
	return &Server{cfg: cfg, live: live, gameWorld: gw, dilationBps: dilationBpsFull}
}

func TestTuneTimeDilationStepsDownUnderPressure(t *testing.T) {
	s := newDilationTestServer(t)

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

	nominal := s.gameWorld.GetNominalTickInterval()
	if got := s.gameWorld.GetTickInterval(); got <= nominal {
		t.Fatalf("tick interval = %v, want > nominal %v under severe pressure", got, nominal)
	}
}

func TestTuneTimeDilationDebounceResetsOnClearTick(t *testing.T) {
	s := newDilationTestServer(t)

	for i := 0; i < dilationDebounceSevereTicks-1; i++ {
		s.tuneTimeDilation(100*time.Millisecond, 0, 0)
	}

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

// TestBroadcastTickReservesStateBudgetFromAcks is the P1 regression from
// the 2026-09-10 review: movement ACKs were enqueued before the fanout
// byte budget was known, and usedBytes started already charged with their
// size, so a single ACK per tick could permanently starve a recipient
// whose needsFullState never gets cheap enough to fit in what budget was
// left. With a budget sized for exactly one full-recovery frame and one
// ACK enqueued every tick, world state must still get through at least
// once across ten ticks.
func TestBroadcastTickReservesStateBudgetFromAcks(t *testing.T) {
	s := reliabilityServer(t)
	s.gameWorld.Stop()
	player := s.gameWorld.AddPlayer("")
	raw, peer := net.Pipe()
	t.Cleanup(func() { raw.Close(); peer.Close() })
	c := s.createConnection(player, raw)
	s.connections[player.ID] = c
	s.admissions = 1

	st := player.ToState()
	frame := s.encodeTickFrame([]types.PlayerState{st}, nil, true, 1, 1)
	oneRecipientRecoveryBytes := len(frame.frame)
	frame.release()

	if err := s.live.Update(func(cfg *config.LiveNetConfig) error {
		cfg.FanoutMaxBroadcastBytesPerTick = oneRecipientRecoveryBytes
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	sawWorldState := false
	for tick := uint32(1); tick <= 10; tick++ {
		c.needsFullState.Store(true)
		player.SetMovementAck(tick, uint16(tick), 0)

		s.broadcastTick([]types.PlayerState{st}, nil, false, tick, time.Millisecond)
		got, _ := drainState(t, c)
		if got != nil {
			sawWorldState = true
		}
	}

	if !sawWorldState {
		t.Fatal("world state starved for 10 consecutive ticks by ACK byte-budget usage")
	}
}

// TestEnqueueBroadcastJobSheddingTripsDropStreak is the P2 regression from
// the 2026-09-10 review: the queue-depth shed and pending-broadcast shed
// branches incremented fanoutDrops but never checked FanoutDropStreak, and
// the one branch that did check used `==` — so a streak that jumped past
// the threshold via shedding (now that shedding also increments the
// counter) could sail past it forever. A long stretch of pure shedding
// must still trigger the same disconnect cleanup a writeCh-full failure
// does.
func TestEnqueueBroadcastJobSheddingTripsDropStreak(t *testing.T) {
	s := reliabilityServer(t)
	s.gameWorld.Stop()
	player := s.gameWorld.AddPlayer("")
	raw, peer := net.Pipe()
	t.Cleanup(func() { raw.Close(); peer.Close() })
	c := s.createConnection(player, raw)
	s.connections[player.ID] = c

	if err := s.live.Update(func(cfg *config.LiveNetConfig) error {
		cfg.FanoutQueueShedDepth = 1
		cfg.FanoutDropStreak = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	c.writeCh <- writeJob{direct: []byte{0}}

	for i := 0; i < 2; i++ {
		frame := &tickFrame{frame: []byte{0x82, 0}, refs: 1}
		if s.enqueueBroadcastJob(c, frame, 0) {
			t.Fatalf("enqueue %d: expected shed while queue depth >= FanoutQueueShedDepth", i)
		}
	}

	select {
	case <-c.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("drop streak reached via shedding alone did not trigger cleanup")
	}
}

// TestBroadcastTickTunesDilationEvenWithoutFanout is the P2 regression from
// the 2026-09-10 review: tuneTimeDilation used to run only on the path that
// actually sent a world-state frame, so an empty world (or, more broadly,
// any tick that suppresses fanout) could never recover from a stale
// slowdown. broadcastTick must now tune the regulator exactly once per
// call regardless of whether it reports a fanout.
func TestBroadcastTickTunesDilationEvenWithoutFanout(t *testing.T) {
	s := newDilationTestServer(t)
	atomic.StoreInt64(&s.dilationBps, dilationBpsFull-2000)
	s.gameWorld.SetTickInterval(s.gameWorld.GetNominalTickInterval() * 10000 / (dilationBpsFull - 2000))

	broadcasted := s.broadcastTick(nil, nil, false, 1, 0)
	if broadcasted {
		t.Fatal("an empty-world tick must not report a fanout")
	}

	got := atomic.LoadInt64(&s.dilationBps)
	if got != dilationBpsFull-1800 {
		t.Fatalf("dilationBps = %d, want %d — the regulator must still run (and recover) on a tick with no players to fan out to", got, dilationBpsFull-1800)
	}
}

// TestBroadcastTickRecordsLazyRecoveryCost is the P2 regression from the
// 2026-09-10 review: BroadcastPayloadBytes/BroadcastRecords used to be
// observed once, right after encoding the base (possibly empty) delta
// frame, before any lazy per-recipient full-recovery frame existed. A tick
// with an empty `changed` slice that still forces a full recovery for a
// recipient (e.g. one that just requested resync) reported zero records
// even though a full roster of records was actually sent. The recovery
// frame's real record count/bytes must also be observed, and
// BroadcastRecoveryFrames must count the recovery sends.
func TestBroadcastTickRecordsLazyRecoveryCost(t *testing.T) {
	s := reliabilityServer(t)
	s.gameWorld.Stop()
	players := []*types.Player{s.gameWorld.AddPlayer(""), s.gameWorld.AddPlayer("")}
	for _, p := range players {
		raw, peer := net.Pipe()
		t.Cleanup(func() { raw.Close(); peer.Close() })
		c := s.createConnection(p, raw)
		s.connections[p.ID] = c
	}
	s.admissions = len(players)
	states := []types.PlayerState{players[0].ToState(), players[1].ToState()}

	s.broadcastTick(states, states, true, 1, time.Millisecond)
	for _, p := range players {
		drainState(t, s.connections[p.ID])
	}

	s.connections[players[0].ID].needsFullState.Store(true)

	recordsBefore := histogramSampleCount(t, metrics.BroadcastRecords)
	recoveryBefore := testutilCounterValue(t, metrics.BroadcastRecoveryFrames)

	if !s.broadcastTick(states, nil, false, 2, time.Millisecond) {
		t.Fatal("expected a fanout for the recipient forced into recovery")
	}

	recordsAfter := histogramSampleCount(t, metrics.BroadcastRecords)
	recoveryAfter := testutilCounterValue(t, metrics.BroadcastRecoveryFrames)

	if recordsAfter <= recordsBefore {
		t.Fatal("recovery frame's real record count was not observed")
	}
	if recoveryAfter <= recoveryBefore {
		t.Fatal("BroadcastRecoveryFrames did not count the lazy recovery send")
	}
}

func testutilCounterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("write counter: %v", err)
	}
	return m.GetCounter().GetValue()
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
