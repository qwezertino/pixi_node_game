import { MOVEMENT } from "../../shared/gameConfig";
import { TICK_RATE } from "../network/protocol/messages";
import type { UnitDefinition } from "../../shared/units";

// GDD §60 World Coordinate Resolution: converts a unit's real moveSpeed (m/s) into
// world units per tick, rounded to the nearest whole unit. This is a client-side
// dead-reckoning approximation only (see playerManager.ts deadReckon, for remote
// players between snapshots) — small drift here is smoothed away by the snapshot
// interpolator, unlike the local player's own prediction (see milliUnitsPerTick).
export function unitsPerTick(unit: UnitDefinition): number {
    return Math.round((unit.moveSpeed * MOVEMENT.unitsPerMeter) / TICK_RATE);
}

// Fixed-point per-tick movement rate mirroring the server's exact formula (world.go
// moveStat.milliUnitsPerTick — GDD §60), in 1/1000 world units. Whenever
// moveSpeed*unitsPerMeter isn't an exact multiple of TICK_RATE (the common case),
// this is a fractional number of units per tick; the server never rounds it away,
// it accumulates the remainder and flushes a whole unit once it crosses 1000 (see
// world.go updatePlayerPosition). Used by the local player's own prediction
// (movementController.ts), which must track the same remainder or its average
// speed permanently diverges from the server's and every ACK visibly snaps.
export function milliUnitsPerTick(unit: UnitDefinition): number {
    return Math.round((unit.moveSpeed * MOVEMENT.unitsPerMeter * 1000) / TICK_RATE);
}
