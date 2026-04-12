import { Container, Point, AnimatedSprite, Texture } from "pixi.js";
import { NetworkManager } from "../network/networkManager";
import { PlayerState } from "../network/protocol/messages";
import { SpriteLoader } from "../utils/spriteLoader";
import {
    AnimationController,
    PlayerState as AnimationPlayerState,
} from "../controllers/animationController";
import { PLAYER, MOVEMENT } from "../../shared/gameConfig";
import { CoordinateConverter } from "../utils/coordinateConverter";
//import { LagCompensationSystem } from "../utils/lagCompensation";

// Тип для результата SpriteLoader.loadCharacterVisual после await
type CharacterVisual = {
    animations: Map<string, Texture[]>;
    getAnimation: (name: string) => AnimatedSprite | undefined;
};

// Represents a remote player in the game
class RemotePlayer {
    sprite: AnimatedSprite;
    animationController: AnimationController;
    direction: -1 | 1 = 1;
    movementVector: { dx: number; dy: number } = { dx: 0, dy: 0 };
    isMoving: boolean = false;

    // Позиция в виртуальном мире (целые числа)
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

    update(_deltaTime: number) {
        const isAttacking =
            this.animationController.playerState ===
            AnimationPlayerState.ATTACKING;

        if (!isAttacking && this.isMoving && this.movementVector &&
            (this.movementVector.dx !== 0 || this.movementVector.dy !== 0)) {
            this.animationController.setState(AnimationPlayerState.MOVING);

            const moveDistance = MOVEMENT.playerSpeedPerTick;

            if (this.movementVector.dx !== 0) {
                this.virtualPosition.x += this.movementVector.dx * moveDistance;
            }
            if (this.movementVector.dy !== 0) {
                this.virtualPosition.y += this.movementVector.dy * moveDistance;
            }

            if (this.coordinateConverter) {
                const clampedPos = this.coordinateConverter.clampToVirtualBounds(
                    this.virtualPosition.x, this.virtualPosition.y
                );
                this.virtualPosition.x = clampedPos.x;
                this.virtualPosition.y = clampedPos.y;
            }

            if (this.coordinateConverter) {
                const screenPos = this.coordinateConverter.virtualToScreen(this.virtualPosition.x, this.virtualPosition.y);
                this.position.x = screenPos.x;
                this.position.y = screenPos.y;
            }
        } else if (!isAttacking) {
            this.animationController.setState(AnimationPlayerState.IDLE);
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
     * Установка позиции (упрощенная версия для прямой синхронизации)
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
                console.log("❌ Player not found in remotePlayers:", playerId, "Available players:", Array.from(this.remotePlayers.keys()));
            }
        });

        this.networkManager.onPlayerDirection((playerId, direction) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.setDirection(direction);
            } else {
                console.log("❌ Player not found for direction:", playerId);
            }
        });

        this.networkManager.onPlayerAttack((playerId, _position) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.performAttack();
            }
        });

        this.networkManager.onGameState(async (players) => {
            console.log("🌍 PlayerManager: Game state received", players);
            const currentPlayerId = this.networkManager.getPlayerId();
            console.log("My player ID:", currentPlayerId);

            for (const [playerId, playerState] of Object.entries(players)) {

                if (playerId === currentPlayerId) {
                    // Local player position is managed exclusively by client-side prediction
                    // and ACK-based reconciliation. Do NOT overwrite it from gameState —
                    // server position lags 1+ tick behind and causes jitter.
                    continue;
                }

                const existingPlayer = this.remotePlayers.get(playerId);

                if (existingPlayer) {
                    existingPlayer.virtualPosition.x = playerState.position.x;
                    existingPlayer.virtualPosition.y = playerState.position.y;

                    const screenPos = this.coordinateConverter.virtualToScreen(
                        existingPlayer.virtualPosition.x,
                        existingPlayer.virtualPosition.y
                    );
                    existingPlayer.position.x = screenPos.x;
                    existingPlayer.position.y = screenPos.y;
                    existingPlayer.sprite.position.copyFrom(existingPlayer.position);

                    existingPlayer.direction = playerState.direction;
                    existingPlayer.isMoving = playerState.moving;

                    // Always sync movement vector from gameState vx/vy
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
