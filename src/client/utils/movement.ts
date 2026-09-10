import { MOVEMENT } from "../../shared/gameConfig";
import { TICK_RATE } from "../network/protocol/messages";
import type { UnitDefinition } from "../../shared/units";

export function unitsPerTick(unit: UnitDefinition): number {
    return Math.round((unit.moveSpeed * MOVEMENT.unitsPerMeter) / TICK_RATE);
}

export function milliUnitsPerTick(unit: UnitDefinition): number {
    return Math.round((unit.moveSpeed * MOVEMENT.unitsPerMeter * 1000) / TICK_RATE);
}

export function diagonalStep(step: number): number {
    return Math.round(step * Math.SQRT1_2);
}

export function milliRatePerTick(unit: UnitDefinition, sprinting: boolean, diagonal: boolean): number {
    const base = milliUnitsPerTick(unit);
    let multiplier = 1;
    if (sprinting) multiplier *= unit.sprintSpeedMultiplier;
    if (diagonal) multiplier *= Math.SQRT1_2;
    return multiplier === 1 ? base : Math.round(base * multiplier);
}

export function integrateRemainder(
    remainderMilli: number,
    milliRate: number,
    elapsedTicks: number
): { distance: number; remainder: number } {
    const total = remainderMilli + milliRate * elapsedTicks;
    const distance = Math.floor(total / 1000);
    return { distance, remainder: total - distance * 1000 };
}
