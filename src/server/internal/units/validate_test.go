package units

import (
	"math"
	"testing"
)

func validDefinition() Definition {
	return Definition{ID: DefaultUnitType, TypeID: 1, HP: 100, Stamina: 100, MoveSpeed: 10, RangeType: "melee", ActiveSeconds: 0.1, RecoverySeconds: 0.2, SprintSpeedMultiplier: 1.5, AnimationSpeed: 0.1}
}

func TestRejectInvalidDefinitionsWithoutPublishing(t *testing.T) {
	good := validDefinition()
	if err := LoadDefinitions([]Definition{good}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Definition){
		func(u *Definition) { u.Stamina = 655.36 }, func(u *Definition) { u.HP = 65536 }, func(u *Definition) { u.MoveSpeed = -1 },
		func(u *Definition) { u.ComboSteps = 5 }, func(u *Definition) { u.ActiveSeconds = math.NaN() },
		func(u *Definition) { u.StaminaRegenPerSecond = math.Inf(1) }, func(u *Definition) { u.Block = &BlockProfile{DrainPerSecond: 1000} },
		func(u *Definition) { u.Cost.Wood = -1 },
	} {
		bad := good
		mutate(&bad)
		if err := LoadDefinitions([]Definition{bad}); err == nil {
			t.Fatal("accepted invalid unit")
		}
		if Get(DefaultUnitType).Stamina != good.Stamina {
			t.Fatal("invalid unit published")
		}
	}
	if err := LoadDefinitions([]Definition{good, good}); err == nil {
		t.Fatal("accepted duplicate ID")
	}
	other := good
	other.ID = "other"
	if err := LoadDefinitions([]Definition{good, other}); err == nil {
		t.Fatal("accepted duplicate type ID")
	}
}
