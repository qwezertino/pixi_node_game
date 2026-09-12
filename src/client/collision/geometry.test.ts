import { describe, expect, test } from "bun:test";
import { aabbValid, circleOverlapsAABB, type AABB } from "./geometry";

describe("aabbValid", () => {
    test("valid box", () => {
        expect(aabbValid({ minX: 0, minY: 0, maxX: 10, maxY: 10 })).toBe(true);
    });
    test("degenerate on X", () => {
        expect(aabbValid({ minX: 10, minY: 0, maxX: 10, maxY: 10 })).toBe(false);
    });
    test("degenerate on Y", () => {
        expect(aabbValid({ minX: 0, minY: 10, maxX: 10, maxY: 10 })).toBe(false);
    });
});

describe("circleOverlapsAABB", () => {
    const box: AABB = { minX: 100, minY: 100, maxX: 200, maxY: 200 };
    const radius = 4;

    test("far away is not overlapping", () => {
        expect(circleOverlapsAABB(0, 0, radius, box)).toBe(false);
    });
    test("center inside overlaps", () => {
        expect(circleOverlapsAABB(150, 150, radius, box)).toBe(true);
    });
    test("touching exactly at radius is not overlap", () => {
        expect(circleOverlapsAABB(96, 150, radius, box)).toBe(false);
    });
    test("one unit penetration overlaps", () => {
        expect(circleOverlapsAABB(97, 150, radius, box)).toBe(true);
    });
    test("on boundary edge overlaps (center on box)", () => {
        expect(circleOverlapsAABB(100, 150, radius, box)).toBe(true);
    });
    test("on boundary corner overlaps", () => {
        expect(circleOverlapsAABB(100, 100, radius, box)).toBe(true);
    });
});
