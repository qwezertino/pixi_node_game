// Frame-grid geometry for the real per-unit spritesheets in public/assets/actual/.
// Confirmed against actual pixel dimensions (see chat/session notes): every sheet in
// this pack uses 4 columns (frames) per row, and cell size is simply
// sheetWidth / 4 — that holds for every unit checked, including the smaller Rogue
// combat sheet. Row *count* (not cell size) is what tells sheets apart.
//
// Non-combat sheets (anything not "*_Combat.png"): one row per (animation, direction)
// pair, in NON_COMBAT_SPECS order. Most animations have all 4 directions, but climb
// only has one (it's not drawn per-direction at all) and getUp only has two (right,
// left) — that's exactly what makes the total come out to 31 rows instead of the
// 9*4=36 you'd get if every animation had all 4: 36 - (4-1 for climb) - (4-2 for
// getUp) = 31.
//
// Combat sheets group by DIRECTION first and animation variant second — the
// opposite of the non-combat sheet's grouping — confirmed against the real art via
// the unit-viewer dev tool: attack1_right, attack2_right, attack1_left, attack2_left,
// attack1_down, attack2_down, attack1_up, attack2_up, then ready for each direction
// (not doubled). See GUARD_KNIGHT_COMBAT_ROWS/ARCHER_COMBAT_ROWS/WARRIOR_COMBAT_ROWS
// for the exact row-by-row lists. Row count tells you which layout a sheet has:
//   8 rows  -> archer:      bowShot x4 dirs, then bowReady x4 dirs
//   12 rows -> guard/knight: (attack1,attack2) x4 dirs, then ready x4 dirs
//   20 rows -> warrior:      guard/knight rows, then archer rows
// (Rogue's combat sheet is 20 rows too, just with a smaller cell — same layout.)

export type Direction = "right" | "left" | "down" | "up";
export const DIRECTIONS: readonly Direction[] = ["right", "left", "down", "up"];

export type NonCombatAnimation =
    | "idle"
    | "walk"
    | "run"
    | "carry"
    | "pickUp"
    | "interact"
    | "knockdown"
    | "climb"
    | "getUp";

interface NonCombatAnimSpec {
    name: NonCombatAnimation;
    /** Directions this animation actually has a row for, in sheet order. */
    directions: readonly Direction[];
}

// Order and per-animation direction counts as confirmed against the real sheets.
const NON_COMBAT_SPECS: readonly NonCombatAnimSpec[] = [
    { name: "idle", directions: DIRECTIONS },
    { name: "walk", directions: DIRECTIONS },
    { name: "run", directions: DIRECTIONS },
    { name: "carry", directions: DIRECTIONS },
    { name: "pickUp", directions: DIRECTIONS },
    { name: "interact", directions: DIRECTIONS },
    { name: "knockdown", directions: DIRECTIONS },
    { name: "climb", directions: ["down"] }, // single direction only — there's just one row
    { name: "getUp", directions: ["right", "left"] },
];

export const NON_COMBAT_ANIMATIONS: readonly NonCombatAnimation[] = NON_COMBAT_SPECS.map((s) => s.name);

export type CombatAnimation = "attack1" | "attack2" | "ready" | "bowShot" | "bowReady";

interface CombatRow {
    name: CombatAnimation;
    direction: Direction;
}

// Explicit row-by-row list rather than a generic "N rows per direction" formula:
// the sheet groups by direction first and animation variant second (attack1_right,
// attack2_right, attack1_left, attack2_left, ...), not by animation first — a
// different convention than the non-combat sheet, confirmed against the real art via
// the unit-viewer dev tool. ready is not doubled (one row per direction).
const GUARD_KNIGHT_COMBAT_ROWS: readonly CombatRow[] = [
    { name: "attack1", direction: "right" },
    { name: "attack2", direction: "right" },
    { name: "attack1", direction: "left" },
    { name: "attack2", direction: "left" },
    { name: "attack1", direction: "down" },
    { name: "attack2", direction: "down" },
    { name: "attack1", direction: "up" },
    { name: "attack2", direction: "up" },
    { name: "ready", direction: "right" },
    { name: "ready", direction: "left" },
    { name: "ready", direction: "down" },
    { name: "ready", direction: "up" },
];
const ARCHER_COMBAT_ROWS: readonly CombatRow[] = [
    { name: "bowShot", direction: "right" },
    { name: "bowShot", direction: "left" },
    { name: "bowShot", direction: "down" },
    { name: "bowShot", direction: "up" },
    { name: "bowReady", direction: "right" },
    { name: "bowReady", direction: "left" },
    { name: "bowReady", direction: "down" },
    { name: "bowReady", direction: "up" },
];
const WARRIOR_COMBAT_ROWS: readonly CombatRow[] = [...GUARD_KNIGHT_COMBAT_ROWS, ...ARCHER_COMBAT_ROWS];

export interface FrameRect {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface SheetGrid {
    cellSize: number;
    columns: number;
    rows: number;
}

export function gridFor(sheetWidth: number, sheetHeight: number): SheetGrid {
    const cellSize = sheetWidth / 4;
    return { cellSize, columns: 4, rows: Math.floor(sheetHeight / cellSize) };
}

// On-screen frame size (px) real per-unit sprites should render at — matches the
// unit-viewer dev tool's reference-sheet display, which is what "the right size" was
// confirmed against. Different units have different native cell sizes (16px for most,
// 24px for Heavy Knight/Paladin), so the actual sprite.scale applied is this divided
// by the unit's cellSize, not a flat constant — see spriteLoader.ts.
export const TARGET_ONSCREEN_SIZE = 64;

export function recommendedScale(cellSize: number): number {
    return TARGET_ONSCREEN_SIZE / cellSize;
}

// Cells are packed edge-to-edge with no gutter, so at the exact frame boundary a
// nearest-neighbor sample can round to the adjacent cell's outermost pixel — visible
// as a stray dark pixel from the neighbor's outline once scaled up 4-8x. Each cell
// has a pixel or two of transparent padding around the actual character art (see
// chat/session notes), so insetting the sampled rect by less than a pixel avoids the
// bleed without ever cropping real content.
const BLEED_INSET = 0.5;

function rowRects(grid: SheetGrid, row: number): FrameRect[] | undefined {
    if (row < 0 || row >= grid.rows) return undefined;
    const rects: FrameRect[] = [];
    for (let col = 0; col < grid.columns; col++) {
        rects.push({
            x: col * grid.cellSize + BLEED_INSET,
            y: row * grid.cellSize + BLEED_INSET,
            width: grid.cellSize - 2 * BLEED_INSET,
            height: grid.cellSize - 2 * BLEED_INSET,
        });
    }
    return rects;
}

/**
 * Non-combat sheet: one row per (animation, direction) — see NON_COMBAT_SPECS for
 * order and which directions each animation actually has. A direction not listed for
 * that animation (e.g. "up" for climb, or "down"/"up" for getUp) returns undefined.
 */
export function getNonCombatFrameRects(
    sheetWidth: number,
    sheetHeight: number,
    animation: NonCombatAnimation,
    direction: Direction
): FrameRect[] | undefined {
    const grid = gridFor(sheetWidth, sheetHeight);
    let rowOffset = 0;
    for (const spec of NON_COMBAT_SPECS) {
        if (spec.name === animation) {
            const dirIndex = spec.directions.indexOf(direction);
            if (dirIndex < 0) return undefined;
            return rowRects(grid, rowOffset + dirIndex);
        }
        rowOffset += spec.directions.length;
    }
    return undefined;
}

function rowsFor(sheetWidth: number, sheetHeight: number): readonly CombatRow[] | undefined {
    const { rows } = gridFor(sheetWidth, sheetHeight);
    if (rows === ARCHER_COMBAT_ROWS.length) return ARCHER_COMBAT_ROWS;
    if (rows === GUARD_KNIGHT_COMBAT_ROWS.length) return GUARD_KNIGHT_COMBAT_ROWS;
    if (rows === WARRIOR_COMBAT_ROWS.length) return WARRIOR_COMBAT_ROWS;
    return undefined; // Unrecognized layout.
}

/** Which combat animations a sheet has, derived from its row count (see module doc). */
export function combatAnimationsFor(sheetWidth: number, sheetHeight: number): readonly CombatAnimation[] | undefined {
    const rows = rowsFor(sheetWidth, sheetHeight);
    if (!rows) return undefined;
    return [...new Set(rows.map((r) => r.name))];
}

export function getCombatFrameRects(
    sheetWidth: number,
    sheetHeight: number,
    animation: CombatAnimation,
    direction: Direction
): FrameRect[] | undefined {
    const rows = rowsFor(sheetWidth, sheetHeight);
    if (!rows) return undefined;
    const rowIndex = rows.findIndex((r) => r.name === animation && r.direction === direction);
    if (rowIndex < 0) return undefined;
    return rowRects(gridFor(sheetWidth, sheetHeight), rowIndex);
}
