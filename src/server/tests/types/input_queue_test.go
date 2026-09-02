package types_test

import (
	"testing"

	"pixi_game_server/internal/types"
)

func TestMovementInputQueuePreservesOrder(t *testing.T) {
	p := &types.Player{}
	for sequence := uint32(1); sequence <= 3; sequence++ {
		if got := p.EnqueueMovementInput(types.MovementInput{Sequence: sequence, DX: int8(sequence % 2), DY: -1}); got != types.InputAccepted {
			t.Fatalf("enqueue sequence %d = %d, want InputAccepted", sequence, got)
		}
	}

	for sequence := uint32(1); sequence <= 3; sequence++ {
		input, ok := p.DequeueMovementInput()
		if !ok || input.Sequence != sequence {
			t.Fatalf("dequeue = %+v/%v, want sequence %d", input, ok, sequence)
		}
	}
	if _, ok := p.DequeueMovementInput(); ok {
		t.Fatal("empty queue returned an input")
	}
}

func TestMovementInputQueueRejectsDuplicateAndOverflow(t *testing.T) {
	p := &types.Player{}
	if got := p.EnqueueMovementInput(types.MovementInput{Sequence: 1}); got != types.InputAccepted {
		t.Fatalf("first input = %d, want InputAccepted", got)
	}
	if got := p.EnqueueMovementInput(types.MovementInput{Sequence: 1}); got != types.InputStale {
		t.Fatalf("duplicate sequence = %d, want InputStale", got)
	}
	if got := p.EnqueueMovementInput(types.MovementInput{Sequence: 0}); got != types.InputStale {
		t.Fatalf("out-of-order sequence = %d, want InputStale", got)
	}

	p = &types.Player{}
	for sequence := uint32(1); sequence <= types.PlayerInputQueueCapacity; sequence++ {
		if got := p.EnqueueMovementInput(types.MovementInput{Sequence: sequence}); got != types.InputAccepted {
			t.Fatalf("queue filled early at sequence %d: %d", sequence, got)
		}
	}
	if got := p.EnqueueMovementInput(types.MovementInput{Sequence: types.PlayerInputQueueCapacity + 1}); got != types.InputQueueFull {
		t.Fatalf("overflow input = %d, want InputQueueFull", got)
	}
}

func TestMovementInputQueueAcceptsSequenceWrap(t *testing.T) {
	p := &types.Player{}
	for _, sequence := range []uint32{0xfffffffe, 0xffffffff, 0, 1} {
		if got := p.EnqueueMovementInput(types.MovementInput{Sequence: sequence}); got != types.InputAccepted {
			t.Fatalf("valid wrapped sequence %d = %d, want InputAccepted", sequence, got)
		}
	}
}
