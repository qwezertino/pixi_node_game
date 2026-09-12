/**
 * Shared spatial-grid addressing and MapStaticGrid, mirroring
 * src/server/internal/collision/grid.go.
 */

import { type AABB, GRID_CELL_SIZE } from "./geometry";

export function gridDims(worldWidth: number, worldHeight: number, cellSize: number): { columns: number; rows: number } {
    return {
        columns: Math.floor((worldWidth + cellSize - 1) / cellSize),
        rows: Math.floor((worldHeight + cellSize - 1) / cellSize),
    };
}

function cellCoord(v: number, cellSize: number, count: number): number {
    const c = Math.floor(v / cellSize);
    if (c < 0) return 0;
    if (c > count - 1) return count - 1;
    return c;
}

function cellRange(minV: number, maxV: number, cellSize: number, count: number): [number, number] {
    return [cellCoord(minV, cellSize, count), cellCoord(maxV, cellSize, count)];
}

function flatIndex(cellX: number, cellY: number, columns: number): number {
    return cellY * columns + cellX;
}

/**
 * Reusable, generation-counted dedup scratch for grid queries. Mirrors
 * collision.QueryScratch: avoids a Set allocation per query while still
 * producing deduplicated results.
 */
export class QueryScratch {
    result: number[] = [];
    private seenGeneration: Uint32Array = new Uint32Array(0);
    private generation = 0;

    private ensureCapacity(n: number): void {
        if (this.seenGeneration.length >= n) return;
        const grown = new Uint32Array(n);
        grown.set(this.seenGeneration);
        this.seenGeneration = grown;
    }

    private nextGeneration(): number {
        this.generation++;
        if (this.generation > 0xffffffff) {
            this.seenGeneration.fill(0);
            this.generation = 1;
        }
        return this.generation;
    }

    /** @internal used by grid query implementations */
    beginQuery(candidateCount: number): number {
        this.ensureCapacity(candidateCount);
        this.result.length = 0;
        return this.nextGeneration();
    }

    /** @internal used by grid query implementations */
    seen(handle: number, gen: number): boolean {
        if (this.seenGeneration[handle] === gen) return true;
        this.seenGeneration[handle] = gen;
        return false;
    }
}

export interface MapAABB {
    id: number;
    minX: number;
    minY: number;
    maxX: number;
    maxY: number;
    renderKind: string;
}

/**
 * Immutable spatial index over campaign map geometry. Mirrors
 * collision.MapStaticGrid.
 */
export class MapStaticGrid {
    readonly columns: number;
    readonly rows: number;
    private readonly buckets: number[][];
    private readonly colliders: MapAABB[];

    constructor(worldWidth: number, worldHeight: number, colliders: MapAABB[]) {
        const { columns, rows } = gridDims(worldWidth, worldHeight, GRID_CELL_SIZE);
        this.columns = columns;
        this.rows = rows;
        this.buckets = new Array(columns * rows);
        for (let i = 0; i < this.buckets.length; i++) this.buckets[i] = [];
        this.colliders = colliders;

        colliders.forEach((c, idx) => {
            if (!(c.minX < c.maxX && c.minY < c.maxY)) {
                throw new Error(`invalid AABB for map collider ${c.id}`);
            }
            const [minCX, maxCX] = cellRange(c.minX, c.maxX, GRID_CELL_SIZE, columns);
            const [minCY, maxCY] = cellRange(c.minY, c.maxY, GRID_CELL_SIZE, rows);
            for (let cy = minCY; cy <= maxCY; cy++) {
                for (let cx = minCX; cx <= maxCX; cx++) {
                    this.buckets[flatIndex(cx, cy, columns)].push(idx);
                }
            }
        });
    }

    collider(idx: number): MapAABB {
        return this.colliders[idx];
    }

    get colliderCount(): number {
        return this.colliders.length;
    }

    /**
     * Returns the deduplicated indices of colliders whose cell membership
     * intersects bounds, written into scratch's reusable result array. The
     * returned array is only valid until the next query on the same
     * scratch.
     */
    queryAABB(bounds: AABB, scratch: QueryScratch): number[] {
        const gen = scratch.beginQuery(this.colliders.length);
        const [minCX, maxCX] = cellRange(bounds.minX, bounds.maxX, GRID_CELL_SIZE, this.columns);
        const [minCY, maxCY] = cellRange(bounds.minY, bounds.maxY, GRID_CELL_SIZE, this.rows);
        for (let cy = minCY; cy <= maxCY; cy++) {
            for (let cx = minCX; cx <= maxCX; cx++) {
                for (const idx of this.buckets[flatIndex(cx, cy, this.columns)]) {
                    if (!scratch.seen(idx, gen)) scratch.result.push(idx);
                }
            }
        }
        return scratch.result;
    }
}

export { cellRange, flatIndex };
