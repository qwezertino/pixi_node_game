import { WORLD } from "./gameConfig";
import { showLoadingError } from "./loadingScreen";
import { MapStaticGrid, type MapAABB } from "../client/collision/grid";

interface MapCollidersResponse {
    campaignId: number;
    generatorVersion: string;
    mapVersion: string;
    cellSize: number;
    playerRadius: number;
    colliders: {
        colliderId: number;
        minX: number;
        minY: number;
        maxX: number;
        maxY: number;
        renderKind: string;
    }[];
}

async function loadMapColliders(): Promise<MapCollidersResponse> {
    try {
        const res = await fetch("/api/map-colliders");
        if (!res.ok) throw new Error(`status ${res.status}`);
        return (await res.json()) as MapCollidersResponse;
    } catch (err) {
        showLoadingError("Could not reach the game server. Please try reloading.");
        throw err;
    }
}

const mapCollidersResponse: MapCollidersResponse = await loadMapColliders();

export const CAMPAIGN_ID = mapCollidersResponse.campaignId;
export const MAP_VERSION = mapCollidersResponse.mapVersion;

const mapColliders: MapAABB[] = mapCollidersResponse.colliders.map((c) => ({
    id: c.colliderId,
    minX: c.minX,
    minY: c.minY,
    maxX: c.maxX,
    maxY: c.maxY,
    renderKind: c.renderKind,
}));

/**
 * The immutable, campaign-wide static map collider index. Built once at
 * startup from GET /api/map-colliders, mirroring the server's
 * MapStaticGrid — see docs/collisions_plan.md.
 */
export const mapStaticGrid = new MapStaticGrid(WORLD.virtualSize.width, WORLD.virtualSize.height, mapColliders);
