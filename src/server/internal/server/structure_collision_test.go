package server

import (
	"bytes"
	"io"
	"net"
	"testing"

	"github.com/gobwas/ws"

	"pixi_game_server/internal/protocol"
)

func readDirectPayloads(t *testing.T, c *Connection) [][]byte {
	t.Helper()
	var payloads [][]byte
	for len(c.writeCh) > 0 {
		job := <-c.writeCh
		if job.direct == nil {
			continue
		}
		reader := bytes.NewReader(job.direct)
		hdr, err := ws.ReadHeader(reader)
		if err != nil {
			t.Fatalf("ws.ReadHeader: %v", err)
		}
		payload := make([]byte, hdr.Length)
		if _, err := io.ReadFull(reader, payload); err != nil {
			t.Fatalf("read payload: %v", err)
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

func TestSendStructureCollisionSnapshot_SentAfterWelcome(t *testing.T) {
	s := reliabilityServer(t)
	s.gameWorld.Stop()
	s.SetCampaignID(1)

	player := mustAddPlayer(t, s.gameWorld, "")
	raw, peer := net.Pipe()
	t.Cleanup(func() { raw.Close(); peer.Close() })
	conn := s.createConnection(player, raw)

	s.sendWelcome(conn)
	s.sendStructureCollisionSnapshot(conn)

	payloads := readDirectPayloads(t, conn)
	if len(payloads) != 2 {
		t.Fatalf("got %d direct payloads, want 2 (welcome + snapshot)", len(payloads))
	}
	if payloads[0][0] != protocol.MessageWelcome {
		t.Fatalf("first payload type = %d, want MessageWelcome", payloads[0][0])
	}
	if payloads[1][0] != protocol.MessageStructureCollisionSnapshot {
		t.Fatalf("second payload type = %d, want MessageStructureCollisionSnapshot", payloads[1][0])
	}

	campaignID, revision, colliders, err := s.protocol.DecodeStructureCollisionSnapshot(payloads[1])
	if err != nil {
		t.Fatalf("DecodeStructureCollisionSnapshot: %v", err)
	}
	if campaignID != 1 {
		t.Fatalf("campaignID = %d, want 1", campaignID)
	}
	if revision != 0 {
		t.Fatalf("revision = %d, want 0 (no structures loaded in this test server)", revision)
	}
	if len(colliders) != 0 {
		t.Fatalf("expected no structure colliders in the empty test world, got %v", colliders)
	}
}

func TestResyncRequest_RespondsWithFreshSnapshot(t *testing.T) {
	s := reliabilityServer(t)
	s.gameWorld.Stop()
	s.SetCampaignID(1)

	player := mustAddPlayer(t, s.gameWorld, "")
	raw, peer := net.Pipe()
	t.Cleanup(func() { raw.Close(); peer.Close() })
	conn := s.createConnection(player, raw)
	s.connectionsMu.Lock()
	s.connections[player.ID] = conn
	s.connectionsMu.Unlock()

	s.processMessage(conn, []byte{protocol.MessageStructureCollisionResyncRequest, 0, 0, 0, 0, 0, 0, 0, 0})

	payloads := readDirectPayloads(t, conn)
	if len(payloads) != 1 || payloads[0][0] != protocol.MessageStructureCollisionSnapshot {
		t.Fatalf("expected exactly one fresh snapshot in response to resync, got %v", payloads)
	}
}
