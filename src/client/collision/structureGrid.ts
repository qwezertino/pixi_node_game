/**
 * Runtime-mutable structure collider index, mirroring
 * src/server/internal/collision/structuregrid.go. Applies the same
 * revision-gated mutation batches the server publishes as
 * structure_collision_delta messages, so client prediction/reconciliation
 * and remote dead reckoning see the same solid geometry as the server.
 */

import { type AABB, GRID_CELL_SIZE } from "./geometry";
import { gridDims, cellRange, flatIndex, QueryScratch } from "./grid";

export interface StructureAABB {
    structureId: number;
    partId: number;
    minX: number;
    minY: number;
    maxX: number;
    maxY: number;
    renderKind: string;
}

export interface StructureColliderKey {
    structureId: number;
    partId: number;
}

function keyOf(c: StructureAABB): StructureColliderKey {
    return { structureId: c.structureId, partId: c.partId };
}

function keyEquals(a: StructureColliderKey, b: StructureColliderKey): boolean {
    return a.structureId === b.structureId && a.partId === b.partId;
}

function keyString(k: StructureColliderKey): string {
    return `${k.structureId}:${k.partId}`;
}

export type StructureMutationOperation = "upsert" | "remove" | "set_solid";

export interface StructureMutation {
    operation: StructureMutationOperation;
    key: StructureColliderKey;
    collider?: StructureAABB;
    solid?: boolean;
}

export interface StructureMutationBatch {
    revision: number;
    effectiveTick: number;
    mutations: StructureMutation[];
}

function aabbValid(c: StructureAABB): boolean {
    return c.minX < c.maxX && c.minY < c.maxY;
}

/**
 * Mutable spatial index of solid structure colliders (walls, gates,
 * towers...). Mutations are only ever applied between simulated ticks
 * (mirroring the server's dedicated mutation phase), never mid-movement.
 */
export class StructureGrid {
    private readonly cellSize = GRID_CELL_SIZE;
    private readonly columns: number;
    private readonly rows: number;
    private revisionValue: number;
    private readonly buckets: number[][];
    private colliders: StructureAABB[] = [];
    private readonly lookup = new Map<string, number>();
    private freeList: number[] = [];

    constructor(worldWidth: number, worldHeight: number, colliders: StructureAABB[], revision: number) {
        const { columns, rows } = gridDims(worldWidth, worldHeight, this.cellSize);
        this.columns = columns;
        this.rows = rows;
        this.revisionValue = revision;
        this.buckets = new Array(columns * rows);
        for (let i = 0; i < this.buckets.length; i++) this.buckets[i] = [];

        for (const c of colliders) {
            if (!aabbValid(c)) throw new Error(`invalid AABB for structure collider (${c.structureId},${c.partId})`);
            const key = keyOf(c);
            const ks = keyString(key);
            if (this.lookup.has(ks)) throw new Error(`duplicate structure collider key (${c.structureId},${c.partId})`);
            const handle = this.colliders.length;
            this.colliders.push(c);
            this.lookup.set(ks, handle);
            this.insertIntoBuckets(handle, c);
        }
    }

    get revision(): number {
        return this.revisionValue;
    }

    collider(handle: number): StructureAABB {
        return this.colliders[handle];
    }

    lookupHandle(key: StructureColliderKey): number | undefined {
        return this.lookup.get(keyString(key));
    }

    /** Every currently active (solid) collider, e.g. for rendering. */
    allColliders(): StructureAABB[] {
        return Array.from(this.lookup.values(), (handle) => this.colliders[handle]);
    }

    private cellsOf(c: StructureAABB): [number, number, number, number] {
        const [minCX, maxCX] = cellRange(c.minX, c.maxX, this.cellSize, this.columns);
        const [minCY, maxCY] = cellRange(c.minY, c.maxY, this.cellSize, this.rows);
        return [minCX, maxCX, minCY, maxCY];
    }

    private insertIntoBuckets(handle: number, c: StructureAABB): void {
        const [minCX, maxCX, minCY, maxCY] = this.cellsOf(c);
        for (let cy = minCY; cy <= maxCY; cy++) {
            for (let cx = minCX; cx <= maxCX; cx++) {
                this.buckets[flatIndex(cx, cy, this.columns)].push(handle);
            }
        }
    }

    private removeFromBuckets(handle: number, c: StructureAABB): void {
        const [minCX, maxCX, minCY, maxCY] = this.cellsOf(c);
        for (let cy = minCY; cy <= maxCY; cy++) {
            for (let cx = minCX; cx <= maxCX; cx++) {
                const fi = flatIndex(cx, cy, this.columns);
                const bucket = this.buckets[fi];
                const i = bucket.indexOf(handle);
                if (i !== -1) bucket.splice(i, 1);
            }
        }
    }

    queryAABB(bounds: AABB, scratch: QueryScratch): number[] {
        const gen = scratch.beginQuery(this.colliders.length);
        const [minCX, maxCX] = cellRange(bounds.minX, bounds.maxX, this.cellSize, this.columns);
        const [minCY, maxCY] = cellRange(bounds.minY, bounds.maxY, this.cellSize, this.rows);
        for (let cy = minCY; cy <= maxCY; cy++) {
            for (let cx = minCX; cx <= maxCX; cx++) {
                for (const handle of this.buckets[flatIndex(cx, cy, this.columns)]) {
                    if (!scratch.seen(handle, gen)) scratch.result.push(handle);
                }
            }
        }
        return scratch.result;
    }

    /**
     * Validates the whole batch (bounds, unknown keys) before applying any
     * operation, so a rejected batch never leaves partial state, and
     * requires revision to be exactly the next one.
     */
    applyMutationBatch(batch: StructureMutationBatch): void {
        if (batch.revision !== this.revisionValue + 1) {
            throw new Error(
                `structure mutation batch revision ${batch.revision} is not the expected next revision ${this.revisionValue + 1}`,
            );
        }

        for (const m of batch.mutations) {
            switch (m.operation) {
                case "upsert":
                    if (!m.collider || !keyEquals(keyOf(m.collider), m.key)) {
                        throw new Error(`mutation collider does not match key (${m.key.structureId},${m.key.partId})`);
                    }
                    if (!aabbValid(m.collider)) {
                        throw new Error(`invalid AABB for structure collider (${m.key.structureId},${m.key.partId})`);
                    }
                    break;
                case "set_solid":
                    if (m.solid) {
                        if (!m.collider || !keyEquals(keyOf(m.collider), m.key)) {
                            throw new Error(`mutation collider does not match key (${m.key.structureId},${m.key.partId})`);
                        }
                        if (!aabbValid(m.collider)) {
                            throw new Error(`invalid AABB for structure collider (${m.key.structureId},${m.key.partId})`);
                        }
                    } else if (!this.lookup.has(keyString(m.key))) {
                        throw new Error(`structure mutation references unknown collider (${m.key.structureId},${m.key.partId})`);
                    }
                    break;
                case "remove":
                    if (!this.lookup.has(keyString(m.key))) {
                        throw new Error(`structure mutation references unknown collider (${m.key.structureId},${m.key.partId})`);
                    }
                    break;
            }
        }

        for (const m of batch.mutations) {
            switch (m.operation) {
                case "upsert":
                    this.upsert(m.key, m.collider as StructureAABB);
                    break;
                case "set_solid":
                    if (m.solid) this.upsert(m.key, m.collider as StructureAABB);
                    else this.remove(m.key);
                    break;
                case "remove":
                    this.remove(m.key);
                    break;
            }
        }
        this.revisionValue = batch.revision;
    }

    private upsert(key: StructureColliderKey, c: StructureAABB): void {
        const ks = keyString(key);
        const existing = this.lookup.get(ks);
        if (existing !== undefined) {
            this.removeFromBuckets(existing, this.colliders[existing]);
            this.colliders[existing] = c;
            this.insertIntoBuckets(existing, c);
            return;
        }
        let handle: number;
        const freed = this.freeList.pop();
        if (freed !== undefined) {
            handle = freed;
            this.colliders[handle] = c;
        } else {
            handle = this.colliders.length;
            this.colliders.push(c);
        }
        this.lookup.set(ks, handle);
        this.insertIntoBuckets(handle, c);
    }

    private remove(key: StructureColliderKey): void {
        const ks = keyString(key);
        const handle = this.lookup.get(ks);
        if (handle === undefined) return;
        this.removeFromBuckets(handle, this.colliders[handle]);
        this.lookup.delete(ks);
        this.freeList.push(handle);
    }
}
