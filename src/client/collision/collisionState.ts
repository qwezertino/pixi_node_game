/**
 * Client-side collision world lifecycle: builds the StructureGrid from the
 * server's structure_collision_snapshot, applies structure_collision_delta
 * batches in order, and exposes the combined CollisionWorld (map + runtime
 * structures) once both are known. See docs/collisions_plan.md, "Клиент и
 * API".
 */

import { WORLD } from "../../shared/gameConfig";
import { mapStaticGrid } from "../../shared/mapColliders";
import { StructureGrid, type StructureAABB, type StructureMutationBatch } from "./structureGrid";
import { CollisionWorld } from "./world";
import type { StructureCollisionSnapshotMessage, StructureCollisionDeltaMessage } from "../network/protocol/messages";

let structureGrid: StructureGrid | null = null;
let collisionWorld: CollisionWorld | null = null;
let resolveReady: (() => void) | null = null;
const changeListeners: (() => void)[] = [];

/** Registers a callback fired after the snapshot or any successfully
 * applied delta changes the structure layer — e.g. to redraw walls. */
export function onStructuresChanged(cb: () => void): void {
    changeListeners.push(cb);
}

function notifyChanged(): void {
    for (const cb of changeListeners) cb();
}

export function getStructureColliders(): StructureAABB[] {
    return structureGrid?.allColliders() ?? [];
}

/** Resolves once the first structure_collision_snapshot has been applied. */
export const collisionReady: Promise<void> = new Promise((resolve) => {
    resolveReady = resolve;
});

export function isReady(): boolean {
    return collisionWorld !== null;
}

export function getCollisionWorld(): CollisionWorld | null {
    return collisionWorld;
}

export function currentStructureRevision(): number {
    return structureGrid?.revision ?? 0;
}

function toStructureAABB(c: StructureCollisionSnapshotMessage["colliders"][number]): StructureAABB {
    return { structureId: c.structureId, partId: c.partId, minX: c.minX, minY: c.minY, maxX: c.maxX, maxY: c.maxY, renderKind: c.renderKind };
}

export function applyStructureSnapshot(msg: StructureCollisionSnapshotMessage): void {
    const colliders = msg.colliders.map(toStructureAABB);
    structureGrid = new StructureGrid(WORLD.virtualSize.width, WORLD.virtualSize.height, colliders, msg.revision);
    collisionWorld = new CollisionWorld(WORLD.virtualSize.width, WORLD.virtualSize.height, mapStaticGrid, structureGrid);
    resolveReady?.();
    resolveReady = null;
    notifyChanged();
}

function toMutationBatch(msg: StructureCollisionDeltaMessage): StructureMutationBatch {
    return {
        revision: msg.revision,
        effectiveTick: msg.effectiveTick,
        mutations: msg.mutations.map((m) => ({
            operation: m.operation,
            key: { structureId: m.structureId, partId: m.partId },
            solid: m.solid,
            collider:
                m.solid && m.minX !== undefined && m.minY !== undefined && m.maxX !== undefined && m.maxY !== undefined
                    ? { structureId: m.structureId, partId: m.partId, minX: m.minX, minY: m.minY, maxX: m.maxX, maxY: m.maxY, renderKind: m.renderKind ?? "" }
                    : undefined,
        })),
    };
}

/**
 * Applies a structure_collision_delta batch. Returns false (and leaves the
 * grid untouched) if it can't be applied — a revision gap, an unknown
 * entity, or the snapshot hasn't arrived yet — so the caller can request a
 * fresh structure_collision_resync.
 */
export function applyStructureCollisionDelta(msg: StructureCollisionDeltaMessage): boolean {
    if (!structureGrid) return false;
    try {
        structureGrid.applyMutationBatch(toMutationBatch(msg));
        notifyChanged();
        return true;
    } catch (err) {
        console.warn("collision: structure delta rejected, resync needed", err);
        return false;
    }
}
