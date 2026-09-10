package units

import (
	"fmt"
	"math"
	"reflect"
)

func ValidateDefinition(u Definition) error {
	if u.ID == "" {
		return fmt.Errorf("unit ID is required")
	}
	if err := validateNumbers(reflect.ValueOf(u), u.ID); err != nil {
		return err
	}
	bounds := []struct {
		name      string
		v, lo, hi float64
	}{
		{"hp", u.HP, 1, 65535}, {"stamina", u.Stamina, 0, 655.35},
		{"moveSpeed", u.MoveSpeed, 0.001, 100}, {"passiveDR", u.PassiveDR, 0, 1},
		{"staminaRegenPerSecond", u.StaminaRegenPerSecond, 0, 655.35},
		{"sprintSpeedMultiplier", u.SprintSpeedMultiplier, 1, 10},
		{"sprintStaminaCostPerSecond", u.SprintStaminaCostPerSecond, 0, 655.35},
		{"animationSpeed", u.AnimationSpeed, 0.001, 100},
		{"attackDuration", u.WindupSeconds + u.ActiveSeconds + u.RecoverySeconds, 0.001, 3600},
		{"comboWindowSeconds", u.ComboWindowSeconds, 0, 3600},
	}
	for _, b := range bounds {
		if b.v < b.lo || b.v > b.hi {
			return fmt.Errorf("%s.%s must be between %g and %g", u.ID, b.name, b.lo, b.hi)
		}
	}
	if u.RangeType != "melee" && u.RangeType != "ranged" {
		return fmt.Errorf("%s: invalid rangeType", u.ID)
	}
	if u.ComboSteps < 0 || u.ComboSteps > 4 {
		return fmt.Errorf("%s: comboSteps must be 0..4", u.ID)
	}
	if u.AttackStaminaCost != nil && *u.AttackStaminaCost > 655.35 {
		return fmt.Errorf("%s: attack stamina cost overflows wire storage", u.ID)
	}
	if b := u.Block; b != nil {
		if b.MeleeDR > 1 || b.RangedDR > 1 || b.DrainPerSecond > 655.35 {
			return fmt.Errorf("%s: invalid block profile", u.ID)
		}
	}

	// These map to Postgres SMALLINT columns (int2, range -32768..32767); the
	// generic 0..65535 check in validateNumbers is too wide for them.
	type smallintField struct {
		name string
		v    int
	}
	var smallints []smallintField
	if u.PositionalBonus != nil {
		smallints = append(smallints, smallintField{"positionalBonus.minNearbyAllies", u.PositionalBonus.MinNearbyAllies})
	}
	if u.RogueQuiver != nil {
		smallints = append(smallints, smallintField{"rogueQuiver.charges", u.RogueQuiver.Charges})
	}
	if u.FireArrow != nil {
		smallints = append(smallints, smallintField{"fireArrow.woodCostPerShot", u.FireArrow.WoodCostPerShot})
	}
	for _, si := range smallints {
		if si.v < 0 || si.v > 32767 {
			return fmt.Errorf("%s.%s must be between 0 and 32767 (stored as SQL smallint)", u.ID, si.name)
		}
	}
	return nil
}

func validateNumbers(v reflect.Value, path string) error {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return validateNumbers(v.Elem(), path)
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if err := validateNumbers(v.Field(i), path+"."+v.Type().Field(i).Name); err != nil {
				return err
			}
		}
	case reflect.Float64:
		n := v.Float()
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1000000 {
			return fmt.Errorf("%s must be finite and between 0 and 1000000", path)
		}
	case reflect.Int:
		if n := v.Int(); n < 0 || n > 65535 {
			return fmt.Errorf("%s must be between 0 and 65535", path)
		}
	}
	return nil
}
