export interface GameConfig {
  network: {
    tickRate: number;
    syncInterval: number;
  };
  movement: {

    unitsPerMeter: number;
  };
  world: {
    virtualSize: {
      width: number;
      height: number;
    };
    spawnArea: {
      minX: number;
      maxX: number;
      minY: number;
      maxY: number;
    };
    boundaries: {
      minX: number;
      maxX: number;
      minY: number;
      maxY: number;
    };
  };
  player: {
    baseScale: number;
  };
  game: {
    debugMode: boolean;
  };
  colors: {
    worldBackground: string;
  };
}

import { showLoadingError } from "./loadingScreen";

// Fetched from the server at startup — there is no bundled fallback. Game
// rules live only in Postgres now (see src/server/internal/liveconfig and
// docker/postgres/init/001_init.sql); the server must be reachable at
// GET /api/config for the client to boot at all.
async function loadGameConfig(): Promise<GameConfig> {
  try {
    const res = await fetch("/api/config");
    if (!res.ok) throw new Error(`status ${res.status}`);
    return (await res.json()) as GameConfig;
  } catch (err) {
    showLoadingError("Could not reach the game server. Please try reloading.");
    throw err;
  }
}

export const gameConfig: GameConfig = await loadGameConfig();

export const NETWORK = gameConfig.network;
export const MOVEMENT = gameConfig.movement;
export const WORLD = gameConfig.world;
export const PLAYER = gameConfig.player;
export const COLORS = gameConfig.colors;
export const GAME = gameConfig.game;
