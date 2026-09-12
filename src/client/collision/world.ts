/**
 * Discrete swept movement solver, mirroring
 * src/server/internal/collision/world.go MoveCircle/FindNearestFree bit for
 * bit: same broad-phase-once, narrow-phase-per-micro-step algorithm, same
 * X-then-Y axis order, so client prediction/reconciliation and remote dead
 * reckoning reproduce the server's authoritative result exactly.
 */

import { circleOverlapsAABB, expandAABB, MAX_MOVE_STEPS, PLAYER_RADIUS, type AABB } from "./geometry";
import { MapStaticGrid, QueryScratch } from "./grid";
import { StructureGrid } from "./structureGrid";

export interface MoveResult {
    x: number;
    y: number;
    effectiveDX: number;
    effectiveDY: number;
    blockedX: boolean;
    blockedY: boolean;
}

export class MoveScratch {
    mapScratch = new QueryScratch();
    structureScratch = new QueryScratch();
}

export class CollisionWorld {
    constructor(
        private readonly worldWidth: number,
        private readonly worldHeight: number,
        private readonly mapStatic: MapStaticGrid,
        private readonly structures: StructureGrid,
    ) {}

    /**
     * Resolves movement of a PLAYER_RADIUS circle from (x, y) along
     * (dx, dy) for `distance` world units using discrete swept movement:
     * one broad-phase query for the whole path, then a 1-world-unit
     * narrow-phase step repeated `distance` times.
     *
     * distance must be in [0, MAX_MOVE_STEPS]; distance > MAX_MOVE_STEPS is
     * treated as "no movement this call" (an invariant violation upstream),
     * never clamped. distance < 0 throws.
     */
    moveCircle(x: number, y: number, dx: number, dy: number, distance: number, scratch: MoveScratch): MoveResult {
        if (distance < 0) throw new Error("moveCircle distance must be >= 0");
        if (distance > MAX_MOVE_STEPS) {
            return { x, y, effectiveDX: 0, effectiveDY: 0, blockedX: dx !== 0, blockedY: dy !== 0 };
        }

        const intendedX = x + dx * distance;
        const intendedY = y + dy * distance;
        const bounds = expandAABB(
            {
                minX: Math.min(x, intendedX),
                minY: Math.min(y, intendedY),
                maxX: Math.max(x, intendedX),
                maxY: Math.max(y, intendedY),
            },
            PLAYER_RADIUS,
        );

        const mapCandidates = this.mapStatic.queryAABB(bounds, scratch.mapScratch);
        const structureCandidates = this.structures.queryAABB(bounds, scratch.structureScratch);

        const minCenterX = PLAYER_RADIUS;
        const maxCenterX = this.worldWidth - PLAYER_RADIUS;
        const minCenterY = PLAYER_RADIUS;
        const maxCenterY = this.worldHeight - PLAYER_RADIUS;

        const free = (cx: number, cy: number): boolean => {
            if (cx < minCenterX || cx > maxCenterX || cy < minCenterY || cy > maxCenterY) return false;
            for (const idx of mapCandidates) {
                const c = this.mapStatic.collider(idx);
                if (circleOverlapsAABB(cx, cy, PLAYER_RADIUS, c)) return false;
            }
            for (const idx of structureCandidates) {
                const c = this.structures.collider(idx);
                if (circleOverlapsAABB(cx, cy, PLAYER_RADIUS, c)) return false;
            }
            return true;
        };

        let curX = x;
        let curY = y;
        let lastMovedX = false;
        let lastMovedY = false;

        for (let step = 0; step < distance; step++) {
            let movedX = false;
            let movedY = false;

            if (dx !== 0) {
                const candidateX = curX + dx;
                if (free(candidateX, curY)) {
                    curX = candidateX;
                    movedX = true;
                }
            }
            if (dy !== 0) {
                const candidateY = curY + dy;
                if (free(curX, candidateY)) {
                    curY = candidateY;
                    movedY = true;
                }
            }

            lastMovedX = movedX;
            lastMovedY = movedY;
            if (!movedX && !movedY) break;
        }

        const effectiveDX = lastMovedX ? dx : 0;
        const effectiveDY = lastMovedY ? dy : 0;

        return {
            x: curX,
            y: curY,
            effectiveDX,
            effectiveDY,
            blockedX: dx !== 0 && effectiveDX === 0,
            blockedY: dy !== 0 && effectiveDY === 0,
        };
    }

    /**
     * Deterministic expanding-ring search for the nearest integer position
     * (starting at the origin) where a circle of the given radius is free
     * of map/structure colliders and inside world bounds.
     */
    findNearestFree(x: number, y: number, radius: number, searchLimit: number, scratch: MoveScratch): { x: number; y: number } | null {
        const bounds: AABB = expandAABB(
            { minX: x - searchLimit, minY: y - searchLimit, maxX: x + searchLimit, maxY: y + searchLimit },
            radius,
        );
        const mapCandidates = this.mapStatic.queryAABB(bounds, scratch.mapScratch);
        const structureCandidates = this.structures.queryAABB(bounds, scratch.structureScratch);

        const minCenterX = radius;
        const maxCenterX = this.worldWidth - radius;
        const minCenterY = radius;
        const maxCenterY = this.worldHeight - radius;

        const free = (cx: number, cy: number): boolean => {
            if (cx < minCenterX || cx > maxCenterX || cy < minCenterY || cy > maxCenterY) return false;
            for (const idx of mapCandidates) {
                if (circleOverlapsAABB(cx, cy, radius, this.mapStatic.collider(idx))) return false;
            }
            for (const idx of structureCandidates) {
                if (circleOverlapsAABB(cx, cy, radius, this.structures.collider(idx))) return false;
            }
            return true;
        };

        if (free(x, y)) return { x, y };

        for (let r = 1; r <= searchLimit; r++) {
            for (let cx = x - r; cx <= x + r; cx++) {
                if (free(cx, y - r)) return { x: cx, y: y - r };
            }
            for (let cy = y - r + 1; cy <= y + r; cy++) {
                if (free(x + r, cy)) return { x: x + r, y: cy };
            }
            for (let cx = x + r - 1; cx >= x - r; cx--) {
                if (free(cx, y + r)) return { x: cx, y: y + r };
            }
            for (let cy = y + r - 1; cy >= y - r + 1; cy--) {
                if (free(x - r, cy)) return { x: x - r, y: cy };
            }
        }
        return null;
    }
}
