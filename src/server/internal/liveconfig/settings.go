package liveconfig

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/units"
)

var ErrUnitNotFound = errors.New("unit not found")
var ErrInvalidUnit = errors.New("invalid unit")

func loadGameSettings(ctx context.Context, db dbQuerier) (*config.GameSettings, error) {
	var gs config.GameSettings
	err := db.QueryRow(ctx, `
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

func (s *Store) LoadGameSettings(ctx context.Context) (*config.GameSettings, error) {
	return loadGameSettings(ctx, s.pool)
}

const unitColumns = `
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
			asset_path, combat_asset_path, dash_asset_path`

type rowScanFunc func(dest ...any) error

// unitRow is a unit definition plus the raw nullable combo columns, which
// units.Definition cannot represent (it stores ComboSteps/ComboWindowSeconds
// as plain zero-valued fields, not pointers).
type unitRow struct {
	def                units.Definition
	comboSteps         *int
	comboWindowSeconds *float64
}

func scanUnitRow(scan rowScanFunc) (unitRow, error) {
	var row unitRow
	d := &row.def
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

	err := scan(
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
		return unitRow{}, err
	}

	row.comboSteps = nullIntPtr(comboSteps)
	row.comboWindowSeconds = nullFloatPtr(comboWindowSeconds)
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

	return row, nil
}

func (s *Store) LoadUnitDefinitions(ctx context.Context) ([]units.Definition, error) {
	rows, err := s.pool.Query(ctx, `SELECT`+unitColumns+` FROM units ORDER BY type_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var defs []units.Definition
	for rows.Next() {
		row, err := scanUnitRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		defs = append(defs, row.def)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return defs, nil
}

// getUnitRowForUpdate reads a unit row inside tx, locking it until tx ends so
// concurrent UpdateUnitStats calls for the same type_id serialize instead of
// racing a read-merge-write against each other.
func getUnitRowForUpdate(ctx context.Context, tx pgx.Tx, typeID uint8) (unitRow, error) {
	row := tx.QueryRow(ctx, `SELECT`+unitColumns+` FROM units WHERE type_id = $1 FOR UPDATE`, typeID)
	return scanUnitRow(row.Scan)
}

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

// NullableField represents a PATCH field that a request may leave untouched
// (Present false), explicitly clear (Present true, Value nil), or set to a
// new value (Present true, Value non-nil).
type NullableField[T any] struct {
	Present bool
	Value   *T
}

// Set builds a NullableField that assigns value.
func Set[T any](value T) NullableField[T] {
	return NullableField[T]{Present: true, Value: &value}
}

// Clear builds a NullableField that explicitly clears the field.
func Clear[T any]() NullableField[T] {
	return NullableField[T]{Present: true, Value: nil}
}

type UnitStatsPatch struct {
	HP                         *float64
	PassiveDR                  *float64
	MoveSpeed                  *float64
	RangeType                  *string
	Range                      *float64
	Damage                     *float64
	WindupSeconds              *float64
	ActiveSeconds              *float64
	RecoverySeconds            *float64
	Stamina                    *float64
	StaminaRegenPerSecond      *float64
	SprintSpeedMultiplier      *float64
	SprintStaminaCostPerSecond *float64
	AnimationSpeed             *float64
	CostWood                   *int
	CostStone                  *int
	CostIron                   *int
	RequiresRoyalGuard         *bool
	Cleave                     *bool
	HasBraceStance             *bool

	ComboSteps               NullableField[int]
	ComboWindowSeconds       NullableField[float64]
	AttackStaminaCost        NullableField[float64]
	DrawHoldThresholdSeconds NullableField[float64]
	DodgeCostMultiplier      NullableField[float64]

	AntiShieldMultiplier        NullableField[float64]
	AntiWoodStructureMultiplier NullableField[float64]

	Block           NullableField[BlockPatch]
	PositionalBonus NullableField[PositionalBonusPatch]
	OpportunistBow  NullableField[OpportunistBowPatch]
	RogueQuiver     NullableField[RogueQuiverPatch]
	Recon           NullableField[ReconPatch]
	FireArrow       NullableField[FireArrowPatch]
	DashThrust      NullableField[DashThrustPatch]
}

// mergeUnitStatsPatch applies patch onto current, leaving every field the
// patch did not set untouched. Struct-shaped sub-abilities (Block,
// PositionalBonus, ...) are replaced wholesale when the patch provides them.
func mergeUnitStatsPatch(current unitRow, patch UnitStatsPatch) unitRow {
	merged := current
	d := &merged.def

	if patch.HP != nil {
		d.HP = *patch.HP
	}
	if patch.PassiveDR != nil {
		d.PassiveDR = *patch.PassiveDR
	}
	if patch.MoveSpeed != nil {
		d.MoveSpeed = *patch.MoveSpeed
	}
	if patch.RangeType != nil {
		d.RangeType = *patch.RangeType
	}
	if patch.Range != nil {
		d.Range = *patch.Range
	}
	if patch.Damage != nil {
		d.Damage = *patch.Damage
	}
	if patch.WindupSeconds != nil {
		d.WindupSeconds = *patch.WindupSeconds
	}
	if patch.ActiveSeconds != nil {
		d.ActiveSeconds = *patch.ActiveSeconds
	}
	if patch.RecoverySeconds != nil {
		d.RecoverySeconds = *patch.RecoverySeconds
	}
	if patch.Stamina != nil {
		d.Stamina = *patch.Stamina
	}
	if patch.StaminaRegenPerSecond != nil {
		d.StaminaRegenPerSecond = *patch.StaminaRegenPerSecond
	}
	if patch.SprintSpeedMultiplier != nil {
		d.SprintSpeedMultiplier = *patch.SprintSpeedMultiplier
	}
	if patch.SprintStaminaCostPerSecond != nil {
		d.SprintStaminaCostPerSecond = *patch.SprintStaminaCostPerSecond
	}
	if patch.AnimationSpeed != nil {
		d.AnimationSpeed = *patch.AnimationSpeed
	}
	if patch.CostWood != nil {
		d.Cost.Wood = *patch.CostWood
	}
	if patch.CostStone != nil {
		d.Cost.Stone = *patch.CostStone
	}
	if patch.CostIron != nil {
		d.Cost.Iron = *patch.CostIron
	}
	if patch.RequiresRoyalGuard != nil {
		d.RequiresRoyalGuard = *patch.RequiresRoyalGuard
	}
	if patch.Cleave != nil {
		d.Cleave = *patch.Cleave
	}
	if patch.HasBraceStance != nil {
		d.HasBraceStance = *patch.HasBraceStance
	}

	if patch.ComboSteps.Present {
		merged.comboSteps = patch.ComboSteps.Value
	}
	if patch.ComboWindowSeconds.Present {
		merged.comboWindowSeconds = patch.ComboWindowSeconds.Value
	}
	if merged.comboSteps != nil {
		d.ComboSteps = *merged.comboSteps
	} else {
		d.ComboSteps = 0
	}
	if merged.comboWindowSeconds != nil {
		d.ComboWindowSeconds = *merged.comboWindowSeconds
	} else {
		d.ComboWindowSeconds = 0
	}

	if patch.AttackStaminaCost.Present {
		d.AttackStaminaCost = patch.AttackStaminaCost.Value
	}
	if patch.DrawHoldThresholdSeconds.Present {
		d.DrawHoldThresholdSeconds = patch.DrawHoldThresholdSeconds.Value
	}
	if patch.DodgeCostMultiplier.Present {
		d.DodgeCostMultiplier = patch.DodgeCostMultiplier.Value
	}
	if patch.AntiShieldMultiplier.Present {
		d.AntiShieldMultiplier = patch.AntiShieldMultiplier.Value
	}
	if patch.AntiWoodStructureMultiplier.Present {
		d.AntiWoodStructureMultiplier = patch.AntiWoodStructureMultiplier.Value
	}

	if patch.Block.Present {
		if patch.Block.Value == nil {
			d.Block = nil
		} else {
			d.Block = &units.BlockProfile{
				MeleeDR: patch.Block.Value.MeleeDR, RangedDR: patch.Block.Value.RangedDR,
				DrainPerSecond: patch.Block.Value.DrainPerSecond, RecoverySeconds: patch.Block.Value.RecoverySeconds,
			}
		}
	}
	if patch.PositionalBonus.Present {
		if patch.PositionalBonus.Value == nil {
			d.PositionalBonus = nil
		} else {
			d.PositionalBonus = &units.PositionalBonus{
				StaminaCostReductionPct: patch.PositionalBonus.Value.StaminaCostReductionPct,
				MinNearbyAllies:         patch.PositionalBonus.Value.MinNearbyAllies,
			}
		}
	}
	if patch.OpportunistBow.Present {
		if patch.OpportunistBow.Value == nil {
			d.OpportunistBow = nil
		} else {
			d.OpportunistBow = &units.OpportunistBow{
				Damage: patch.OpportunistBow.Value.Damage, Range: patch.OpportunistBow.Value.Range,
				CooldownSeconds: patch.OpportunistBow.Value.CooldownSeconds,
			}
		}
	}
	if patch.RogueQuiver.Present {
		if patch.RogueQuiver.Value == nil {
			d.RogueQuiver = nil
		} else {
			d.RogueQuiver = &units.RogueQuiver{
				Damage: patch.RogueQuiver.Value.Damage, Range: patch.RogueQuiver.Value.Range,
				Charges: patch.RogueQuiver.Value.Charges, RechargeSeconds: patch.RogueQuiver.Value.RechargeSeconds,
				ExecuteMultiplier: patch.RogueQuiver.Value.ExecuteMultiplier, ExecuteHpThresholdPct: patch.RogueQuiver.Value.ExecuteHpThresholdPct,
			}
		}
	}
	if patch.Recon.Present {
		if patch.Recon.Value == nil {
			d.Recon = nil
		} else {
			d.Recon = &units.Recon{
				ViewRadiusBonusPct: patch.Recon.Value.ViewRadiusBonusPct, DetectionRadiusMeters: patch.Recon.Value.DetectionRadiusMeters,
			}
		}
	}
	if patch.FireArrow.Present {
		if patch.FireArrow.Value == nil {
			d.FireArrow = nil
		} else {
			d.FireArrow = &units.FireArrow{
				Damage: patch.FireArrow.Value.Damage, StructureDamageMultiplier: patch.FireArrow.Value.StructureDamageMultiplier,
				WoodCostPerShot: patch.FireArrow.Value.WoodCostPerShot,
			}
		}
	}
	if patch.DashThrust.Present && patch.DashThrust.Value == nil {
		d.DashThrust = nil
	} else if patch.DashThrust.Present {
		d.DashThrust = &units.DashThrust{
			DistanceMeters: patch.DashThrust.Value.DistanceMeters, WindupSeconds: patch.DashThrust.Value.WindupSeconds,
			RecoverySeconds: patch.DashThrust.Value.RecoverySeconds, DamageMultiplier: patch.DashThrust.Value.DamageMultiplier,
			CooldownSeconds: patch.DashThrust.Value.CooldownSeconds,
		}
	}

	return merged
}

func (s *Store) UpdateUnitStats(ctx context.Context, typeID uint8, patch UnitStatsPatch) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	current, err := getUnitRowForUpdate(ctx, tx, typeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnitNotFound
		}
		return err
	}

	merged := mergeUnitStatsPatch(current, patch)
	d := merged.def
	if err := units.ValidateDefinition(d); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidUnit, err)
	}

	var blockMeleeDR, blockRangedDR, blockDrainPerSecond, blockRecoverySeconds *float64
	if d.Block != nil {
		blockMeleeDR = &d.Block.MeleeDR
		blockRangedDR = &d.Block.RangedDR
		blockDrainPerSecond = &d.Block.DrainPerSecond
		blockRecoverySeconds = d.Block.RecoverySeconds
	}

	var positionalStaminaCostReductionPct *float64
	var positionalMinNearbyAllies *int
	if d.PositionalBonus != nil {
		positionalStaminaCostReductionPct = &d.PositionalBonus.StaminaCostReductionPct
		positionalMinNearbyAllies = &d.PositionalBonus.MinNearbyAllies
	}

	var opportunistBowDamage, opportunistBowRange, opportunistBowCooldown *float64
	if d.OpportunistBow != nil {
		opportunistBowDamage = &d.OpportunistBow.Damage
		opportunistBowRange = &d.OpportunistBow.Range
		opportunistBowCooldown = &d.OpportunistBow.CooldownSeconds
	}

	var rogueQuiverDamage, rogueQuiverRange, rogueQuiverRecharge *float64
	var rogueQuiverExecuteMult, rogueQuiverExecuteHPPct *float64
	var rogueQuiverCharges *int
	if d.RogueQuiver != nil {
		rogueQuiverDamage = &d.RogueQuiver.Damage
		rogueQuiverRange = &d.RogueQuiver.Range
		rogueQuiverCharges = &d.RogueQuiver.Charges
		rogueQuiverRecharge = &d.RogueQuiver.RechargeSeconds
		rogueQuiverExecuteMult = &d.RogueQuiver.ExecuteMultiplier
		rogueQuiverExecuteHPPct = &d.RogueQuiver.ExecuteHpThresholdPct
	}

	var reconViewRadiusPct, reconDetectionRadius *float64
	if d.Recon != nil {
		reconViewRadiusPct = &d.Recon.ViewRadiusBonusPct
		reconDetectionRadius = &d.Recon.DetectionRadiusMeters
	}

	var fireArrowDamage, fireArrowStructureMult *float64
	var fireArrowWoodCost *int
	if d.FireArrow != nil {
		fireArrowDamage = &d.FireArrow.Damage
		fireArrowStructureMult = &d.FireArrow.StructureDamageMultiplier
		fireArrowWoodCost = &d.FireArrow.WoodCostPerShot
	}

	var dashDistance, dashWindup, dashRecovery, dashDamageMult, dashCooldown *float64
	if d.DashThrust != nil {
		dashDistance = &d.DashThrust.DistanceMeters
		dashWindup = &d.DashThrust.WindupSeconds
		dashRecovery = &d.DashThrust.RecoverySeconds
		dashDamageMult = &d.DashThrust.DamageMultiplier
		dashCooldown = &d.DashThrust.CooldownSeconds
	}

	tag, err := tx.Exec(ctx, `
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
		d.HP, d.PassiveDR, d.MoveSpeed, d.RangeType, d.Range, d.Damage,
		d.WindupSeconds, d.ActiveSeconds, d.RecoverySeconds,
		d.Stamina, d.StaminaRegenPerSecond,
		d.SprintSpeedMultiplier, d.SprintStaminaCostPerSecond,
		merged.comboSteps, merged.comboWindowSeconds,
		d.AnimationSpeed, d.AttackStaminaCost,
		d.DrawHoldThresholdSeconds, d.DodgeCostMultiplier,
		d.Cost.Wood, d.Cost.Stone, d.Cost.Iron,
		d.RequiresRoyalGuard, d.Cleave, d.HasBraceStance,
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
		d.AntiShieldMultiplier, d.AntiWoodStructureMultiplier,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUnitNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.PublishUnitsChanged(ctx)
}

func nullIntPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func nullFloatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}
