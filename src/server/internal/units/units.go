package units

import (
	"fmt"
	"sync/atomic"
)

type Cost struct {
	Wood  int `json:"wood"`
	Stone int `json:"stone"`
	Iron  int `json:"iron"`
}

type BlockProfile struct {
	MeleeDR         float64  `json:"meleeDR"`
	RangedDR        float64  `json:"rangedDR"`
	DrainPerSecond  float64  `json:"drainPerSecond"`
	RecoverySeconds *float64 `json:"recoverySeconds,omitempty"`
}

type PositionalBonus struct {
	StaminaCostReductionPct float64 `json:"staminaCostReductionPct"`
	MinNearbyAllies         int     `json:"minNearbyAllies"`
}

type OpportunistBow struct {
	Damage          float64 `json:"damage"`
	Range           float64 `json:"range"`
	CooldownSeconds float64 `json:"cooldownSeconds"`
}

type RogueQuiver struct {
	Damage                float64 `json:"damage"`
	Range                 float64 `json:"range"`
	Charges               int     `json:"charges"`
	RechargeSeconds       float64 `json:"rechargeSeconds"`
	ExecuteMultiplier     float64 `json:"executeMultiplier"`
	ExecuteHpThresholdPct float64 `json:"executeHpThresholdPct"`
}

type Recon struct {
	ViewRadiusBonusPct    float64 `json:"viewRadiusBonusPct"`
	DetectionRadiusMeters float64 `json:"detectionRadiusMeters"`
}

type FireArrow struct {
	Damage                    float64 `json:"damage"`
	StructureDamageMultiplier float64 `json:"structureDamageMultiplier"`
	WoodCostPerShot           int     `json:"woodCostPerShot"`
}

type DashThrust struct {
	DistanceMeters   float64 `json:"distanceMeters"`
	WindupSeconds    float64 `json:"windupSeconds"`
	RecoverySeconds  float64 `json:"recoverySeconds"`
	DamageMultiplier float64 `json:"damageMultiplier"`
	CooldownSeconds  float64 `json:"cooldownSeconds"`
}

// Definition is a unit's full stat block. json tags are used both ways: for
// scanning out of the `units` Postgres table (see internal/liveconfig) and
// for serving GET /api/units to the TS client, which needs the exact same
// shape it used to get from the bundled units.json.
type Definition struct {
	TypeID      uint8  `json:"typeId"`
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Tier        string `json:"tier"`

	HP              float64 `json:"hp"`
	PassiveDR       float64 `json:"passiveDR"`
	MoveSpeed       float64 `json:"moveSpeed"`
	RangeType       string  `json:"rangeType"`
	Range           float64 `json:"range"`
	Damage          float64 `json:"damage"`
	WindupSeconds   float64 `json:"windupSeconds"`
	ActiveSeconds   float64 `json:"activeSeconds"`
	RecoverySeconds float64 `json:"recoverySeconds"`

	Stamina                    float64 `json:"stamina"`
	StaminaRegenPerSecond      float64 `json:"staminaRegenPerSecond"`
	SprintSpeedMultiplier      float64 `json:"sprintSpeedMultiplier"`
	SprintStaminaCostPerSecond float64 `json:"sprintStaminaCostPerSecond"`

	ComboSteps         int     `json:"comboSteps,omitempty"`
	ComboWindowSeconds float64 `json:"comboWindowSeconds,omitempty"`

	AnimationSpeed           float64  `json:"animationSpeed"`
	AttackStaminaCost        *float64 `json:"attackStaminaCost,omitempty"`
	DrawHoldThresholdSeconds *float64 `json:"drawHoldThresholdSeconds,omitempty"`
	DodgeCostMultiplier      *float64 `json:"dodgeCostMultiplier,omitempty"`

	Cost               Cost `json:"cost"`
	RequiresRoyalGuard bool `json:"requiresRoyalGuard,omitempty"`
	Cleave             bool `json:"cleave,omitempty"`
	HasBraceStance     bool `json:"hasBraceStance,omitempty"`

	Block                       *BlockProfile    `json:"block,omitempty"`
	PositionalBonus             *PositionalBonus `json:"positionalBonus,omitempty"`
	OpportunistBow              *OpportunistBow  `json:"opportunistBow,omitempty"`
	RogueQuiver                 *RogueQuiver     `json:"rogueQuiver,omitempty"`
	Recon                       *Recon           `json:"recon,omitempty"`
	FireArrow                   *FireArrow       `json:"fireArrow,omitempty"`
	DashThrust                  *DashThrust      `json:"dashThrust,omitempty"`
	AntiShieldMultiplier        *float64         `json:"antiShieldMultiplier,omitempty"`
	AntiWoodStructureMultiplier *float64         `json:"antiWoodStructureMultiplier,omitempty"`

	AssetPath       string `json:"assetPath,omitempty"`
	CombatAssetPath string `json:"combatAssetPath,omitempty"`
	DashAssetPath   string `json:"dashAssetPath,omitempty"`
}

const DefaultUnitType = "spearman"

// state bundles the three derived lookups together so a reload can publish
// them as one atomic pointer swap — readers never see a byID built from one
// generation of defs paired with a byTypeID from another.
type state struct {
	all      []Definition
	byID     map[string]Definition
	byTypeID map[uint8]Definition
}

var current atomic.Pointer[state]

func init() {
	// Empty-but-non-nil default so All/Get/GetByTypeID/IsValid are safe to
	// call before main.go's first LoadDefinitions (e.g. in tests that build
	// a GameWorld without ever loading real unit data).
	current.Store(&state{byID: map[string]Definition{}, byTypeID: map[uint8]Definition{}})
}

// LoadDefinitions replaces the active unit definitions with defs — at
// startup from main.go, and again any time the dev-only unit admin API
// changes a row (see internal/liveconfig's unit watcher and
// internal/server/admin.go). There is no embedded/file fallback: Postgres's
// `units` table is authoritative. Safe to call concurrently with All/Get/
// GetByTypeID/IsValid — it's a single atomic pointer swap, not a mutation of
// shared maps.
func LoadDefinitions(defs []Definition) error {
	newByID := make(map[string]Definition, len(defs))
	newByTypeID := make(map[uint8]Definition, len(defs))
	for _, u := range defs {
		newByID[u.ID] = u
		newByTypeID[u.TypeID] = u
	}
	if _, ok := newByID[DefaultUnitType]; !ok {
		return fmt.Errorf("units: DefaultUnitType %q not found in loaded definitions", DefaultUnitType)
	}
	current.Store(&state{all: defs, byID: newByID, byTypeID: newByTypeID})
	return nil
}

func All() []Definition {
	return current.Load().all
}

func Get(id string) Definition {
	s := current.Load()
	if u, ok := s.byID[id]; ok {
		return u
	}
	return s.byID[DefaultUnitType]
}

func GetByTypeID(typeID uint8) Definition {
	s := current.Load()
	if u, ok := s.byTypeID[typeID]; ok {
		return u
	}
	return s.byID[DefaultUnitType]
}

func IsValid(id string) bool {
	_, ok := current.Load().byID[id]
	return ok
}
