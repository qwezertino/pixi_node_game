package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"pixi_game_server/internal/config"
	"pixi_game_server/internal/liveconfig"
	"pixi_game_server/internal/protocol"
	"pixi_game_server/internal/types"
	"pixi_game_server/internal/units"
)

func reliabilityServer(t *testing.T) *Server {
	t.Helper()
	err := units.LoadDefinitions([]units.Definition{{ID: "spearman", TypeID: 1, HP: 100, Stamina: 100,
		MoveSpeed: 10, RangeType: "melee", ActiveSeconds: 0.1, RecoverySeconds: 0.2,
		SprintSpeedMultiplier: 1.5, AnimationSpeed: 0.1}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Build(&config.GameSettings{TickRate: 20, SyncIntervalSec: 30, UnitsPerMeter: 10,
		WorldWidth: 1000, WorldHeight: 1000, SpawnMinX: 100, SpawnMaxX: 200, SpawnMinY: 100, SpawnMaxY: 200, PlayerBaseScale: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Net.IPConnRate = 0
	cfg.Server.Port = 0
	cfg.Server.ManagementAddr = "127.0.0.1:0"
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.SetStaticBlobs([]byte("{}"), []byte("[]"))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}

type partialWriteConn struct {
	net.Conn
	calls  atomic.Int32
	closed atomic.Bool
	count  int
	err    error
}

func (c *partialWriteConn) SetWriteDeadline(time.Time) error { return nil }
func (c *partialWriteConn) Write(p []byte) (int, error) {
	c.calls.Add(1)
	return min(c.count, len(p)), c.err
}
func (c *partialWriteConn) Close() error { c.closed.Store(true); return nil }

func TestWriterStopsOnPartialWriteAndDrains(t *testing.T) {
	for _, count := range []int{0, 1, 2, 4} {
		for _, failure := range []error{io.ErrUnexpectedEOF, nil} {
			for _, batch := range []int{1, 8} {
				s := reliabilityServer(t)
				if failure == nil && batch > 1 {
					continue
				}
				if err := s.live.Update(func(c *config.LiveNetConfig) error { c.WriteBatchSize = batch; return nil }); err != nil {
					t.Fatal(err)
				}
				raw := &partialWriteConn{count: count, err: failure}
				c := s.createConnection(&types.Player{ID: 999}, raw)
				first := &tickFrame{frame: []byte{0x82, 3, 1, 2, 3}, refs: 1}
				second := &tickFrame{frame: []byte{0x82, 3, 4, 5, 6}, refs: 1}
				c.enqueue(writeJob{frame: first, timeout: time.Second})
				c.enqueue(writeJob{frame: second, timeout: time.Second})
				s.startWriteLoop(c)
				select {
				case <-c.ctx.Done():
				case <-time.After(time.Second):
					t.Fatal("writer did not close")
				}
				deadline := time.Now().Add(time.Second)
				for atomic.LoadInt32(&second.refs) != 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if raw.calls.Load() != 1 || !raw.closed.Load() || atomic.LoadInt32(&first.refs) != 0 || atomic.LoadInt32(&second.refs) != 0 {
					t.Fatalf("count=%d batch=%d err=%v: calls=%d refs=%d/%d", count, batch, failure, raw.calls.Load(), first.refs, second.refs)
				}
				if c.enqueue(writeJob{direct: []byte{1}}) {
					t.Fatal("enqueue after close")
				}
			}
		}
	}
}

func drainState(t *testing.T, c *Connection) ([]byte, *tickFrame) {
	t.Helper()
	var state []byte
	var shared *tickFrame
	for len(c.writeCh) > 0 {
		job := <-c.writeCh
		if job.frame == nil {
			continue
		}
		shared = job.frame
		reader := bytes.NewReader(job.frame.frame)
		for reader.Len() > 0 {
			hdr, err := ws.ReadHeader(reader)
			if err != nil {
				t.Fatal(err)
			}
			payload := make([]byte, hdr.Length)
			if _, err = io.ReadFull(reader, payload); err != nil {
				t.Fatal(err)
			}
			if payload[0] == protocol.MessageGameState || payload[0] == protocol.MessageDeltaGameState {
				state = payload
			}
		}
		job.frame.release()
		atomic.StoreInt32(&c.pendingBroadcast, 0)
		atomic.StoreInt64(&c.pendingStateNs, 0)
	}
	return state, shared
}

func TestMissedStopRecoversWithSharedFullSnapshot(t *testing.T) {
	s := reliabilityServer(t)
	s.gameWorld.Stop()
	players := []*types.Player{s.gameWorld.AddPlayer(""), s.gameWorld.AddPlayer("")}
	conns := []*Connection{}
	for _, p := range players {
		raw, peer := net.Pipe()
		t.Cleanup(func() { raw.Close(); peer.Close() })
		c := s.createConnection(p, raw)
		s.connections[p.ID] = c
		conns = append(conns, c)
	}
	s.admissions = 2
	states := []types.PlayerState{players[0].ToState(), players[1].ToState()}
	states[0].VX = 1
	s.broadcastTick(states, states, true, 1)
	drainState(t, conns[1])
	states[0].VX = 0
	s.broadcastTick(states, states[:1], false, 2)
	got, _ := drainState(t, conns[1])
	if got[0] != protocol.MessageDeltaGameState {
		t.Fatal("healthy client lost delta")
	}
	drainState(t, conns[0])
	conns[1].needsFullState.Store(true)
	s.broadcastTick(states, nil, false, 3)
	a, sharedA := drainState(t, conns[0])
	b, sharedB := drainState(t, conns[1])
	if a[0] != protocol.MessageGameState || b[0] != protocol.MessageGameState || sharedA != sharedB {
		t.Fatal("recovery was not a shared full snapshot")
	}
	if binary.LittleEndian.Uint32(a[1:5]) != 3 {
		t.Fatal("wrong recovery sequence")
	}
	_, n := binary.Uvarint(a[15:])
	if n <= 0 || a[15+n+4] != 0 {
		t.Fatal("missed STOP not recovered")
	}
}

func TestResyncRequestsAreBoundedAndDoNotEnqueue(t *testing.T) {
	s := reliabilityServer(t)
	raw, peer := net.Pipe()
	defer raw.Close()
	defer peer.Close()
	c := s.createConnection(&types.Player{ID: 1}, raw)
	for i := 0; i < 10000; i++ {
		s.sendInitialState(c)
	}
	if len(c.writeCh) != 0 || !c.needsFullState.Swap(false) {
		t.Fatal("resync did work on read path")
	}
	s.sendInitialState(c)
	if c.needsFullState.Load() {
		t.Fatal("resync limiter did not suppress repeated request")
	}
}

func dialTestWS(t *testing.T, url string) (net.Conn, io.Reader) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, reader, _, err := ws.DefaultDialer.Dial(ctx, "ws"+strings.TrimPrefix(url, "http")+"/ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if reader != nil {
		return c, reader
	}
	return c, c
}

func TestIncompleteFramesDoNotBlockOtherPlayersAndShutdownClosesAll(t *testing.T) {
	s := reliabilityServer(t)
	httpServer := httptest.NewServer(s.publicHandler())
	defer httpServer.Close()
	var slow []net.Conn
	for i := 0; i < 32; i++ {
		c, _ := dialTestWS(t, httpServer.URL)
		if _, err := c.Write([]byte{0x82}); err != nil {
			t.Fatal(err)
		}
		slow = append(slow, c)
	}
	c, reader := dialTestWS(t, httpServer.URL)
	ping := ws.MaskFrame(ws.NewBinaryFrame([]byte{protocol.MessagePing, 1, 2, 3, 4}))
	if err := ws.WriteFrame(c, ping); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(time.Second))
	for {
		h, err := ws.ReadHeader(reader)
		if err != nil {
			t.Fatal("healthy client blocked:", err)
		}
		p := make([]byte, h.Length)
		if _, err = io.ReadFull(reader, p); err != nil {
			t.Fatal(err)
		}
		if len(p) == 5 && p[0] == protocol.MessagePong {
			break
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := s.Shutdown(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if s.gameWorld.GetPlayerCount() != 0 {
		t.Fatal("players leaked at shutdown")
	}
	for _, conn := range slow {
		conn.SetReadDeadline(time.Now().Add(time.Second))
		_, err := io.Copy(io.Discard, conn)
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatal("socket still open after shutdown")
		}
	}
	resp, err := http.Get(httpServer.URL + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("admission during shutdown")
	}
}

func TestManagementRoutesArePrivateAndAdminRequiresBearer(t *testing.T) {
	s := reliabilityServer(t)
	s.adminStore = &liveconfig.Store{}
	s.cfg.Server.AdminToken = strings.Repeat("s", 32)
	for _, path := range []string{"/metrics", "/metrics/json", "/debug/pprof/", "/api/admin/units/1"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		s.publicHandler().ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("public %s = %d", path, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodPatch, "/api/admin/units/1", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	s.managementHandler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("unauthenticated admin accepted")
	}
	r = httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	w = httptest.NewRecorder()
	s.managementHandler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("pprof enabled by default")
	}
	r = httptest.NewRequest(http.MethodPatch, "/api/admin/units/bad", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+s.cfg.Server.AdminToken)
	w = httptest.NewRecorder()
	s.managementHandler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("authorized request did not reach handler")
	}
}

func TestStartAndShutdownLifecycle(t *testing.T) {
	s := reliabilityServer(t)
	result := make(chan error, 1)
	go func() { result <- s.Start() }()
	deadline := time.Now().Add(time.Second)
	for {
		s.lifecycleMu.Lock()
		started := s.httpServer != nil
		s.lifecycleMu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("start timeout")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
	if err := s.Start(); err == nil {
		t.Fatal("start after shutdown succeeded")
	}
}
