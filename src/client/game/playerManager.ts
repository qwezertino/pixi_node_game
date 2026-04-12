import { Container, Point, AnimatedSprite, Texture } from "pixi.js";
import { NetworkManager } from "../network/networkManager";
import { PlayerState } from "../network/protocol/messages";
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

// Задержка рендера: 2 тика при 30Hz = ~66ms.
// Клиент всегда рендерит других игроков "в прошлом",
// интерполируя между двумя известными позициями — никаких телепортов.
const INTERPOLATION_DELAY_MS = 100;
const MAX_SNAPSHOTS = 32;

class RemotePlayer {
    sprite: AnimatedSprite;
    animationController: AnimationController;
    direction: -1 | 1 = 1;
    movementVector: { dx: number; dy: number } = { dx: 0, dy: 0 };
    isMoving: boolean = false;

    // Буфер серверных позиций для интерполяции
    private snapshots: PositionSnapshot[] = [];

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
    pushSnapshot(x: number, y: number) {
        const now = performance.now();
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

        // Entity interpolation: рендерим позицию на INTERPOLATION_DELAY_MS в прошлом.
        // Это означает что мы всегда имеем два снимка вокруг целевого времени —
        // никаких экстраполяций и телепортов.
        const renderTime = performance.now() - INTERPOLATION_DELAY_MS;
        const snaps = this.snapshots;

        if (snaps.length >= 2) {
            // Найти два снимка вокруг renderTime
            let newer = snaps[snaps.length - 1];
            let older = snaps[snaps.length - 2];

            for (let i = snaps.length - 1; i >= 1; i--) {
                if (snaps[i - 1].time <= renderTime) {
                    older = snaps[i - 1];
                    newer = snaps[i];
                    break;
                }
            }

            // Интерполируем между older и newer
            const span = newer.time - older.time;
            const t = span > 0 ? Math.min(1, (renderTime - older.time) / span) : 1;
            this.virtualPosition.x = older.x + (newer.x - older.x) * t;
            this.virtualPosition.y = older.y + (newer.y - older.y) * t;

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
            { time: now - INTERPOLATION_DELAY_MS - 50, x: virtualX, y: virtualY },
            { time: now - INTERPOLATION_DELAY_MS,       x: virtualX, y: virtualY },
        ];
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

        this.networkManager.onGameState(async (players) => {
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
                    existingPlayer.pushSnapshot(playerState.position.x, playerState.position.y);

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
