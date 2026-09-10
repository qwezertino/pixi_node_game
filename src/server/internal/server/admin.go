package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"pixi_game_server/internal/liveconfig"
)

func (s *Server) EnableUnitAdminAPI(store *liveconfig.Store) {
	s.adminStore = store
}

type blockPatchRequest struct {
	MeleeDR         float64  `json:"meleeDR"`
	RangedDR        float64  `json:"rangedDR"`
	DrainPerSecond  float64  `json:"drainPerSecond"`
	RecoverySeconds *float64 `json:"recoverySeconds"`
}

type positionalBonusPatchRequest struct {
	StaminaCostReductionPct float64 `json:"staminaCostReductionPct"`
	MinNearbyAllies         int     `json:"minNearbyAllies"`
}

type opportunistBowPatchRequest struct {
	Damage          float64 `json:"damage"`
	Range           float64 `json:"range"`
	CooldownSeconds float64 `json:"cooldownSeconds"`
}

type rogueQuiverPatchRequest struct {
	Damage                float64 `json:"damage"`
	Range                 float64 `json:"range"`
	Charges               int     `json:"charges"`
	RechargeSeconds       float64 `json:"rechargeSeconds"`
	ExecuteMultiplier     float64 `json:"executeMultiplier"`
	ExecuteHpThresholdPct float64 `json:"executeHpThresholdPct"`
}

type reconPatchRequest struct {
	ViewRadiusBonusPct    float64 `json:"viewRadiusBonusPct"`
	DetectionRadiusMeters float64 `json:"detectionRadiusMeters"`
}

type fireArrowPatchRequest struct {
	Damage                    float64 `json:"damage"`
	StructureDamageMultiplier float64 `json:"structureDamageMultiplier"`
	WoodCostPerShot           int     `json:"woodCostPerShot"`
}

type dashThrustPatchRequest struct {
	DistanceMeters   float64 `json:"distanceMeters"`
	WindupSeconds    float64 `json:"windupSeconds"`
	RecoverySeconds  float64 `json:"recoverySeconds"`
	DamageMultiplier float64 `json:"damageMultiplier"`
	CooldownSeconds  float64 `json:"cooldownSeconds"`
}

func isJSONNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}

// decodeRequiredField sets *dst when key is present with a non-null value.
// A key present with an explicit null is rejected: the underlying column is
// NOT NULL, so there is no way to "clear" it, only to leave it untouched
// (key absent) or set it (key present, non-null).
func decodeRequiredField[T any](raw map[string]json.RawMessage, key string, dst **T) error {
	v, ok := raw[key]
	if !ok {
		return nil
	}
	if isJSONNull(v) {
		return fmt.Errorf("%s cannot be null", key)
	}
	var val T
	if err := json.Unmarshal(v, &val); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*dst = &val
	return nil
}

// decodeNullableField distinguishes absent (dst left as its zero value, i.e.
// not present), explicit null (dst.Present = true, dst.Value = nil, meaning
// "clear this field"), and a provided value (dst.Present = true, dst.Value
// set).
func decodeNullableField[T any](raw map[string]json.RawMessage, key string, dst *liveconfig.NullableField[T]) error {
	v, ok := raw[key]
	if !ok {
		return nil
	}
	dst.Present = true
	if isJSONNull(v) {
		dst.Value = nil
		return nil
	}
	var val T
	if err := json.Unmarshal(v, &val); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	dst.Value = &val
	return nil
}

func decodeUnitStatsPatch(body []byte) (liveconfig.UnitStatsPatch, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return liveconfig.UnitStatsPatch{}, err
	}

	var patch liveconfig.UnitStatsPatch

	type requiredFloat struct {
		key string
		dst **float64
	}
	for _, f := range []requiredFloat{
		{"hp", &patch.HP}, {"passiveDR", &patch.PassiveDR}, {"moveSpeed", &patch.MoveSpeed},
		{"range", &patch.Range}, {"damage", &patch.Damage},
		{"windupSeconds", &patch.WindupSeconds}, {"activeSeconds", &patch.ActiveSeconds}, {"recoverySeconds", &patch.RecoverySeconds},
		{"stamina", &patch.Stamina}, {"staminaRegenPerSecond", &patch.StaminaRegenPerSecond},
		{"sprintSpeedMultiplier", &patch.SprintSpeedMultiplier}, {"sprintStaminaCostPerSecond", &patch.SprintStaminaCostPerSecond},
		{"animationSpeed", &patch.AnimationSpeed},
	} {
		if err := decodeRequiredField(raw, f.key, f.dst); err != nil {
			return patch, err
		}
	}
	if err := decodeRequiredField(raw, "rangeType", &patch.RangeType); err != nil {
		return patch, err
	}

	if costRaw, ok := raw["cost"]; ok {
		if isJSONNull(costRaw) {
			return patch, fmt.Errorf("cost cannot be null")
		}
		var cost struct {
			Wood  *int `json:"wood"`
			Stone *int `json:"stone"`
			Iron  *int `json:"iron"`
		}
		if err := json.Unmarshal(costRaw, &cost); err != nil {
			return patch, fmt.Errorf("cost: %w", err)
		}
		patch.CostWood, patch.CostStone, patch.CostIron = cost.Wood, cost.Stone, cost.Iron
	}

	type requiredBool struct {
		key string
		dst **bool
	}
	for _, f := range []requiredBool{
		{"requiresRoyalGuard", &patch.RequiresRoyalGuard}, {"cleave", &patch.Cleave}, {"hasBraceStance", &patch.HasBraceStance},
	} {
		if err := decodeRequiredField(raw, f.key, f.dst); err != nil {
			return patch, err
		}
	}

	if err := decodeNullableField(raw, "comboSteps", &patch.ComboSteps); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "comboWindowSeconds", &patch.ComboWindowSeconds); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "attackStaminaCost", &patch.AttackStaminaCost); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "drawHoldThresholdSeconds", &patch.DrawHoldThresholdSeconds); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "dodgeCostMultiplier", &patch.DodgeCostMultiplier); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "antiShieldMultiplier", &patch.AntiShieldMultiplier); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "antiWoodStructureMultiplier", &patch.AntiWoodStructureMultiplier); err != nil {
		return patch, err
	}

	if err := decodeNullableField(raw, "block", &patch.Block); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "positionalBonus", &patch.PositionalBonus); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "opportunistBow", &patch.OpportunistBow); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "rogueQuiver", &patch.RogueQuiver); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "recon", &patch.Recon); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "fireArrow", &patch.FireArrow); err != nil {
		return patch, err
	}
	if err := decodeNullableField(raw, "dashThrust", &patch.DashThrust); err != nil {
		return patch, err
	}

	return patch, nil
}

func (s *Server) handleAdminUpdateUnit(w http.ResponseWriter, r *http.Request) {
	typeID64, err := strconv.ParseUint(r.PathValue("typeId"), 10, 8)
	if err != nil {
		http.Error(w, "invalid unit type id", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	patch, err := decodeUnitStatsPatch(body)
	if err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.adminStore.UpdateUnitStats(r.Context(), uint8(typeID64), patch); err != nil {
		if errors.Is(err, liveconfig.ErrInvalidUnit) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, liveconfig.ErrUnitNotFound) {
			http.Error(w, "unit not found", http.StatusNotFound)
			return
		}
		slog.Error("admin: failed to update unit stats", "type_id", typeID64, "error", err)
		http.Error(w, "failed to update unit", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"saved","note":"applied live — new spawns and per-tick stats (speed, stamina, attack timing) use it now; already-spawned players keep their current HP/stamina until they reconnect"}`))
}
