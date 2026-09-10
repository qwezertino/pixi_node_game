package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gobwas/ws"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/units"
)

// TestHandleHealthReportsUnhealthyWhenTickLoopStalls is the P2 regression
// from the 2026-09-10 review: /health only checked the stopping flag and
// whether the player count could be read; a hung tick loop left the
// service reporting "healthy" forever, defeating automatic instance
// removal from routing. handleHealth must now fail once the game loop has
// gone longer than the watchdog threshold without completing a tick.
func TestHandleHealthReportsUnhealthyWhenTickLoopStalls(t *testing.T) {
	// The watchdog threshold is max(20*tickInterval, minTickWatchdogThreshold).
	// TickRate is capped at 240 in config validation, so 20*tickInterval
	// cannot go below ~83ms; fold the fixed floor down too so the test
	// only has to wait out that tick-rate-derived floor, not the real
	// (multi-second) production minimum.
	original := minTickWatchdogThreshold
	minTickWatchdogThreshold = time.Millisecond
	t.Cleanup(func() { minTickWatchdogThreshold = original })

	err := units.LoadDefinitions([]units.Definition{{ID: "spearman", TypeID: 1, HP: 100, Stamina: 100,
		MoveSpeed: 10, RangeType: "melee", ActiveSeconds: 0.1, RecoverySeconds: 0.2,
		SprintSpeedMultiplier: 1.5, AnimationSpeed: 0.1}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Build(&config.GameSettings{TickRate: 240, SyncIntervalSec: 30, UnitsPerMeter: 10,
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

	time.Sleep(20 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthy tick loop reported %d: %s", rec.Code, rec.Body.String())
	}

	s.gameWorld.Stop()
	time.Sleep(150 * time.Millisecond)

	rec2 := httptest.NewRecorder()
	s.handleHealth(rec2, req)
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("stalled tick loop reported %d, want %d: %s", rec2.Code, http.StatusServiceUnavailable, rec2.Body.String())
	}
}

func TestClientHeaderRejectReason(t *testing.T) {
	valid := ws.Header{Masked: true, Fin: true, OpCode: ws.OpBinary, Length: 6}

	cases := []struct {
		name   string
		mutate func(ws.Header) ws.Header
		want   string
	}{
		{"unmasked", func(h ws.Header) ws.Header { h.Masked = false; return h }, "unmasked_client_frame"},
		{"fragmented", func(h ws.Header) ws.Header { h.Fin = false; return h }, "fragmented_frame"},
		{"reserved", func(h ws.Header) ws.Header { h.Rsv = 1; return h }, "reserved_bits_set"},
		{"oversized", func(h ws.Header) ws.Header { h.Length = maxClientFramePayload + 1; return h }, "payload_too_large"},
		{"text", func(h ws.Header) ws.Header { h.OpCode = ws.OpText; return h }, "unsupported_opcode"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hdr := tc.mutate(valid)
			if validClientHeader(hdr) {
				t.Fatal("header accepted, expected rejection")
			}
			if got := clientHeaderRejectReason(hdr); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}
