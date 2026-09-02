package server

import (
	"testing"

	"github.com/gobwas/ws"
)

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
