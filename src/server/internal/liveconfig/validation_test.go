package liveconfig

import (
	"context"
	"errors"
	"testing"
)

func TestRejectInvalidPatchBeforeDatabaseWrite(t *testing.T) {
	s := &Store{}
	for _, p := range []UnitStatsPatch{{}, {HP: 100}, {HP: 100, MoveSpeed: -1}} {
		if err := s.UpdateUnitStats(context.Background(), 1, p); !errors.Is(err, ErrInvalidUnit) {
			t.Fatalf("invalid patch = %v", err)
		}
	}
	valid := UnitStatsPatch{HP: 100, MoveSpeed: 10, RangeType: "melee", Stamina: 100, SprintSpeedMultiplier: 1.5, AnimationSpeed: 0.1, ActiveSeconds: 0.1}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	cost := 700.0
	valid.AttackStaminaCost = &cost
	if err := valid.Validate(); err == nil {
		t.Fatal("accepted overflowing attack cost")
	}
}
