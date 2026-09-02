import { Container, Point, AnimatedSprite } from "pixi.js";
import { NetworkManager } from "../network/networkManager";
import { PlayerState, TICK_RATE } from "../network/protocol/messages";
import { CharacterVisual, SpriteLoader } from "../utils/spriteLoader";
import {
    AnimationController,
    PlayerState as AnimationPlayerState,
} from "../controllers/animationController";
import { MOVEMENT, PLAYER } from "../../shared/gameConfig";
import { CoordinateConverter } from "../utils/coordinateConverter";
//import { LagCompensationSystem } from "../utils/lagCompensation";

// Represents a remote player in the game
// Снимок состояния для entity interpolation
interface PositionSnapshot {
    time: number; // performance.now() момент получения от сервера
    x: number;
    y: number;
}

// При 20Hz тик = 50ms. Держим буфер 2 тика (100ms) чтобы
// всегда было 2 снапшота для интерполяции даже при лёгком джиттере.
const MIN_INTERPOLATION_DELAY_MS = 100;
const MAX_INTERPOLATION_DELAY_MS = 300;
const SNAPSHOT_EWMA_ALPHA = 0.15;
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

    /**
     * Advance a player the server deliberately omitted from a frame.
     *
     * Position is a deterministic function of velocity on both sides (`pos += v*speed`
     * per tick, integer), so the server only sends a record when that integration would
     * be wrong — a velocity change, or a world-boundary clamp. For every other player
     * the client reproduces the record the server chose not to send and feeds it to the
     * same snapshot buffer, so the interpolator below is unchanged and cannot tell the
     * difference.
     *
     * Deliberately unclamped: it mirrors the server's prediction, and any clamp the
     * server applied arrives as a real record on the next frame.
     */
    deadReckon(elapsedTicks: number, stateSequence?: number) {
        if (elapsedTicks <= 0) return;

        const last = this.snapshots[this.snapshots.length - 1];
        if (!last) return;

        if (this.movementVector.dx === 0 && this.movementVector.dy === 0) {
            // A stationary player produces the same position every frame. Still push a
            // snapshot: the interpolator needs a steady arrival cadence, and its
            // adaptive delay is driven by that cadence.
            this.pushSnapshot(last.x, last.y, stateSequence);
            return;
        }

        const step = MOVEMENT.playerSpeedPerTick * elapsedTicks;
        this.pushSnapshot(
            last.x + this.movementVector.dx * step,
            last.y + this.movementVector.dy * step,
            stateSequence
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
                // renderTime опережает новейший снимок — буфер 100ms (2 тика × 50ms)
                // делает этот случай редким. Держим последнюю известную позицию:
                // никакой экстраполяции → никакого телепорта-назад при получении снимка.
                this.virtualPosition.x = newest.x;
                this.virtualPosition.y = newest.y;
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
    private pendingPlayers: Map<string, Promise<void>> = new Map();
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
        this.networkManager.onPlayerJoined((player) => {
            if (player.id === this.networkManager.getPlayerId()) return;
            void this.addRemotePlayer(player);
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
            }
        });

        this.networkManager.onPlayerDirection((playerId, direction) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.setDirection(direction);
            }
        });

        this.networkManager.onPlayerAttack((playerId) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.performAttack();
            }
        });

        this.networkManager.onGameState((players, stateSequence, fullState, elapsedTicks) => {
            const currentPlayerId = this.networkManager.getPlayerId();

            // Players the frame omits did not stand still — the server withheld them
            // because their movement was predictable. Reproduce it before applying the
            // records, so both paths land in the buffer with the same arrival time.
            if (!fullState && elapsedTicks > 0) {
                for (const [playerId, remote] of this.remotePlayers) {
                    if (playerId !== currentPlayerId && !players[playerId]) {
                        remote.deadReckon(elapsedTicks, stateSequence);
                    }
                }
            }

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
                    void this.addRemotePlayer(playerState);
                }
            }

            // A delta contains only changed players. Absence means removal only in a full snapshot.
            if (fullState) {
                for (const playerId of this.remotePlayers.keys()) {
                    if (playerId !== currentPlayerId && !players[playerId]) {
                        this.removeRemotePlayer(playerId);
                    }
                }
            }
        });
    }

    async addRemotePlayer(playerState: PlayerState) {
        if (this.remotePlayers.has(playerState.id)) {
            return;
        }

        const pending = this.pendingPlayers.get(playerState.id);
        if (pending) {
            return pending;
        }

        const creation = this.createRemotePlayer(playerState)
            .finally(() => this.pendingPlayers.delete(playerState.id));
        this.pendingPlayers.set(playerState.id, creation);
        return creation;
    }

    private async createRemotePlayer(playerState: PlayerState): Promise<void> {

        const characterVisual = await SpriteLoader.loadCharacterVisual(
            "/assets/16x16_knight_2_v3.png"
        );

        // The player may have left while the shared asset promise was resolving.
        if (this.remotePlayers.has(playerState.id) || !this.networkManager.getPlayers()[playerState.id]) {
            return;
        }

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
            player.sprite.stop();
            player.sprite.destroy();
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

            void this.addRemotePlayer(playerState);
        }
    }
}
