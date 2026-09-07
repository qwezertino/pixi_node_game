package units

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed units.json
var embeddedUnits []byte

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

var (
	all      []Definition
	byID     map[string]Definition
	byTypeID map[uint8]Definition
)

const DefaultUnitType = "spearman"

func init() {
	if err := json.Unmarshal(embeddedUnits, &all); err != nil {
		panic(fmt.Errorf("units: failed to parse embedded units.json: %w", err))
	}
	byID = make(map[string]Definition, len(all))
	byTypeID = make(map[uint8]Definition, len(all))
	for _, u := range all {
		byID[u.ID] = u
		byTypeID[u.TypeID] = u
	}
	if _, ok := byID[DefaultUnitType]; !ok {
		panic(fmt.Errorf("units: DefaultUnitType %q not found in units.json", DefaultUnitType))
	}
}

func All() []Definition {
	return all
}

func Get(id string) Definition {
	if u, ok := byID[id]; ok {
		return u
	}
	return byID[DefaultUnitType]
}

func GetByTypeID(typeID uint8) Definition {
	if u, ok := byTypeID[typeID]; ok {
		return u
	}
	return byID[DefaultUnitType]
}

func IsValid(id string) bool {
	_, ok := byID[id]
	return ok
}
