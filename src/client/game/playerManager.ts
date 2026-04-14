import { Container, Point, AnimatedSprite, Texture } from "pixi.js";
import { NetworkManager } from "../network/networkManager";
import { PlayerState, TICK_RATE } from "../network/protocol/messages";
import { SpriteLoader } from "../utils/spriteLoader";
import {
    AnimationController,
    PlayerState as AnimationPlayerState,
} from "../controllers/animationController";
import { PLAYER } from "../../shared/gameConfig";
import { CoordinateConverter } from "../utils/coordinateConverter";
//import { LagCompensationSystem } from "../utils/lagCompensation";

// Тип для результата SpriteLoader.loadCharacterVisual после await
type CharacterVisual = {
    animations: Map<string, Texture[]>;
    getAnimation: (name: string) => AnimatedSprite | undefined;
};

// Represents a remote player in the game
// Снимок состояния для entity interpolation
interface PositionSnapshot {
    time: number; // performance.now() момент получения от сервера
    x: number;
    y: number;
}

const MIN_INTERPOLATION_DELAY_MS = 90;
const MAX_INTERPOLATION_DELAY_MS = 220;
const SNAPSHOT_EWMA_ALPHA = 0.15;
// Максимальное время экстраполяции когда буфер пуст (высокий пинг / потеря пакетов).
// После этого порога позиция замораживается до следующего снимка.
const MAX_EXTRAPOLATE_CAP_MS = 320;
const MAX_SNAPSHOTS = 32;

class RemotePlayer {
    sprite: AnimatedSprite;
    animationController: AnimationController;
    direction: -1 | 1 = 1;
    movementVector: { dx: number; dy: number } = { dx: 0, dy: 0 };
    isMoving: boolean = false;

    // Буфер серверных позиций для интерполяции
    private snapshots: PositionSnapshot[] = [];
    private interpolationDelayMs = 130;
    private interArrivalEwmaMs = 1000 / Math.max(TICK_RATE, 1);
    private jitterEwmaMs = 0;
    private lastSnapshotTimeMs = 0;
    private lastSnapshotSequence = 0;

    // Текущая рендер-позиция (интерполированная)
    public virtualPosition = { x: 0, y: 0 };
    private coordinateConverter: CoordinateConverter | null = null;



    constructor(
        public id: string,
        public position: Point, // Экранная позиция спрайта
        characterVisual: CharacterVisual,
        coordinateConverter?: CoordinateConverter,
        virtualPosition?: { x: number; y: number } // Позиция в виртуальном мире
    ) {
        this.coordinateConverter = coordinateConverter || null;

        if (virtualPosition) {
            this.virtualPosition.x = Math.round(virtualPosition.x);
            this.virtualPosition.y = Math.round(virtualPosition.y);
        } else {
            if (this.coordinateConverter) {
                const virtualPos = this.coordinateConverter.screenToVirtual(
                    position.x,
                    position.y
                );
                this.virtualPosition.x = virtualPos.x;
                this.virtualPosition.y = virtualPos.y;
            } else {
                this.virtualPosition.x = Math.round(position.x);
                this.virtualPosition.y = Math.round(position.y);
            }
        }
        this.sprite = characterVisual.getAnimation("idle")!;

        if (this.coordinateConverter) {
            const screenPos = this.coordinateConverter.virtualToScreen(this.virtualPosition.x, this.virtualPosition.y);
            this.position.x = screenPos.x;
            this.position.y = screenPos.y;
            this.sprite.position.copyFrom(this.position);
        } else {
            this.sprite.position.copyFrom(position);
        }

        this.sprite.scale.set(PLAYER.baseScale);
        this.sprite.animationSpeed = PLAYER.animationSpeed;
        this.sprite.play();

        this.animationController = new AnimationController(
            characterVisual.animations,
            this.sprite
        );
    }

    // Добавить серверный снимок позиции в буфер
    pushSnapshot(x: number, y: number, stateSequence?: number) {
        if (typeof stateSequence === "number") {
            const seq = stateSequence >>> 0;
            if (this.lastSnapshotSequence !== 0) {
                const delta = (seq - this.lastSnapshotSequence) >>> 0;
                if (delta === 0 || delta >= 0x80000000) {
                    return;
                }
            }
            this.lastSnapshotSequence = seq;
        }

        const now = performance.now();
        if (this.lastSnapshotTimeMs > 0) {
            const dt = now - this.lastSnapshotTimeMs;
            if (dt > 0) {
                this.interArrivalEwmaMs =
                    this.interArrivalEwmaMs * (1 - SNAPSHOT_EWMA_ALPHA) + dt * SNAPSHOT_EWMA_ALPHA;
                const deviation = Math.abs(dt - this.interArrivalEwmaMs);
                this.jitterEwmaMs =
                    this.jitterEwmaMs * (1 - SNAPSHOT_EWMA_ALPHA) + deviation * SNAPSHOT_EWMA_ALPHA;

                const targetDelay = Math.min(
                    MAX_INTERPOLATION_DELAY_MS,
                    Math.max(
                        MIN_INTERPOLATION_DELAY_MS,
                        this.interArrivalEwmaMs * 2.2 + this.jitterEwmaMs * 1.8
                    )
                );
                this.interpolationDelayMs =
                    this.interpolationDelayMs * (1 - SNAPSHOT_EWMA_ALPHA) + targetDelay * SNAPSHOT_EWMA_ALPHA;
            }
        }
        this.lastSnapshotTimeMs = now;

        this.snapshots.push({ time: now, x, y });
        // Удаляем старые снимки — держим только MAX_SNAPSHOTS
        if (this.snapshots.length > MAX_SNAPSHOTS) {
            this.snapshots.shift();
        }
    }

    update(_deltaTime: number) {
        const isAttacking =
            this.animationController.playerState ===
            AnimationPlayerState.ATTACKING;

        // Entity interpolation: рендерим позицию на adaptive delay в прошлом.
        // Это означает что мы всегда имеем два снимка вокруг целевого времени —
        // никаких экстраполяций и телепортов.
        const renderTime = performance.now() - this.interpolationDelayMs;
        const snaps = this.snapshots;

        if (snaps.length >= 2) {
            const newest = snaps[snaps.length - 1];

            if (renderTime <= newest.time) {
                // Нормальная интерполяция: ищем пару снимков вокруг renderTime
                let newer = newest;
                let older = snaps[snaps.length - 2];

                for (let i = snaps.length - 1; i >= 1; i--) {
                    if (snaps[i - 1].time <= renderTime) {
                        older = snaps[i - 1];
                        newer = snaps[i];
                        break;
                    }
                }

                const span = newer.time - older.time;
                const t = span > 0 ? Math.min(1, (renderTime - older.time) / span) : 1;
                this.virtualPosition.x = older.x + (newer.x - older.x) * t;
                this.virtualPosition.y = older.y + (newer.y - older.y) * t;
            } else {
                // renderTime опережает новейший снимок — высокий пинг / потеря пакетов.
                // Экстраполируем скоростью последних двух снимков вместо заморозки.
                const extrapolateMs = renderTime - newest.time;
                const maxExtrapolateMs = Math.min(
                    MAX_EXTRAPOLATE_CAP_MS,
                    Math.max(140, this.interpolationDelayMs * 1.75)
                );
                if (extrapolateMs <= maxExtrapolateMs) {
                    const prev = snaps[snaps.length - 2];
                    const dt = newest.time - prev.time;
                    if (dt > 0) {
                        const vx = (newest.x - prev.x) / dt;
                        const vy = (newest.y - prev.y) / dt;
                        this.virtualPosition.x = newest.x + vx * extrapolateMs;
                        this.virtualPosition.y = newest.y + vy * extrapolateMs;
                    } else {
                        this.virtualPosition.x = newest.x;
                        this.virtualPosition.y = newest.y;
                    }
                } else {
                    // Слишком долго без данных — замораживаем на последней известной позиции
                    this.virtualPosition.x = newest.x;
                    this.virtualPosition.y = newest.y;
                }
            }

            if (this.coordinateConverter) {
                const screenPos = this.coordinateConverter.virtualToScreen(
                    this.virtualPosition.x, this.virtualPosition.y
                );
                this.position.x = screenPos.x;
                this.position.y = screenPos.y;
            }
        } else if (snaps.length === 1) {
            // Только один снимок — просто применяем его
            this.virtualPosition.x = snaps[0].x;
            this.virtualPosition.y = snaps[0].y;
            if (this.coordinateConverter) {
                const screenPos = this.coordinateConverter.virtualToScreen(
                    this.virtualPosition.x, this.virtualPosition.y
                );
                this.position.x = screenPos.x;
                this.position.y = screenPos.y;
            }
        }
        // Если снимков нет — не двигаем, ждём первого gameState

        // Анимация
        if (!isAttacking) {
            this.animationController.setState(
                this.isMoving ? AnimationPlayerState.MOVING : AnimationPlayerState.IDLE
            );
        }

        this.sprite.position.copyFrom(this.position);
        this.sprite.scale.x = this.direction * Math.abs(this.sprite.scale.x);
    }

    setMovementVector(dx: number, dy: number) {
        this.movementVector.dx = dx;
        this.movementVector.dy = dy;
        this.isMoving = dx !== 0 || dy !== 0;
    }

    setDirection(direction: -1 | 1) {
        this.direction = direction;
    }

    performAttack() {
        this.movementVector.dx = 0;
        this.movementVector.dy = 0;
        this.isMoving = false;

        this.animationController.setState(AnimationPlayerState.ATTACKING);
    }

    /**
     * Установка начальной позиции (при первом появлении игрока).
     * Добавляет два идентичных снимка чтобы интерполяция сразу работала.
     */
    syncPosition(virtualX: number, virtualY: number) {
        this.virtualPosition.x = virtualX;
        this.virtualPosition.y = virtualY;

        if (this.coordinateConverter) {
            const screenPos = this.coordinateConverter.virtualToScreen(virtualX, virtualY);
            this.position.x = screenPos.x;
            this.position.y = screenPos.y;
        }

        this.sprite.position.copyFrom(this.position);

        // Заполняем буфер снимков начальной позицией чтобы интерполяция
        // сразу имела данные и игрок был виден
        const now = performance.now();
        this.snapshots = [
            { time: now - this.interpolationDelayMs - 50, x: virtualX, y: virtualY },
            { time: now - this.interpolationDelayMs,      x: virtualX, y: virtualY },
        ];

        this.lastSnapshotTimeMs = now;
    }
}

export class PlayerManager {
    private remotePlayers: Map<string, RemotePlayer> = new Map();
    private playerContainer: Container;
    private networkManager: NetworkManager;
    private coordinateConverter: CoordinateConverter;
    private movementController: any = null;

    constructor(
        playerContainer: Container,
        networkManager: NetworkManager,
        coordinateConverter: CoordinateConverter
    ) {
        this.playerContainer = playerContainer;
        this.networkManager = networkManager;
        this.coordinateConverter = coordinateConverter;

        this.setupNetworkCallbacks();

        this.processExistingPlayers();
    }

    private setupNetworkCallbacks() {
        this.networkManager.onPlayerJoined(async (player) => {
            if (player.id === this.networkManager.getPlayerId()) return;
            await this.addRemotePlayer(player);
        });

        this.networkManager.onPlayerLeft((playerId) => {
            this.removeRemotePlayer(playerId);
        });

        this.networkManager.onPlayerMovement((playerId, dx, dy) => {
            const currentPlayerId = this.networkManager.getPlayerId();

            if (playerId === currentPlayerId) {
                return;
            }

            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.setMovementVector(dx, dy);
            } else {

            }
        });

        this.networkManager.onPlayerDirection((playerId, direction) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.setDirection(direction);
            } else {

            }
        });

        this.networkManager.onPlayerAttack((playerId, _position) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.performAttack();
            }
        });

        this.networkManager.onGameState(async (players, stateSequence) => {
            const currentPlayerId = this.networkManager.getPlayerId();

            for (const [playerId, playerState] of Object.entries(players)) {

                if (playerId === currentPlayerId) {
                    // Local player position is managed exclusively by client-side prediction
                    // and ACK-based reconciliation. Do NOT overwrite it from gameState —
                    // server position lags 1+ tick behind and causes jitter.
                    continue;
                }

                const existingPlayer = this.remotePlayers.get(playerId);

                if (existingPlayer) {
                    // Entity interpolation: добавляем снимок в буфер,
                    // позиция будет плавно интерполирована в update()
                    existingPlayer.pushSnapshot(playerState.position.x, playerState.position.y, stateSequence);

                    existingPlayer.direction = playerState.direction;
                    existingPlayer.isMoving = playerState.moving;
                    existingPlayer.setMovementVector(
                        playerState.vx ?? 0,
                        playerState.vy ?? 0
                    );
                } else {
                    await this.addRemotePlayer(playerState);
                }
            }

            for (const playerId of this.remotePlayers.keys()) {
                if (playerId !== currentPlayerId && !players[playerId]) {
                    this.removeRemotePlayer(playerId);
                }
            }
        });
    }

    async addRemotePlayer(playerState: PlayerState) {

        if (this.remotePlayers.has(playerState.id)) {
            return;
        }

        const characterVisual = await SpriteLoader.loadCharacterVisual(
            "/assets/16x16_knight_2_v3.png"
        );

        const screenPos = this.coordinateConverter.virtualToScreen(
            playerState.position.x,
            playerState.position.y
        );
        const position = new Point(screenPos.x, screenPos.y);

        const remotePlayer = new RemotePlayer(
            playerState.id,
            position,
            characterVisual,
            this.coordinateConverter,
            playerState.position
        );

        remotePlayer.direction = playerState.direction;
        remotePlayer.isMoving = playerState.moving;

        if (playerState.movementVector) {
            remotePlayer.setMovementVector(
                playerState.movementVector.dx,
                playerState.movementVector.dy
            );
        }

        this.playerContainer.addChild(remotePlayer.sprite);

        this.remotePlayers.set(playerState.id, remotePlayer);

    }

    setMovementController(movementController: any) {
        this.movementController = movementController;
    }

    /**
     * Обновить позиции всех игроков при изменении размеров экрана
     */
    updateAllPlayerPositions(): void {
        if (this.movementController) {
            const currentVirtualPos = this.movementController.getVirtualPosition();
            const newScreenPos = this.coordinateConverter.virtualToScreen(currentVirtualPos.x, currentVirtualPos.y);
            this.movementController.position.set(newScreenPos.x, newScreenPos.y);
        }

        for (const [, remotePlayer] of this.remotePlayers.entries()) {
            const screenPos = this.coordinateConverter.virtualToScreen(
                remotePlayer.virtualPosition.x,
                remotePlayer.virtualPosition.y
            );
            remotePlayer.position.x = screenPos.x;
            remotePlayer.position.y = screenPos.y;
            remotePlayer.sprite.position.copyFrom(remotePlayer.position);
        }
    }

    removeRemotePlayer(playerId: string) {
        const player = this.remotePlayers.get(playerId);

        if (player) {
            this.playerContainer.removeChild(player.sprite);
            this.remotePlayers.delete(playerId);
        }
    }

    update(deltaTime: number) {
        for (const player of this.remotePlayers.values()) {
            player.update(deltaTime);
        }
    }

    private async processExistingPlayers() {
        const existingPlayers = this.networkManager.getPlayers();
        const currentPlayerId = this.networkManager.getPlayerId();

        for (const [playerId, playerState] of Object.entries(existingPlayers)) {
            if (playerId === currentPlayerId) continue;

            await this.addRemotePlayer(playerState);
        }
    }
}
