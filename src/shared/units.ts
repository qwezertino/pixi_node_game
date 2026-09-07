
import unitsData from "./units.json";

export type UnitTier = "citizen" | "guard" | "archer" | "warrior" | "knight";
export type DamageRangeType = "melee" | "ranged";

export interface UnitCost {
    wood: number;
    stone: number;
    iron: number;
}

export interface BlockProfile {
    meleeDR: number;
    rangedDR: number;
    drainPerSecond: number;
    recoverySeconds?: number;
}

export interface PositionalBonus {
    staminaCostReductionPct: number;
    minNearbyAllies: number;
}

export interface OpportunistBow {
    damage: number;
    range: number;
    cooldownSeconds: number;
}

export interface RogueQuiver {
    damage: number;
    range: number;
    charges: number;
    rechargeSeconds: number;
    executeMultiplier: number;
    executeHpThresholdPct: number;
}

export interface Recon {
    viewRadiusBonusPct: number;
    detectionRadiusMeters: number;
}

export interface FireArrow {
    damage: number;
    structureDamageMultiplier: number;
    woodCostPerShot: number;
}

export interface DashThrust {
    distanceMeters: number;
    windupSeconds: number;
    recoverySeconds: number;
    damageMultiplier: number;
    cooldownSeconds: number;
}

export interface UnitDefinition {
    /** Stable numeric id used on the wire (WELCOME / unit-roster messages). */
    typeId: number;
    /** Stable string key used in code/config/URLs. */
    id: string;
    displayName: string;
    tier: UnitTier;

    hp: number;
    /** Passive damage reduction (0..1), always active. Only Heavy Knight/Paladin have this. */
    passiveDR: number;
    /** m/s */
    moveSpeed: number;
    rangeType: DamageRangeType;
    /** meters */
    range: number;
    damage: number;
    windupSeconds: number;
    activeSeconds: number;
    recoverySeconds: number;

    stamina: number;
    staminaRegenPerSecond: number;
    /** Sprint (GDD §54): move-speed multiplier while holding Shift and moving. */
    sprintSpeedMultiplier: number;
    /** Sprint (GDD §57): stamina drained per second while sprinting. */
    sprintStaminaCostPerSecond: number;
    /** AnimatedSprite.animationSpeed (frames/tick) for every animation this unit plays. */
    animationSpeed: number;
    /** Attack combo chain length (undefined/1 = no combo, every swing re-attacks). */
    comboSteps?: number;
    /** How long past a swing's own duration a new swing still continues the combo
     * chain instead of resetting to step 1 — see server world.go executeAttack. */
    comboWindowSeconds?: number;
    /** Flat stamina cost per swing/thrust, where the doc specifies one. */
    attackStaminaCost?: number;
    /** Archer-only: draw-and-hold only costs stamina past this many seconds. */
    drawHoldThresholdSeconds?: number;
    dodgeCostMultiplier?: number;

    cost: UnitCost;
    requiresRoyalGuard?: boolean;
    cleave?: boolean;
    hasBraceStance?: boolean;

    block?: BlockProfile;
    positionalBonus?: PositionalBonus;
    opportunistBow?: OpportunistBow;
    rogueQuiver?: RogueQuiver;
    recon?: Recon;
    fireArrow?: FireArrow;
    dashThrust?: DashThrust;
    antiShieldMultiplier?: number;
    antiWoodStructureMultiplier?: number;

    /** Reference to public/assets/<path> — not yet wired into the sprite loader. */
    assetPath?: string;
    combatAssetPath?: string;
    dashAssetPath?: string;
}

export const UNITS: readonly UnitDefinition[] = unitsData as UnitDefinition[];

const BY_ID = new Map<string, UnitDefinition>(UNITS.map((u) => [u.id, u]));
const BY_TYPE_ID = new Map<number, UnitDefinition>(UNITS.map((u) => [u.typeId, u]));

export type UnitType = (typeof UNITS)[number]["id"];

export const DEFAULT_UNIT_TYPE: UnitType = "spearman";

export function isValidUnitType(id: string): id is UnitType {
    return BY_ID.has(id);
}

export function getUnitDefinition(id: string): UnitDefinition {
    return BY_ID.get(id) ?? BY_ID.get(DEFAULT_UNIT_TYPE)!;
}

export function getUnitDefinitionByTypeId(typeId: number): UnitDefinition {
    return BY_TYPE_ID.get(typeId) ?? BY_ID.get(DEFAULT_UNIT_TYPE)!;
}
