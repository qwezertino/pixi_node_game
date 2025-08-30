// Загрузчик конфигурации для клиента
export interface GameConfig {
  network: {
    tickRate: number;
    syncInterval: number;
    batchIntervalMs: number;
    port: number;
    maxConnections: number;
    eventChannelSize: number;
  };
  movement: {
    playerSpeedPerTick: number;
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
    animationSpeed: number;
  };
  game: {
    debugMode: boolean;
  };
}

// Импортируем конфиг как модуль
import configData from './gameConfig.json';

export const gameConfig: GameConfig = configData;

// Экспортируем отдельные секции для удобства
export const NETWORK = gameConfig.network;
export const MOVEMENT = gameConfig.movement;
export const WORLD = gameConfig.world;
export const PLAYER = gameConfig.player;
export const GAME = gameConfig.game;
