package liveconfig

import (
	"context"
	"database/sql"
	"errors"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/units"
)

var ErrUnitNotFound = errors.New("unit not found")

// LoadGameSettings reads the singleton `game_settings` row (see
// docker/postgres/init/001_init.sql) — the static game/world rules that used
// to live in gameConfig.json. There is no fallback: this table is expected
// to exist and be seeded by the migration.
func (s *Store) LoadGameSettings(ctx context.Context) (*config.GameSettings, error) {
	var gs config.GameSettings
	err := s.pool.QueryRow(ctx, `
		SELECT tick_rate, sync_interval_sec, units_per_meter,
		       world_width, world_height,
		       spawn_min_x, spawn_max_x, spawn_min_y, spawn_max_y,
		       player_base_scale, debug_mode, world_background_color
		FROM game_settings WHERE id = 1
	`).Scan(
		&gs.TickRate, &gs.SyncIntervalSec, &gs.UnitsPerMeter,
		&gs.WorldWidth, &gs.WorldHeight,
		&gs.SpawnMinX, &gs.SpawnMaxX, &gs.SpawnMinY, &gs.SpawnMaxY,
		&gs.PlayerBaseScale, &gs.DebugMode, &gs.WorldBackgroundColor,
	)
	if err != nil {
		return nil, err
	}
	return &gs, nil
}

// LoadUnitDefinitions reads every row of the `units` table (see
// docker/postgres/init/001_init.sql) — the per-unit-type stats that used to
// live in units.json. There is no fallback: this table is expected to exist
// and be seeded by the migration.
func (s *Store) LoadUnitDefinitions(ctx context.Context) ([]units.Definition, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			type_id, id, display_name, tier,
			hp, passive_dr, move_speed, range_type, range, damage,
			windup_seconds, active_seconds, recovery_seconds,
			stamina, stamina_regen_per_second, sprint_speed_multiplier, sprint_stamina_cost_per_second,
			combo_steps, combo_window_seconds,
			animation_speed, attack_stamina_cost, draw_hold_threshold_seconds, dodge_cost_multiplier,
			cost_wood, cost_stone, cost_iron,
			requires_royal_guard, cleave, has_brace_stance,
			block_melee_dr, block_ranged_dr, block_drain_per_second, block_recovery_seconds,
			positional_stamina_cost_reduction_pct, positional_min_nearby_allies,
			opportunist_bow_damage, opportunist_bow_range, opportunist_bow_cooldown_seconds,
			rogue_quiver_damage, rogue_quiver_range, rogue_quiver_charges, rogue_quiver_recharge_seconds,
			rogue_quiver_execute_multiplier, rogue_quiver_execute_hp_threshold_pct,
			recon_view_radius_bonus_pct, recon_detection_radius_meters,
			fire_arrow_damage, fire_arrow_structure_damage_multiplier, fire_arrow_wood_cost_per_shot,
			dash_thrust_distance_meters, dash_thrust_windup_seconds, dash_thrust_recovery_seconds,
			dash_thrust_damage_multiplier, dash_thrust_cooldown_seconds,
			anti_shield_multiplier, anti_wood_structure_multiplier,
			asset_path, combat_asset_path, dash_asset_path
		FROM units
		ORDER BY type_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var defs []units.Definition
	for rows.Next() {
		var d units.Definition
		var comboSteps, positionalMinAllies, rogueCharges, fireArrowWoodCost sql.NullInt64
		var comboWindowSeconds, attackStaminaCost, drawHoldThresholdSeconds, dodgeCostMultiplier sql.NullFloat64
		var blockMeleeDR, blockRangedDR, blockDrainPerSecond, blockRecoverySeconds sql.NullFloat64
		var positionalStaminaCostReductionPct sql.NullFloat64
		var opportunistBowDamage, opportunistBowRange, opportunistBowCooldown sql.NullFloat64
		var rogueQuiverDamage, rogueQuiverRange, rogueQuiverRecharge, rogueQuiverExecuteMult, rogueQuiverExecuteHPPct sql.NullFloat64
		var reconViewRadiusPct, reconDetectionRadius sql.NullFloat64
		var fireArrowDamage, fireArrowStructureMult sql.NullFloat64
		var dashDistance, dashWindup, dashRecovery, dashDamageMult, dashCooldown sql.NullFloat64
		var antiShieldMultiplier, antiWoodStructureMultiplier sql.NullFloat64
		var assetPath, combatAssetPath, dashAssetPath sql.NullString

		err := rows.Scan(
			&d.TypeID, &d.ID, &d.DisplayName, &d.Tier,
			&d.HP, &d.PassiveDR, &d.MoveSpeed, &d.RangeType, &d.Range, &d.Damage,
			&d.WindupSeconds, &d.ActiveSeconds, &d.RecoverySeconds,
			&d.Stamina, &d.StaminaRegenPerSecond, &d.SprintSpeedMultiplier, &d.SprintStaminaCostPerSecond,
			&comboSteps, &comboWindowSeconds,
			&d.AnimationSpeed, &attackStaminaCost, &drawHoldThresholdSeconds, &dodgeCostMultiplier,
			&d.Cost.Wood, &d.Cost.Stone, &d.Cost.Iron,
			&d.RequiresRoyalGuard, &d.Cleave, &d.HasBraceStance,
			&blockMeleeDR, &blockRangedDR, &blockDrainPerSecond, &blockRecoverySeconds,
			&positionalStaminaCostReductionPct, &positionalMinAllies,
			&opportunistBowDamage, &opportunistBowRange, &opportunistBowCooldown,
			&rogueQuiverDamage, &rogueQuiverRange, &rogueCharges, &rogueQuiverRecharge,
			&rogueQuiverExecuteMult, &rogueQuiverExecuteHPPct,
			&reconViewRadiusPct, &reconDetectionRadius,
			&fireArrowDamage, &fireArrowStructureMult, &fireArrowWoodCost,
			&dashDistance, &dashWindup, &dashRecovery, &dashDamageMult, &dashCooldown,
			&antiShieldMultiplier, &antiWoodStructureMultiplier,
			&assetPath, &combatAssetPath, &dashAssetPath,
		)
		if err != nil {
			return nil, err
		}

		if comboSteps.Valid {
			d.ComboSteps = int(comboSteps.Int64)
		}
		if comboWindowSeconds.Valid {
			d.ComboWindowSeconds = comboWindowSeconds.Float64
		}
		d.AttackStaminaCost = nullFloatPtr(attackStaminaCost)
		d.DrawHoldThresholdSeconds = nullFloatPtr(drawHoldThresholdSeconds)
		d.DodgeCostMultiplier = nullFloatPtr(dodgeCostMultiplier)
		d.AntiShieldMultiplier = nullFloatPtr(antiShieldMultiplier)
		d.AntiWoodStructureMultiplier = nullFloatPtr(antiWoodStructureMultiplier)
		d.AssetPath = assetPath.String
		d.CombatAssetPath = combatAssetPath.String
		d.DashAssetPath = dashAssetPath.String

		if blockMeleeDR.Valid {
			d.Block = &units.BlockProfile{
				MeleeDR:         blockMeleeDR.Float64,
				RangedDR:        blockRangedDR.Float64,
				DrainPerSecond:  blockDrainPerSecond.Float64,
				RecoverySeconds: nullFloatPtr(blockRecoverySeconds),
			}
		}
		if positionalStaminaCostReductionPct.Valid {
			d.PositionalBonus = &units.PositionalBonus{
				StaminaCostReductionPct: positionalStaminaCostReductionPct.Float64,
				MinNearbyAllies:         int(positionalMinAllies.Int64),
			}
		}
		if opportunistBowDamage.Valid {
			d.OpportunistBow = &units.OpportunistBow{
				Damage:          opportunistBowDamage.Float64,
				Range:           opportunistBowRange.Float64,
				CooldownSeconds: opportunistBowCooldown.Float64,
			}
		}
		if rogueQuiverDamage.Valid {
			d.RogueQuiver = &units.RogueQuiver{
				Damage:                rogueQuiverDamage.Float64,
				Range:                 rogueQuiverRange.Float64,
				Charges:               int(rogueCharges.Int64),
				RechargeSeconds:       rogueQuiverRecharge.Float64,
				ExecuteMultiplier:     rogueQuiverExecuteMult.Float64,
				ExecuteHpThresholdPct: rogueQuiverExecuteHPPct.Float64,
			}
		}
		if reconViewRadiusPct.Valid {
			d.Recon = &units.Recon{
				ViewRadiusBonusPct:    reconViewRadiusPct.Float64,
				DetectionRadiusMeters: reconDetectionRadius.Float64,
			}
		}
		if fireArrowDamage.Valid {
			d.FireArrow = &units.FireArrow{
				Damage:                    fireArrowDamage.Float64,
				StructureDamageMultiplier: fireArrowStructureMult.Float64,
				WoodCostPerShot:           int(fireArrowWoodCost.Int64),
			}
		}
		if dashDistance.Valid {
			d.DashThrust = &units.DashThrust{
				DistanceMeters:   dashDistance.Float64,
				WindupSeconds:    dashWindup.Float64,
				RecoverySeconds:  dashRecovery.Float64,
				DamageMultiplier: dashDamageMult.Float64,
				CooldownSeconds:  dashCooldown.Float64,
			}
		}

		defs = append(defs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return defs, nil
}

// BlockPatch, PositionalBonusPatch, ... mirror the nested optional combat
// profiles on units.Definition (see internal/units). A nil group pointer on
// UnitStatsPatch means "this unit doesn't have this mechanic" and writes
// NULL to every column in the group; a non-nil group always writes concrete
// values for that group's columns (RecoverySeconds inside BlockPatch is the
// one field that's independently optional even when the group is present,
// matching block_recovery_seconds being nullable on its own).
type BlockPatch struct {
	MeleeDR         float64
	RangedDR        float64
	DrainPerSecond  float64
	RecoverySeconds *float64
}

type PositionalBonusPatch struct {
	StaminaCostReductionPct float64
	MinNearbyAllies         int
}

type OpportunistBowPatch struct {
	Damage          float64
	Range           float64
	CooldownSeconds float64
}

type RogueQuiverPatch struct {
	Damage                float64
	Range                 float64
	Charges               int
	RechargeSeconds       float64
	ExecuteMultiplier     float64
	ExecuteHpThresholdPct float64
}

type ReconPatch struct {
	ViewRadiusBonusPct    float64
	DetectionRadiusMeters float64
}

type FireArrowPatch struct {
	Damage                    float64
	StructureDamageMultiplier float64
	WoodCostPerShot           int
}

type DashThrustPatch struct {
	DistanceMeters   float64
	WindupSeconds    float64
	RecoverySeconds  float64
	DamageMultiplier float64
	CooldownSeconds  float64
}

// UnitStatsPatch is the full set of unit fields the dev-only Unit Viewer
// panel can edit (GET/PATCH /api/admin/units, see server.go) — every column
// on the `units` row except the identity ones (type_id, id, display_name,
// tier) and asset paths, which aren't balance knobs and would risk breaking
// unit lookup or sprite loading if edited from this form.
type UnitStatsPatch struct {
	HP                         float64
	PassiveDR                  float64
	MoveSpeed                  float64
	RangeType                  string
	Range                      float64
	Damage                     float64
	WindupSeconds              float64
	ActiveSeconds              float64
	RecoverySeconds            float64
	Stamina                    float64
	StaminaRegenPerSecond      float64
	SprintSpeedMultiplier      float64
	SprintStaminaCostPerSecond float64
	AnimationSpeed             float64
	CostWood                   int
	CostStone                  int
	CostIron                   int
	RequiresRoyalGuard         bool
	Cleave                     bool
	HasBraceStance             bool

	ComboSteps               *int
	ComboWindowSeconds       *float64
	AttackStaminaCost        *float64
	DrawHoldThresholdSeconds *float64
	DodgeCostMultiplier      *float64

	AntiShieldMultiplier        *float64
	AntiWoodStructureMultiplier *float64

	Block           *BlockPatch
	PositionalBonus *PositionalBonusPatch
	OpportunistBow  *OpportunistBowPatch
	RogueQuiver     *RogueQuiverPatch
	Recon           *ReconPatch
	FireArrow       *FireArrowPatch
	DashThrust      *DashThrustPatch
}

// UpdateUnitStats writes patch onto the unit row identified by typeID and
// publishes a reload notification (see PublishUnitsChanged/WatchUnits) so
// every connected server instance picks it up within moments — no restart.
// Already-spawned players keep the HP/stamina they spawned with; only new
// spawns (and everything computed per-tick: move speed, stamina regen, block
// drain, attack/combo timing) see the new values. It does not touch schema,
// seeding, id/display_name/tier, or asset paths — see UnitStatsPatch.
//
// Nil group/scalar pointers write NULL (pgx encodes a typed nil pointer as
// SQL NULL) — that's how a unit's optional mechanics (block, dash-thrust,
// combo, ...) get added or removed via this form, not just tuned.
func (s *Store) UpdateUnitStats(ctx context.Context, typeID uint8, patch UnitStatsPatch) error {
	var blockMeleeDR, blockRangedDR, blockDrainPerSecond, blockRecoverySeconds *float64
	if patch.Block != nil {
		blockMeleeDR = &patch.Block.MeleeDR
		blockRangedDR = &patch.Block.RangedDR
		blockDrainPerSecond = &patch.Block.DrainPerSecond
		blockRecoverySeconds = patch.Block.RecoverySeconds
	}

	var positionalStaminaCostReductionPct *float64
	var positionalMinNearbyAllies *int
	if patch.PositionalBonus != nil {
		positionalStaminaCostReductionPct = &patch.PositionalBonus.StaminaCostReductionPct
		positionalMinNearbyAllies = &patch.PositionalBonus.MinNearbyAllies
	}

	var opportunistBowDamage, opportunistBowRange, opportunistBowCooldown *float64
	if patch.OpportunistBow != nil {
		opportunistBowDamage = &patch.OpportunistBow.Damage
		opportunistBowRange = &patch.OpportunistBow.Range
		opportunistBowCooldown = &patch.OpportunistBow.CooldownSeconds
	}

	var rogueQuiverDamage, rogueQuiverRange, rogueQuiverRecharge *float64
	var rogueQuiverExecuteMult, rogueQuiverExecuteHPPct *float64
	var rogueQuiverCharges *int
	if patch.RogueQuiver != nil {
		rogueQuiverDamage = &patch.RogueQuiver.Damage
		rogueQuiverRange = &patch.RogueQuiver.Range
		rogueQuiverCharges = &patch.RogueQuiver.Charges
		rogueQuiverRecharge = &patch.RogueQuiver.RechargeSeconds
		rogueQuiverExecuteMult = &patch.RogueQuiver.ExecuteMultiplier
		rogueQuiverExecuteHPPct = &patch.RogueQuiver.ExecuteHpThresholdPct
	}

	var reconViewRadiusPct, reconDetectionRadius *float64
	if patch.Recon != nil {
		reconViewRadiusPct = &patch.Recon.ViewRadiusBonusPct
		reconDetectionRadius = &patch.Recon.DetectionRadiusMeters
	}

	var fireArrowDamage, fireArrowStructureMult *float64
	var fireArrowWoodCost *int
	if patch.FireArrow != nil {
		fireArrowDamage = &patch.FireArrow.Damage
		fireArrowStructureMult = &patch.FireArrow.StructureDamageMultiplier
		fireArrowWoodCost = &patch.FireArrow.WoodCostPerShot
	}

	var dashDistance, dashWindup, dashRecovery, dashDamageMult, dashCooldown *float64
	if patch.DashThrust != nil {
		dashDistance = &patch.DashThrust.DistanceMeters
		dashWindup = &patch.DashThrust.WindupSeconds
		dashRecovery = &patch.DashThrust.RecoverySeconds
		dashDamageMult = &patch.DashThrust.DamageMultiplier
		dashCooldown = &patch.DashThrust.CooldownSeconds
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE units SET
			hp = $2, passive_dr = $3, move_speed = $4, range_type = $5, range = $6, damage = $7,
			windup_seconds = $8, active_seconds = $9, recovery_seconds = $10,
			stamina = $11, stamina_regen_per_second = $12,
			sprint_speed_multiplier = $13, sprint_stamina_cost_per_second = $14,
			combo_steps = $15::smallint, combo_window_seconds = $16,
			animation_speed = $17, attack_stamina_cost = $18,
			draw_hold_threshold_seconds = $19, dodge_cost_multiplier = $20,
			cost_wood = $21, cost_stone = $22, cost_iron = $23,
			requires_royal_guard = $24, cleave = $25, has_brace_stance = $26,
			block_melee_dr = $27, block_ranged_dr = $28, block_drain_per_second = $29, block_recovery_seconds = $30,
			positional_stamina_cost_reduction_pct = $31, positional_min_nearby_allies = $32::smallint,
			opportunist_bow_damage = $33, opportunist_bow_range = $34, opportunist_bow_cooldown_seconds = $35,
			rogue_quiver_damage = $36, rogue_quiver_range = $37, rogue_quiver_charges = $38::smallint,
			rogue_quiver_recharge_seconds = $39,
			rogue_quiver_execute_multiplier = $40, rogue_quiver_execute_hp_threshold_pct = $41,
			recon_view_radius_bonus_pct = $42, recon_detection_radius_meters = $43,
			fire_arrow_damage = $44, fire_arrow_structure_damage_multiplier = $45,
			fire_arrow_wood_cost_per_shot = $46::smallint,
			dash_thrust_distance_meters = $47, dash_thrust_windup_seconds = $48,
			dash_thrust_recovery_seconds = $49, dash_thrust_damage_multiplier = $50,
			dash_thrust_cooldown_seconds = $51,
			anti_shield_multiplier = $52, anti_wood_structure_multiplier = $53
		WHERE type_id = $1
	`,
		typeID,
		patch.HP, patch.PassiveDR, patch.MoveSpeed, patch.RangeType, patch.Range, patch.Damage,
		patch.WindupSeconds, patch.ActiveSeconds, patch.RecoverySeconds,
		patch.Stamina, patch.StaminaRegenPerSecond,
		patch.SprintSpeedMultiplier, patch.SprintStaminaCostPerSecond,
		patch.ComboSteps, patch.ComboWindowSeconds,
		patch.AnimationSpeed, patch.AttackStaminaCost,
		patch.DrawHoldThresholdSeconds, patch.DodgeCostMultiplier,
		patch.CostWood, patch.CostStone, patch.CostIron,
		patch.RequiresRoyalGuard, patch.Cleave, patch.HasBraceStance,
		blockMeleeDR, blockRangedDR, blockDrainPerSecond, blockRecoverySeconds,
		positionalStaminaCostReductionPct, positionalMinNearbyAllies,
		opportunistBowDamage, opportunistBowRange, opportunistBowCooldown,
		rogueQuiverDamage, rogueQuiverRange, rogueQuiverCharges,
		rogueQuiverRecharge,
		rogueQuiverExecuteMult, rogueQuiverExecuteHPPct,
		reconViewRadiusPct, reconDetectionRadius,
		fireArrowDamage, fireArrowStructureMult,
		fireArrowWoodCost,
		dashDistance, dashWindup,
		dashRecovery, dashDamageMult,
		dashCooldown,
		patch.AntiShieldMultiplier, patch.AntiWoodStructureMultiplier,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUnitNotFound
	}
	return s.PublishUnitsChanged(ctx)
}

func nullFloatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}
