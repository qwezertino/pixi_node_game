package server

import (
	"testing"

	"pixi_game_server/internal/liveconfig"
)

func TestDecodeUnitStatsPatchAbsentFieldNotPresent(t *testing.T) {
	patch, err := decodeUnitStatsPatch([]byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if patch.Block.Present {
		t.Fatalf("expected block to be absent, got %+v", patch.Block)
	}
	if patch.AttackStaminaCost.Present {
		t.Fatalf("expected attackStaminaCost to be absent, got %+v", patch.AttackStaminaCost)
	}
}

func TestDecodeUnitStatsPatchExplicitNullClears(t *testing.T) {
	patch, err := decodeUnitStatsPatch([]byte(`{"block":null,"attackStaminaCost":null}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !patch.Block.Present || patch.Block.Value != nil {
		t.Fatalf("expected block to be explicitly cleared, got %+v", patch.Block)
	}
	if !patch.AttackStaminaCost.Present || patch.AttackStaminaCost.Value != nil {
		t.Fatalf("expected attackStaminaCost to be explicitly cleared, got %+v", patch.AttackStaminaCost)
	}
}

func TestDecodeUnitStatsPatchValueSets(t *testing.T) {
	patch, err := decodeUnitStatsPatch([]byte(`{"block":{"meleeDR":0.2,"rangedDR":0.3,"drainPerSecond":5},"attackStaminaCost":42}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !patch.Block.Present || patch.Block.Value == nil || patch.Block.Value.MeleeDR != 0.2 {
		t.Fatalf("expected block to be set, got %+v", patch.Block)
	}
	if !patch.AttackStaminaCost.Present || patch.AttackStaminaCost.Value == nil || *patch.AttackStaminaCost.Value != 42 {
		t.Fatalf("expected attackStaminaCost to be set to 42, got %+v", patch.AttackStaminaCost)
	}
}

func TestDecodeUnitStatsPatchRejectsNullOnRequiredField(t *testing.T) {
	if _, err := decodeUnitStatsPatch([]byte(`{"hp":null}`)); err == nil {
		t.Fatalf("expected error when a NOT NULL field is set to null")
	}
}

func TestDecodeUnitStatsPatchAndMergeEndToEnd(t *testing.T) {
	patch, err := decodeUnitStatsPatch([]byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if patch.Block.Present {
		t.Fatalf("empty body must not mark block as present")
	}

	patch, err = decodeUnitStatsPatch([]byte(`{"block":null}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !patch.Block.Present || patch.Block.Value != nil {
		t.Fatalf("explicit null must mark block present with nil value, got %+v", patch.Block)
	}

	var _ liveconfig.NullableField[liveconfig.BlockPatch] = patch.Block
}
