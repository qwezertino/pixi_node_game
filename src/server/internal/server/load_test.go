package server

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"pixi_game_server/internal/protocol"
)

func TestLoadConnections(t *testing.T) {
	count, _ := strconv.Atoi(os.Getenv("GAME_LOAD_CLIENTS"))
	if count == 0 {
		t.Skip("set GAME_LOAD_CLIENTS for the local load smoke test")
	}
	if count < 1 || count > 1200 {
		t.Fatal("GAME_LOAD_CLIENTS must be 1..1200")
	}
	seconds := 15
	if v, err := strconv.Atoi(os.Getenv("GAME_LOAD_SECONDS")); err == nil && v > 0 {
		seconds = v
	}
	s := reliabilityServer(t)
	httpServer := httptest.NewServer(s.publicHandler())
	defer httpServer.Close()
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
	sockets := make([]net.Conn, count)
	var readers sync.WaitGroup
	var closing atomic.Bool
	var gaps, failures, frames, acks, received atomic.Int64
	for i := 0; i < count; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, buffer, _, err := ws.DefaultDialer.Dial(ctx, url)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		sockets[i] = conn
		var reader io.Reader = conn
		if buffer != nil {
			reader = buffer
		}
		readers.Add(1)
		go func() {
			defer readers.Done()
			var sequence uint32
			hasSequence := false
			for {
				header, err := ws.ReadHeader(reader)
				if err != nil {
					if !closing.Load() {
						failures.Add(1)
					}
					return
				}
				payload := make([]byte, header.Length)
				if _, err = io.ReadFull(reader, payload); err != nil {
					if !closing.Load() {
						failures.Add(1)
					}
					return
				}
				received.Add(int64(len(payload)))
				if len(payload) == 0 {
					continue
				}
				switch payload[0] {
				case protocol.MessageGameState, protocol.MessageDeltaGameState:
					next := binary.LittleEndian.Uint32(payload[1:5])
					if hasSequence && payload[0] == protocol.MessageDeltaGameState && next-sequence != 1 {
						gaps.Add(1)
					}
					sequence = next
					hasSequence = true
					frames.Add(1)
				case protocol.MessageMovementAck:
					acks.Add(1)
				}
			}
		}()
	}
	t.Cleanup(func() {
		closing.Store(true)
		for _, conn := range sockets {
			if conn != nil {
				conn.Close()
			}
		}
		readers.Wait()
	})
	if got := s.gameWorld.GetPlayerCount(); got != count {
		t.Fatalf("connected=%d want=%d", got, count)
	}
	start := time.Now()
	beforeBytes := received.Load()
	beforeFrames := frames.Load()
	for turn := 1; turn <= seconds*2; turn++ {
		turnStart := time.Now()
		for _, conn := range sockets {
			payload := []byte{protocol.MessageMove, protocol.PackMovement(int8(turn%3)-1, 0), 0, 0, 0, 0}
			binary.LittleEndian.PutUint32(payload[2:], uint32(turn))
			if err := ws.WriteFrame(conn, ws.MaskFrame(ws.NewBinaryFrame(payload))); err != nil {
				failures.Add(1)
			}
		}
		if wait := 500*time.Millisecond - time.Since(turnStart); wait > 0 {
			time.Sleep(wait)
		}
	}
	elapsed := time.Since(start)
	t.Logf("clients=%d plateau=%s frames=%d payload_MBps=%.2f gaps=%d errors=%d acks=%d dilation=%d", count, elapsed, frames.Load()-beforeFrames, float64(received.Load()-beforeBytes)/elapsed.Seconds()/1e6, gaps.Load(), failures.Load(), acks.Load(), s.currentDilationBps())
	if failures.Load() != 0 || gaps.Load() != 0 || acks.Load() < int64(count) {
		t.Fatal("load smoke test failed")
	}
	closing.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	readers.Wait()
}
