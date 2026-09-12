import { describe, expect, test } from "bun:test";
import { gridDims, QueryScratch, MapStaticGrid, type MapAABB } from "./grid";

describe("gridDims", () => {
    test("divides evenly", () => {
        expect(gridDims(32000, 32000, 64)).toEqual({ columns: 500, rows: 500 });
    });
    test("non-divisible rounds up", () => {
        expect(gridDims(100, 100, 64)).toEqual({ columns: 2, rows: 2 });
    });
});

describe("MapStaticGrid", () => {
    test("single-cell object is queryable", () => {
        const colliders: MapAABB[] = [{ id: 1, minX: 10, minY: 10, maxX: 20, maxY: 20, renderKind: "stone_wall" }];
        const g = new MapStaticGrid(640, 640, colliders);
        const scratch = new QueryScratch();
        const got = g.queryAABB({ minX: 0, minY: 0, maxX: 63, maxY: 63 }, scratch);
        expect(got).toEqual([0]);
    });

    test("out-of-bounds query clamps and still finds collider", () => {
        const colliders: MapAABB[] = [{ id: 1, minX: 0, minY: 0, maxX: 10, maxY: 10, renderKind: "stone_wall" }];
        const g = new MapStaticGrid(64, 64, colliders);
        const scratch = new QueryScratch();
        const got = g.queryAABB({ minX: -1000, minY: -1000, maxX: 1000, maxY: 1000 }, scratch);
        expect(got).toEqual([0]);
    });

    test("large collider spanning many cells deduplicates to one result", () => {
        const colliders: MapAABB[] = [{ id: 1, minX: 0, minY: 0, maxX: 500, maxY: 500, renderKind: "stone_wall" }];
        const g = new MapStaticGrid(640, 640, colliders);
        const scratch = new QueryScratch();
        const got = g.queryAABB({ minX: 0, minY: 0, maxX: 500, maxY: 500 }, scratch);
        expect(got.length).toBe(1);
    });

    test("query touching a cell boundary at 64 includes the neighbor", () => {
        const colliders: MapAABB[] = [{ id: 1, minX: 64, minY: 64, maxX: 128, maxY: 128, renderKind: "stone_wall" }];
        const g = new MapStaticGrid(640, 640, colliders);
        const scratch = new QueryScratch();
        expect(g.queryAABB({ minX: 60, minY: 60, maxX: 64, maxY: 64 }, scratch).length).toBe(1);
        expect(g.queryAABB({ minX: 0, minY: 0, maxX: 63, maxY: 63 }, scratch).length).toBe(0);
    });

    test("rejects invalid AABB", () => {
        const colliders: MapAABB[] = [{ id: 1, minX: 10, minY: 10, maxX: 10, maxY: 20, renderKind: "stone_wall" }];
        expect(() => new MapStaticGrid(640, 640, colliders)).toThrow();
    });

    test("scratch is reusable across calls", () => {
        const colliders: MapAABB[] = [
            { id: 1, minX: 0, minY: 0, maxX: 10, maxY: 10, renderKind: "stone_wall" },
            { id: 2, minX: 600, minY: 600, maxX: 610, maxY: 610, renderKind: "stone_wall" },
        ];
        const g = new MapStaticGrid(640, 640, colliders);
        const scratch = new QueryScratch();
        expect(g.queryAABB({ minX: 0, minY: 0, maxX: 10, maxY: 10 }, scratch)).toEqual([0]);
        expect(g.queryAABB({ minX: 600, minY: 600, maxX: 610, maxY: 610 }, scratch)).toEqual([1]);
    });
});
