/**
 * Общие настройки игры, используемые как на сервере, так и на клиенте
 */

// Параметры сети
export const NETWORK = {
    TICK_RATE: 32, // Обновлений в секунду (влияет на частоту обновления физики)
    SYNC_INTERVAL: 30000, // Интервал полной синхронизации состояния (мс)
};

// Параметры движения
export const MOVEMENT = {
    PLAYER_SPEED: 100, // Базовая скорость движения игрока (единиц/сек)
    ACCELERATION: 0.5, // Ускорение при начале движения
    DECELERATION: 0.8, // Замедление при остановке
};

// Параметры игрового мира
export const WORLD = {
    SPAWN_AREA: {
        MIN_X: 100,
        MAX_X: 700,
        MIN_Y: 100,
        MAX_Y: 500,
    },
    BOUNDARIES: {
        MIN_X: 0,
        MAX_X: 800,
        MIN_Y: 0,
        MAX_Y: 600,
    },
};

// Параметры игроков
export const PLAYER = {
    BASE_SCALE: 2, // Базовый масштаб спрайтов игроков
    ANIMATION_SPEED: 0.1, // Скорость анимации
};

// Другие настройки игры
export const GAME = {
    DEBUG_MODE: false, // Режим отладки
};