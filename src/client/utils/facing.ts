import type { Direction } from "./animationLayout";

/**
 * Splits the screen around the player into 4 equal 90° quadrants (up/down/left/right)
 * based on cursor position and returns which one the cursor falls in. Comparing
 * |dx| vs |dy| is equivalent to splitting on the two diagonals through the player,
 * so each of the 4 directions owns exactly a quarter of the circle around them.
 */
export function directionFromDelta(dx: number, dy: number): Direction {
    if (Math.abs(dx) >= Math.abs(dy)) {
        return dx >= 0 ? "right" : "left";
    }
    return dy >= 0 ? "down" : "up";
}
