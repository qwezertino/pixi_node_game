/**
 * Pure, Pixi-free geometry primitives shared by every collision layer.
 * Mirrors src/server/internal/collision/geometry.go — integer semantics,
 * same circle-vs-AABB formula — so client prediction, reconciliation and
 * remote dead reckoning agree with the server bit-for-bit.
 */

export const PLAYER_RADIUS = 4;
export const GRID_CELL_SIZE = 64;
export const MAX_MOVE_STEPS = 64;

export interface AABB {
    minX: number;
    minY: number;
    maxX: number;
    maxY: number;
}

export function aabbValid(box: AABB): boolean {
    return box.minX < box.maxX && box.minY < box.maxY;
}

export function expandAABB(box: AABB, r: number): AABB {
    return { minX: box.minX - r, minY: box.minY - r, maxX: box.maxX + r, maxY: box.maxY + r };
}

function clampInt(v: number, lo: number, hi: number): number {
    if (v < lo) return lo;
    if (v > hi) return hi;
    return v;
}

/**
 * Reports whether a circle centered at (x, y) with the given radius
 * overlaps box. Touching the boundary counts as contact, not overlap:
 * blocked only when the squared distance is strictly less than radius^2.
 * All inputs must be integers within the safe integer range (world
 * coordinates and radii are always small enough for this to hold).
 */
export function circleOverlapsAABB(x: number, y: number, radius: number, box: AABB): boolean {
    const closestX = clampInt(x, box.minX, box.maxX);
    const closestY = clampInt(y, box.minY, box.maxY);
    const dx = x - closestX;
    const dy = y - closestY;
    return dx * dx + dy * dy < radius * radius;
}
