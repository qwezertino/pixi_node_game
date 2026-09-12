import { describe, expect, test } from "bun:test";
import { CollisionWorld, MoveScratch } from "./world";
import { MapStaticGrid, type MapAABB } from "./grid";
import { StructureGrid, type StructureAABB } from "./structureGrid";
import { PLAYER_RADIUS, MAX_MOVE_STEPS } from "./geometry";

function newTestWorld(colliders: MapAABB[] = []): CollisionWorld {
    const mapGrid = new MapStaticGrid(2000, 2000, colliders);
    const structGrid = new StructureGrid(2000, 2000, [], 0);
    return new CollisionWorld(2000, 2000, mapGrid, structGrid);
}

/**
 * These scenarios are copied verbatim (same inputs, same expected outputs)
 * from src/server/internal/collision/world_test.go to lock in
 * client/server parity for the movement solver.
 */
describe("MoveCircle parity with Go solver", () => {
    test("unobstructed movement", () => {
        const w = newTestWorld();
        const res = w.moveCircle(100, 100, 1, 0, 10, new MoveScratch());
        expect(res.x).toBe(110);
        expect(res.y).toBe(100);
        expect(res.effectiveDX).toBe(1);
        expect(res.effectiveDY).toBe(0);
        expect(res.blockedX).toBe(false);
        expect(res.blockedY).toBe(false);
    });

    test("direct collision from all four sides", () => {
        const wall: MapAABB = { id: 1, minX: 190, minY: 190, maxX: 210, maxY: 210, renderKind: "stone_wall" };
        const cases: Array<[number, number, number, number, number, number]> = [
            [150, 200, 1, 0, 186, 200],
            [250, 200, -1, 0, 214, 200],
            [200, 150, 0, 1, 200, 186],
            [200, 250, 0, -1, 200, 214],
        ];
        for (const [startX, startY, dx, dy, wantX, wantY] of cases) {
            const w = newTestWorld([wall]);
            const res = w.moveCircle(startX, startY, dx, dy, 64, new MoveScratch());
            expect([res.x, res.y]).toEqual([wantX, wantY]);
        }
    });

    test("diagonal sliding along a vertical wall", () => {
        const wall: MapAABB = { id: 1, minX: 210, minY: 0, maxX: 250, maxY: 2000, renderKind: "stone_wall" };
        const w = newTestWorld([wall]);
        const res = w.moveCircle(190, 100, 1, 1, 30, new MoveScratch());
        expect(res.x).toBeLessThan(210 - PLAYER_RADIUS + 1);
        expect(res.y).toBe(130);
        expect(res.effectiveDX).toBe(0);
        expect(res.effectiveDY).toBe(1);
        expect(res.blockedX).toBe(true);
        expect(res.blockedY).toBe(false);
    });

    test("full stop when both axes blocked at inner corner", () => {
        const wallRight: MapAABB = { id: 1, minX: 210, minY: 0, maxX: 250, maxY: 2000, renderKind: "stone_wall" };
        const wallBelow: MapAABB = { id: 2, minX: 0, minY: 210, maxX: 2000, maxY: 250, renderKind: "stone_wall" };
        const w = newTestWorld([wallRight, wallBelow]);
        const res = w.moveCircle(200, 200, 1, 1, 20, new MoveScratch());
        expect(res.effectiveDX).toBe(0);
        expect(res.effectiveDY).toBe(0);
        expect(res.blockedX).toBe(true);
        expect(res.blockedY).toBe(true);
    });

    test("outer corner allows diagonal movement past a block's edge", () => {
        const block: MapAABB = { id: 1, minX: 200, minY: 200, maxX: 210, maxY: 210, renderKind: "stone_wall" };
        const w = newTestWorld([block]);
        const res = w.moveCircle(150, 150, 1, 1, 40, new MoveScratch());
        expect(res.effectiveDX).toBe(1);
        expect(res.effectiveDY).toBe(1);
    });

    test("resumes X movement after passing a short wall's Y extent", () => {
        const wall: MapAABB = { id: 1, minX: 210, minY: 190, maxX: 250, maxY: 210, renderKind: "stone_wall" };
        const w = newTestWorld([wall]);
        const res = w.moveCircle(190, 190, 1, 1, 64, new MoveScratch());
        expect(res.x).toBeGreaterThan(210);
    });

    test("L-shaped corner blocks both axes with enough distance", () => {
        const horizontal: MapAABB = { id: 1, minX: 100, minY: 300, maxX: 400, maxY: 320, renderKind: "stone_wall" };
        const vertical: MapAABB = { id: 2, minX: 380, minY: 100, maxX: 400, maxY: 320, renderKind: "stone_wall" };
        const w = newTestWorld([horizontal, vertical]);
        const res = w.moveCircle(370, 280, 1, 1, 64, new MoveScratch());
        expect(res.effectiveDX).toBe(0);
        expect(res.effectiveDY).toBe(0);
    });

    test("movement away from a touched wall surface is free", () => {
        const wall: MapAABB = { id: 1, minX: 190, minY: 190, maxX: 210, maxY: 210, renderKind: "stone_wall" };
        const w = newTestWorld([wall]);
        const res = w.moveCircle(186, 200, -1, 0, 10, new MoveScratch());
        expect(res.x).toBe(176);
    });

    test.each([1, 2, 40])("anti-tunneling holds for %d-unit-thick walls at MAX_MOVE_STEPS", (thickness) => {
        const wallMinX = 500;
        const wall: MapAABB = { id: 1, minX: wallMinX, minY: 0, maxX: wallMinX + thickness, maxY: 2000, renderKind: "stone_wall" };
        const w = newTestWorld([wall]);
        const res = w.moveCircle(400, 100, 1, 0, MAX_MOVE_STEPS, new MoveScratch());
        expect(res.x).toBeLessThan(wallMinX);
    });

    test("anti-tunneling holds at sprint distance (26 units/tick)", () => {
        const wallMinX = 500;
        const wall: MapAABB = { id: 1, minX: wallMinX, minY: 0, maxX: wallMinX + 1, maxY: 2000, renderKind: "stone_wall" };
        const w = newTestWorld([wall]);
        const res = w.moveCircle(480, 100, 1, 0, 26, new MoveScratch());
        expect(res.x).toBeLessThan(wallMinX);
    });

    test("distance of exactly 1 moves exactly one unit", () => {
        const w = newTestWorld();
        const res = w.moveCircle(100, 100, 1, 0, 1, new MoveScratch());
        expect(res.x).toBe(101);
    });

    test("distance above MAX_MOVE_STEPS is a no-op", () => {
        const w = newTestWorld();
        const res = w.moveCircle(100, 100, 1, 0, 65, new MoveScratch());
        expect(res.x).toBe(100);
        expect(res.y).toBe(100);
    });

    test("negative distance throws", () => {
        const w = newTestWorld();
        expect(() => w.moveCircle(100, 100, 1, 0, -1, new MoveScratch())).toThrow();
    });

    test("world bounds clamp on all four sides", () => {
        const w = new CollisionWorld(1000, 1000, new MapStaticGrid(1000, 1000, []), new StructureGrid(1000, 1000, [], 0));
        expect(w.moveCircle(10, 500, -1, 0, 64, new MoveScratch()).x).toBe(PLAYER_RADIUS);
        expect(w.moveCircle(990, 500, 1, 0, 64, new MoveScratch()).x).toBe(1000 - PLAYER_RADIUS);
        expect(w.moveCircle(500, 10, 0, -1, 64, new MoveScratch()).y).toBe(PLAYER_RADIUS);
        expect(w.moveCircle(500, 990, 0, 1, 64, new MoveScratch()).y).toBe(1000 - PLAYER_RADIUS);
    });

    test("world corner clamps diagonally", () => {
        const w = new CollisionWorld(1000, 1000, new MapStaticGrid(1000, 1000, []), new StructureGrid(1000, 1000, [], 0));
        const res = w.moveCircle(10, 10, -1, -1, 64, new MoveScratch());
        expect(res.x).toBe(PLAYER_RADIUS);
        expect(res.y).toBe(PLAYER_RADIUS);
    });

    test("structure layer blocks movement", () => {
        const mapGrid = new MapStaticGrid(2000, 2000, []);
        const structColliders: StructureAABB[] = [{ structureId: 1, partId: 1, minX: 190, minY: 190, maxX: 210, maxY: 210, renderKind: "wood_wall" }];
        const structGrid = new StructureGrid(2000, 2000, structColliders, 0);
        const w = new CollisionWorld(2000, 2000, mapGrid, structGrid);
        const res = w.moveCircle(150, 200, 1, 0, 64, new MoveScratch());
        expect(res.x).toBe(186);
    });
});

describe("FindNearestFree parity with Go solver", () => {
    test("origin already free returns it unchanged", () => {
        const w = newTestWorld();
        const got = w.findNearestFree(500, 500, PLAYER_RADIUS, 128, new MoveScratch());
        expect(got).toEqual({ x: 500, y: 500 });
    });

    test("relocates when origin is occupied", () => {
        const wall: MapAABB = { id: 1, minX: 480, minY: 480, maxX: 520, maxY: 520, renderKind: "stone_wall" };
        const w = newTestWorld([wall]);
        const got = w.findNearestFree(500, 500, PLAYER_RADIUS, 128, new MoveScratch());
        expect(got).not.toBeNull();
    });

    test("no free position within limit returns null", () => {
        const wall: MapAABB = { id: 1, minX: 100, minY: 100, maxX: 900, maxY: 900, renderKind: "stone_wall" };
        const w = newTestWorld([wall]);
        const got = w.findNearestFree(500, 500, PLAYER_RADIUS, 128, new MoveScratch());
        expect(got).toBeNull();
    });
});
