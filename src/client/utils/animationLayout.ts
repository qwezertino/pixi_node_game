
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

const NON_COMBAT_SPECS: readonly NonCombatAnimSpec[] = [
    { name: "idle", directions: DIRECTIONS },
    { name: "walk", directions: DIRECTIONS },
    { name: "run", directions: DIRECTIONS },
    { name: "carry", directions: DIRECTIONS },
    { name: "pickUp", directions: DIRECTIONS },
    { name: "interact", directions: DIRECTIONS },
    { name: "knockdown", directions: DIRECTIONS },
    { name: "climb", directions: ["down"] },
    { name: "getUp", directions: ["right", "left"] },
];

export const NON_COMBAT_ANIMATIONS: readonly NonCombatAnimation[] = NON_COMBAT_SPECS.map((s) => s.name);

export type CombatAnimation = "attack1" | "attack2" | "ready" | "bowShot" | "bowReady";

interface CombatRow {
    name: CombatAnimation;
    direction: Direction;
}

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

export const TARGET_ONSCREEN_SIZE = 64;

const CONTENT_CELL_OVERRIDE: Record<string, number> = {
    heavy_knight: 16,
    paladin: 16,
};

/** The cell size that should drive scaling decisions for this unit — see CONTENT_CELL_OVERRIDE. */
export function effectiveCellSize(cellSize: number, unitId?: string): number {
    return (unitId && CONTENT_CELL_OVERRIDE[unitId]) || cellSize;
}

export function recommendedScale(cellSize: number, unitId?: string): number {
    return TARGET_ONSCREEN_SIZE / effectiveCellSize(cellSize, unitId);
}

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
    return undefined;
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
