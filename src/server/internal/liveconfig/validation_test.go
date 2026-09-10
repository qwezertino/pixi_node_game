package liveconfig

import (
	"testing"

	"pixi_game_server/internal/units"
)

func validUnitRow() unitRow {
	return unitRow{
		def: units.Definition{
			TypeID: 1, ID: "spearman", DisplayName: "Spearman", Tier: "t1",
			HP: 100, MoveSpeed: 10, RangeType: "melee", Range: 1.5, Damage: 10,
			WindupSeconds: 0.1, ActiveSeconds: 0.1, RecoverySeconds: 0.1,
			Stamina: 100, SprintSpeedMultiplier: 1.5, AnimationSpeed: 0.1,
		},
	}
}

func floatPtr(v float64) *float64 { return &v }

func TestMergeUnitStatsPatchRejectsInvalidValues(t *testing.T) {
	for _, patch := range []UnitStatsPatch{
		{MoveSpeed: floatPtr(-1)},
		{AttackStaminaCost: Set(700.0)},
	} {
		merged := mergeUnitStatsPatch(validUnitRow(), patch)
		if err := units.ValidateDefinition(merged.def); err == nil {
			t.Fatalf("expected invalid patch %+v to fail validation", patch)
		}
	}
}

func TestMergeUnitStatsPatchPreservesUntouchedFields(t *testing.T) {
	current := validUnitRow()
	merged := mergeUnitStatsPatch(current, UnitStatsPatch{HP: floatPtr(250)})

	if err := units.ValidateDefinition(merged.def); err != nil {
		t.Fatalf("partial patch should stay valid, got %v", err)
	}
	if merged.def.HP != 250 {
		t.Fatalf("expected HP to be updated to 250, got %v", merged.def.HP)
	}
	if merged.def.MoveSpeed != current.def.MoveSpeed {
		t.Fatalf("partial patch clobbered MoveSpeed: got %v, want %v", merged.def.MoveSpeed, current.def.MoveSpeed)
	}
	if merged.def.Stamina != current.def.Stamina {
		t.Fatalf("partial patch clobbered Stamina: got %v, want %v", merged.def.Stamina, current.def.Stamina)
	}
	if merged.def.RangeType != current.def.RangeType {
		t.Fatalf("partial patch clobbered RangeType: got %v, want %v", merged.def.RangeType, current.def.RangeType)
	}
}

func TestMergeUnitStatsPatchPreservesComboFieldsWhenAbsent(t *testing.T) {
	comboSteps := 3
	comboWindow := 1.5
	current := validUnitRow()
	current.comboSteps = &comboSteps
	current.comboWindowSeconds = &comboWindow

	merged := mergeUnitStatsPatch(current, UnitStatsPatch{HP: floatPtr(250)})
	if merged.comboSteps == nil || *merged.comboSteps != comboSteps {
		t.Fatalf("partial patch clobbered comboSteps: %v", merged.comboSteps)
	}
	if merged.comboWindowSeconds == nil || *merged.comboWindowSeconds != comboWindow {
		t.Fatalf("partial patch clobbered comboWindowSeconds: %v", merged.comboWindowSeconds)
	}
}

func TestMergeUnitStatsPatchAbsentFieldLeavesBlockUntouched(t *testing.T) {
	current := validUnitRow()
	current.def.Block = &units.BlockProfile{MeleeDR: 0.5, RangedDR: 0.5, DrainPerSecond: 10}

	merged := mergeUnitStatsPatch(current, UnitStatsPatch{HP: floatPtr(250)})
	if merged.def.Block == nil || *merged.def.Block != *current.def.Block {
		t.Fatalf("absent block field should be untouched, got %+v", merged.def.Block)
	}
}

func TestMergeUnitStatsPatchExplicitNullClearsBlock(t *testing.T) {
	current := validUnitRow()
	current.def.Block = &units.BlockProfile{MeleeDR: 0.5, RangedDR: 0.5, DrainPerSecond: 10}

	merged := mergeUnitStatsPatch(current, UnitStatsPatch{Block: Clear[BlockPatch]()})
	if merged.def.Block != nil {
		t.Fatalf("explicit null block should clear it, got %+v", merged.def.Block)
	}
}

func TestMergeUnitStatsPatchValueSetsBlock(t *testing.T) {
	current := validUnitRow()

	merged := mergeUnitStatsPatch(current, UnitStatsPatch{
		Block: Set(BlockPatch{MeleeDR: 0.2, RangedDR: 0.3, DrainPerSecond: 5}),
	})
	if merged.def.Block == nil || merged.def.Block.MeleeDR != 0.2 || merged.def.Block.RangedDR != 0.3 || merged.def.Block.DrainPerSecond != 5 {
		t.Fatalf("expected block to be set from patch, got %+v", merged.def.Block)
	}
}

func TestMergeUnitStatsPatchAbsentVsNullAttackStaminaCost(t *testing.T) {
	current := validUnitRow()
	cost := 15.0
	current.def.AttackStaminaCost = &cost

	absent := mergeUnitStatsPatch(current, UnitStatsPatch{HP: floatPtr(250)})
	if absent.def.AttackStaminaCost == nil || *absent.def.AttackStaminaCost != cost {
		t.Fatalf("absent attackStaminaCost should be untouched, got %v", absent.def.AttackStaminaCost)
	}

	cleared := mergeUnitStatsPatch(current, UnitStatsPatch{AttackStaminaCost: Clear[float64]()})
	if cleared.def.AttackStaminaCost != nil {
		t.Fatalf("explicit null attackStaminaCost should clear it, got %v", cleared.def.AttackStaminaCost)
	}

	set := mergeUnitStatsPatch(current, UnitStatsPatch{AttackStaminaCost: Set(42.0)})
	if set.def.AttackStaminaCost == nil || *set.def.AttackStaminaCost != 42.0 {
		t.Fatalf("expected attackStaminaCost to be set to 42, got %v", set.def.AttackStaminaCost)
	}
}
