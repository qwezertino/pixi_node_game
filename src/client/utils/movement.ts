import { MOVEMENT } from "../../shared/gameConfig";
import { TICK_RATE } from "../network/protocol/messages";
import type { UnitDefinition } from "../../shared/units";

export function unitsPerTick(unit: UnitDefinition): number {
    return Math.round((unit.moveSpeed * MOVEMENT.unitsPerMeter) / TICK_RATE);
}

export function milliUnitsPerTick(unit: UnitDefinition): number {
    return Math.round((unit.moveSpeed * MOVEMENT.unitsPerMeter * 1000) / TICK_RATE);
}
