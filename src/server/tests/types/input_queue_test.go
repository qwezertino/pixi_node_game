package types_test

import (
	"testing"

	"pixi_game_server/internal/types"
)

func TestMovementInputOfferThenConsume(t *testing.T) {
	p := &types.Player{}
	if got := p.OfferMovementInput(types.MovementInput{Sequence: 1, DX: 1, DY: -1}); got != types.InputAccepted {
		t.Fatalf("offer = %s, want InputAccepted", got)
	}

	input, ok := p.ConsumeLatestMovementInput()
	if !ok || input.Sequence != 1 || input.DX != 1 || input.DY != -1 {
		t.Fatalf("consume = %+v/%v, want sequence 1", input, ok)
	}
	if _, ok := p.ConsumeLatestMovementInput(); ok {
		t.Fatal("consume with nothing pending returned an input")
	}
}

// Several samples can arrive before a server tick consumes one; only the newest
// should survive, since old samples are not a backlog of movement steps.
func TestMovementInputOfferKeepsOnlyLatestBeforeConsume(t *testing.T) {
	p := &types.Player{}
	for sequence := uint32(1); sequence <= 3; sequence++ {
		if got := p.OfferMovementInput(types.MovementInput{Sequence: sequence, DX: int8(sequence % 2), DY: -1}); got != types.InputAccepted {
			t.Fatalf("offer sequence %d = %s, want InputAccepted", sequence, got)
		}
	}

	input, ok := p.ConsumeLatestMovementInput()
	if !ok || input.Sequence != 3 {
		t.Fatalf("consume = %+v/%v, want sequence 3", input, ok)
	}
	if _, ok := p.ConsumeLatestMovementInput(); ok {
		t.Fatal("second consume should find nothing pending")
	}
}

func TestMovementInputRejectsDuplicateAndOutOfOrder(t *testing.T) {
	p := &types.Player{}
	if got := p.OfferMovementInput(types.MovementInput{Sequence: 5}); got != types.InputAccepted {
		t.Fatalf("first input = %s, want InputAccepted", got)
	}
	if got := p.OfferMovementInput(types.MovementInput{Sequence: 5}); got != types.InputStale {
		t.Fatalf("duplicate sequence = %s, want InputStale", got)
	}
	if got := p.OfferMovementInput(types.MovementInput{Sequence: 4}); got != types.InputStale {
		t.Fatalf("out-of-order sequence = %s, want InputStale", got)
	}
}

// The client sends one fixed-rate sample per tick, so any missing sequence means a
// dropped/duplicated message or a non-conforming client — treated as fatal rather
// than silently tolerated, since there is no backlog to replay it from.
func TestMovementInputRejectsSequenceGap(t *testing.T) {
	p := &types.Player{}
	if got := p.OfferMovementInput(types.MovementInput{Sequence: 10}); got != types.InputAccepted {
		t.Fatalf("first sample = %s", got)
	}
	if got := p.OfferMovementInput(types.MovementInput{Sequence: 12}); got != types.InputGap {
		t.Fatalf("gapped sample = %s, want InputGap", got)
	}
}

func TestMovementInputAcceptsSequenceWrap(t *testing.T) {
	p := &types.Player{}
	for _, sequence := range []uint32{0xfffffffe, 0xffffffff, 0, 1} {
		if got := p.OfferMovementInput(types.MovementInput{Sequence: sequence}); got != types.InputAccepted {
			t.Fatalf("valid wrapped sequence %d = %s, want InputAccepted", sequence, got)
		}
	}
}
