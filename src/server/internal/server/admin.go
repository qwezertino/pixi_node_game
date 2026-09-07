package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"pixi_game_server/internal/liveconfig"
	"pixi_game_server/internal/units"
)

// EnableUnitAdminAPI wires PATCH /api/admin/units/{typeId} for the dev-only
// Unit Viewer panel (see src/client/debug/unitViewerPanel.ts). Call it only
// when ENABLE_UNIT_ADMIN_API=true (see cmd/server/main.go) — with no call,
// Start() never registers the route, so production deployments never expose
// a write path into game balance data.
func (s *Server) EnableUnitAdminAPI(store *liveconfig.Store) {
	s.adminStore = store
}

// unitStatsPatchRequest mirrors units.Definition's JSON shape (see
// src/shared/units.ts) so the client can round-trip whatever GET /api/units
// gave it. Pointer fields are the nullable/optional columns on `units` —
// an absent (or explicit null) field decodes as nil and UpdateUnitStats
// writes SQL NULL for it, so this is also how a mechanic (block, dash-thrust,
// combo, ...) gets added to or removed from a unit, not just tuned.
type unitStatsPatchRequest struct {
	HP                         float64    `json:"hp"`
	PassiveDR                  float64    `json:"passiveDR"`
	MoveSpeed                  float64    `json:"moveSpeed"`
	RangeType                  string     `json:"rangeType"`
	Range                      float64    `json:"range"`
	Damage                     float64    `json:"damage"`
	WindupSeconds              float64    `json:"windupSeconds"`
	ActiveSeconds              float64    `json:"activeSeconds"`
	RecoverySeconds            float64    `json:"recoverySeconds"`
	Stamina                    float64    `json:"stamina"`
	StaminaRegenPerSecond      float64    `json:"staminaRegenPerSecond"`
	SprintSpeedMultiplier      float64    `json:"sprintSpeedMultiplier"`
	SprintStaminaCostPerSecond float64    `json:"sprintStaminaCostPerSecond"`
	AnimationSpeed             float64    `json:"animationSpeed"`
	Cost                       units.Cost `json:"cost"`
	RequiresRoyalGuard         bool       `json:"requiresRoyalGuard"`
	Cleave                     bool       `json:"cleave"`
	HasBraceStance             bool       `json:"hasBraceStance"`

	ComboSteps               *int     `json:"comboSteps"`
	ComboWindowSeconds       *float64 `json:"comboWindowSeconds"`
	AttackStaminaCost        *float64 `json:"attackStaminaCost"`
	DrawHoldThresholdSeconds *float64 `json:"drawHoldThresholdSeconds"`
	DodgeCostMultiplier      *float64 `json:"dodgeCostMultiplier"`

	AntiShieldMultiplier        *float64 `json:"antiShieldMultiplier"`
	AntiWoodStructureMultiplier *float64 `json:"antiWoodStructureMultiplier"`

	Block           *blockPatchRequest           `json:"block"`
	PositionalBonus *positionalBonusPatchRequest `json:"positionalBonus"`
	OpportunistBow  *opportunistBowPatchRequest  `json:"opportunistBow"`
	RogueQuiver     *rogueQuiverPatchRequest     `json:"rogueQuiver"`
	Recon           *reconPatchRequest           `json:"recon"`
	FireArrow       *fireArrowPatchRequest       `json:"fireArrow"`
	DashThrust      *dashThrustPatchRequest      `json:"dashThrust"`
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

// handleAdminUpdateUnit only ever runs when EnableUnitAdminAPI registered it
// (see Start()). It writes straight to Postgres and publishes a reload
// notification (see internal/liveconfig.PublishUnitsChanged) — every
// connected server picks it up within moments, no restart. Already-spawned
// players keep the HP/stamina they spawned with; the response says so.
func (s *Server) handleAdminUpdateUnit(w http.ResponseWriter, r *http.Request) {
	typeID64, err := strconv.ParseUint(r.PathValue("typeId"), 10, 8)
	if err != nil {
		http.Error(w, "invalid unit type id", http.StatusBadRequest)
		return
	}

	var body unitStatsPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	patch := liveconfig.UnitStatsPatch{
		HP: body.HP, PassiveDR: body.PassiveDR, MoveSpeed: body.MoveSpeed,
		RangeType: body.RangeType, Range: body.Range, Damage: body.Damage,
		WindupSeconds: body.WindupSeconds, ActiveSeconds: body.ActiveSeconds, RecoverySeconds: body.RecoverySeconds,
		Stamina: body.Stamina, StaminaRegenPerSecond: body.StaminaRegenPerSecond,
		SprintSpeedMultiplier: body.SprintSpeedMultiplier, SprintStaminaCostPerSecond: body.SprintStaminaCostPerSecond,
		AnimationSpeed: body.AnimationSpeed,
		CostWood:       body.Cost.Wood, CostStone: body.Cost.Stone, CostIron: body.Cost.Iron,
		RequiresRoyalGuard: body.RequiresRoyalGuard, Cleave: body.Cleave, HasBraceStance: body.HasBraceStance,

		ComboSteps:               body.ComboSteps,
		ComboWindowSeconds:       body.ComboWindowSeconds,
		AttackStaminaCost:        body.AttackStaminaCost,
		DrawHoldThresholdSeconds: body.DrawHoldThresholdSeconds,
		DodgeCostMultiplier:      body.DodgeCostMultiplier,

		AntiShieldMultiplier:        body.AntiShieldMultiplier,
		AntiWoodStructureMultiplier: body.AntiWoodStructureMultiplier,
	}

	if body.Block != nil {
		patch.Block = &liveconfig.BlockPatch{
			MeleeDR: body.Block.MeleeDR, RangedDR: body.Block.RangedDR,
			DrainPerSecond: body.Block.DrainPerSecond, RecoverySeconds: body.Block.RecoverySeconds,
		}
	}
	if body.PositionalBonus != nil {
		patch.PositionalBonus = &liveconfig.PositionalBonusPatch{
			StaminaCostReductionPct: body.PositionalBonus.StaminaCostReductionPct,
			MinNearbyAllies:         body.PositionalBonus.MinNearbyAllies,
		}
	}
	if body.OpportunistBow != nil {
		patch.OpportunistBow = &liveconfig.OpportunistBowPatch{
			Damage: body.OpportunistBow.Damage, Range: body.OpportunistBow.Range,
			CooldownSeconds: body.OpportunistBow.CooldownSeconds,
		}
	}
	if body.RogueQuiver != nil {
		patch.RogueQuiver = &liveconfig.RogueQuiverPatch{
			Damage: body.RogueQuiver.Damage, Range: body.RogueQuiver.Range,
			Charges: body.RogueQuiver.Charges, RechargeSeconds: body.RogueQuiver.RechargeSeconds,
			ExecuteMultiplier: body.RogueQuiver.ExecuteMultiplier, ExecuteHpThresholdPct: body.RogueQuiver.ExecuteHpThresholdPct,
		}
	}
	if body.Recon != nil {
		patch.Recon = &liveconfig.ReconPatch{
			ViewRadiusBonusPct: body.Recon.ViewRadiusBonusPct, DetectionRadiusMeters: body.Recon.DetectionRadiusMeters,
		}
	}
	if body.FireArrow != nil {
		patch.FireArrow = &liveconfig.FireArrowPatch{
			Damage: body.FireArrow.Damage, StructureDamageMultiplier: body.FireArrow.StructureDamageMultiplier,
			WoodCostPerShot: body.FireArrow.WoodCostPerShot,
		}
	}
	if body.DashThrust != nil {
		patch.DashThrust = &liveconfig.DashThrustPatch{
			DistanceMeters: body.DashThrust.DistanceMeters, WindupSeconds: body.DashThrust.WindupSeconds,
			RecoverySeconds: body.DashThrust.RecoverySeconds, DamageMultiplier: body.DashThrust.DamageMultiplier,
			CooldownSeconds: body.DashThrust.CooldownSeconds,
		}
	}

	if err := s.adminStore.UpdateUnitStats(r.Context(), uint8(typeID64), patch); err != nil {
		if err == liveconfig.ErrUnitNotFound {
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
