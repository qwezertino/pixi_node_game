import { describe, expect, test } from "bun:test";
import { StructureGrid, type StructureAABB } from "./structureGrid";
import { QueryScratch } from "./grid";

function wall(structureId: number, partId: number, minX: number, minY: number, maxX: number, maxY: number): StructureAABB {
    return { structureId, partId, minX, minY, maxX, maxY, renderKind: "wood_wall" };
}

describe("StructureGrid restore", () => {
    test("restores colliders and revision", () => {
        const g = new StructureGrid(32000, 32000, [wall(9001, 1, 3300, 700, 3340, 1800)], 5);
        expect(g.revision).toBe(5);
        expect(g.lookupHandle({ structureId: 9001, partId: 1 })).toBe(0);
    });

    test("rejects duplicate keys", () => {
        expect(() => new StructureGrid(32000, 32000, [wall(1, 1, 0, 0, 10, 10), wall(1, 1, 20, 20, 30, 30)], 0)).toThrow();
    });

    test("rejects invalid AABB", () => {
        expect(() => new StructureGrid(32000, 32000, [wall(1, 1, 10, 0, 10, 10)], 0)).toThrow();
    });
});

describe("StructureGrid mutations", () => {
    test("upsert adds a new collider", () => {
        const g = new StructureGrid(32000, 32000, [], 0);
        g.applyMutationBatch({
            revision: 1,
            effectiveTick: 0,
            mutations: [{ operation: "upsert", key: { structureId: 1, partId: 1 }, collider: wall(1, 1, 100, 100, 200, 200) }],
        });
        expect(g.revision).toBe(1);
        expect(g.lookupHandle({ structureId: 1, partId: 1 })).toBeDefined();
    });

    test("remove only affects the target cell", () => {
        const g = new StructureGrid(32000, 32000, [wall(1, 1, 0, 0, 10, 10), wall(2, 1, 1000, 1000, 1010, 1010)], 0);
        g.applyMutationBatch({ revision: 1, effectiveTick: 0, mutations: [{ operation: "remove", key: { structureId: 1, partId: 1 } }] });
        expect(g.lookupHandle({ structureId: 1, partId: 1 })).toBeUndefined();
        expect(g.lookupHandle({ structureId: 2, partId: 1 })).toBeDefined();
    });

    test("set_solid false then true toggles presence", () => {
        const g = new StructureGrid(32000, 32000, [wall(1, 1, 0, 0, 10, 10)], 0);
        g.applyMutationBatch({ revision: 1, effectiveTick: 0, mutations: [{ operation: "set_solid", key: { structureId: 1, partId: 1 }, solid: false }] });
        expect(g.lookupHandle({ structureId: 1, partId: 1 })).toBeUndefined();
        g.applyMutationBatch({
            revision: 2,
            effectiveTick: 0,
            mutations: [{ operation: "set_solid", key: { structureId: 1, partId: 1 }, solid: true, collider: wall(1, 1, 0, 0, 10, 10) }],
        });
        expect(g.lookupHandle({ structureId: 1, partId: 1 })).toBeDefined();
    });

    test("invalid batch leaves no partial changes", () => {
        const g = new StructureGrid(32000, 32000, [], 0);
        expect(() =>
            g.applyMutationBatch({
                revision: 1,
                effectiveTick: 0,
                mutations: [
                    { operation: "upsert", key: { structureId: 1, partId: 1 }, collider: wall(1, 1, 0, 0, 10, 10) },
                    { operation: "upsert", key: { structureId: 1, partId: 2 }, collider: { structureId: 1, partId: 2, minX: 10, minY: 0, maxX: 10, maxY: 10, renderKind: "x" } },
                ],
            }),
        ).toThrow();
        expect(g.revision).toBe(0);
        expect(g.lookupHandle({ structureId: 1, partId: 1 })).toBeUndefined();
    });

    test("rejects wrong revision", () => {
        const g = new StructureGrid(32000, 32000, [], 5);
        expect(() =>
            g.applyMutationBatch({
                revision: 7,
                effectiveTick: 0,
                mutations: [{ operation: "upsert", key: { structureId: 1, partId: 1 }, collider: wall(1, 1, 0, 0, 10, 10) }],
            }),
        ).toThrow();
    });

    test("handle reuse after full removal", () => {
        const g = new StructureGrid(32000, 32000, [wall(1, 1, 0, 0, 10, 10)], 0);
        g.applyMutationBatch({ revision: 1, effectiveTick: 0, mutations: [{ operation: "remove", key: { structureId: 1, partId: 1 } }] });
        g.applyMutationBatch({
            revision: 2,
            effectiveTick: 0,
            mutations: [{ operation: "upsert", key: { structureId: 2, partId: 1 }, collider: wall(2, 1, 500, 500, 510, 510) }],
        });
        const scratch = new QueryScratch();
        expect(g.queryAABB({ minX: 0, minY: 0, maxX: 10, maxY: 10 }, scratch).length).toBe(0);
        expect(g.queryAABB({ minX: 500, minY: 500, maxX: 510, maxY: 510 }, scratch).length).toBe(1);
    });
});
