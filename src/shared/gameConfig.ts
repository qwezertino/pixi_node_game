export interface GameConfig {
  network: {
    tickRate: number;
    syncInterval: number;
  };
  movement: {
    // GDD §60 World Coordinate Resolution: world units per meter (10 = 1 unit =
    // 0.1m/decimeter). Per-unit movement speed (units.json moveSpeed, m/s) is
    // converted through this — see client/utils/movement.ts and, authoritatively,
    // server world.go moveStat.
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

import configData from './gameConfig.json';

export const gameConfig: GameConfig = configData;

export const NETWORK = gameConfig.network;
export const MOVEMENT = gameConfig.movement;
export const WORLD = gameConfig.world;
export const PLAYER = gameConfig.player;
export const COLORS = gameConfig.colors;
export const GAME = gameConfig.game;
