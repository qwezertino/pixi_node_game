import { Container, Point, AnimatedSprite, Texture } from "pixi.js";
import { NetworkManager } from "../network/networkManager";
import { PlayerState, TICK_RATE } from "../../protocol/messages";
import { SpriteLoader } from "../utils/spriteLoader";
import { AnimationController, PlayerState as AnimationPlayerState } from "../controllers/animationController";
import { MOVEMENT, PLAYER } from "../../common/gameSettings";

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
    movementVector: { dx: number, dy: number } = { dx: 0, dy: 0 };
    isMoving: boolean = false;

    constructor(
        public id: string,
        public position: Point,
        characterVisual: CharacterVisual,
    ) {
        // Setup player sprite - use the helper method from characterVisual
        this.sprite = characterVisual.getAnimation("idle")!;
        this.sprite.position.copyFrom(position);
        this.sprite.scale.set(PLAYER.BASE_SCALE); // Используем масштаб из настроек
        this.sprite.animationSpeed = PLAYER.ANIMATION_SPEED; // Используем скорость анимации из настроек
        this.sprite.play();

        // Setup animation controller
        this.animationController = new AnimationController(characterVisual.animations, this.sprite);
    }

    update(_deltaTime: number) {
        // Use fixed timestep for consistent movement
        const fixedTimeStep = 1 / TICK_RATE;
        const speed = MOVEMENT.PLAYER_SPEED; // Используем скорость из настроек

        if (this.isMoving && this.movementVector) {
            // Update position based on movement vector
            this.position.x += this.movementVector.dx * speed * fixedTimeStep;
            this.position.y += this.movementVector.dy * speed * fixedTimeStep;

            // Update sprite position
            this.sprite.position.copyFrom(this.position);

            // Set animation state
            this.animationController.setState(AnimationPlayerState.MOVING);
        } else {
            this.animationController.setState(AnimationPlayerState.IDLE);
        }

        // Update sprite direction
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
}

export class PlayerManager {
    private remotePlayers: Map<string, RemotePlayer> = new Map();
    private playerContainer: Container;
    private networkManager: NetworkManager;

    constructor(playerContainer: Container, networkManager: NetworkManager) {
        this.playerContainer = playerContainer;
        this.networkManager = networkManager;

        this.setupNetworkCallbacks();
    }

    private setupNetworkCallbacks() {
        // When a new player joins
        this.networkManager.onPlayerJoined(async (player) => {
            await this.addRemotePlayer(player);
        });

        // When a player leaves
        this.networkManager.onPlayerLeft((playerId) => {
            this.removeRemotePlayer(playerId);
        });

        // When a player moves
        this.networkManager.onPlayerMovement((playerId, dx, dy) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.setMovementVector(dx, dy);
            }
        });

        // When a player changes direction
        this.networkManager.onPlayerDirection((playerId, direction) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.setDirection(direction);
            }
        });

        // When game state is received
        this.networkManager.onGameState(async (players) => {
            // Handle initial game state or full sync
            const currentPlayerId = this.networkManager.getPlayerId();

            for (const [playerId, playerState] of Object.entries(players)) {
                // Skip ourselves
                if (playerId === currentPlayerId) continue;

                const existingPlayer = this.remotePlayers.get(playerId);

                if (existingPlayer) {
                    // Update existing player
                    existingPlayer.position.x = playerState.position.x;
                    existingPlayer.position.y = playerState.position.y;
                    existingPlayer.direction = playerState.direction;
                    existingPlayer.isMoving = playerState.moving;

                    if (playerState.movementVector) {
                        existingPlayer.setMovementVector(
                            playerState.movementVector.dx,
                            playerState.movementVector.dy
                        );
                    }

                    // Update sprite position directly for sync
                    existingPlayer.sprite.position.copyFrom(existingPlayer.position);
                } else {
                    // Add new player
                    await this.addRemotePlayer(playerState);
                }
            }

            // Remove players that no longer exist
            for (const playerId of this.remotePlayers.keys()) {
                if (playerId !== currentPlayerId && !players[playerId]) {
                    this.removeRemotePlayer(playerId);
                }
            }
        });
    }

    async addRemotePlayer(playerState: PlayerState) {
        if (this.remotePlayers.has(playerState.id)) return;

        // Load player visual (could be optimized to reuse assets)
        const characterVisual = await SpriteLoader.loadCharacterVisual("/assets/16x16_knight_2_v3.png");

        // Create position from playerState
        const position = new Point(playerState.position.x, playerState.position.y);

        // Create remote player
        const remotePlayer = new RemotePlayer(
            playerState.id,
            position,
            characterVisual
        );

        // Set initial state
        remotePlayer.direction = playerState.direction;
        remotePlayer.isMoving = playerState.moving;

        if (playerState.movementVector) {
            remotePlayer.setMovementVector(
                playerState.movementVector.dx,
                playerState.movementVector.dy
            );
        }

        // Add to player container
        this.playerContainer.addChild(remotePlayer.sprite);

        // Store in map
        this.remotePlayers.set(playerState.id, remotePlayer);

        console.log(`Remote player joined: ${playerState.id}`);
    }

    removeRemotePlayer(playerId: string) {
        const player = this.remotePlayers.get(playerId);

        if (player) {
            // Remove sprite from container
            this.playerContainer.removeChild(player.sprite);

            // Remove from map
            this.remotePlayers.delete(playerId);

            console.log(`Remote player left: ${playerId}`);
        }
    }

    update(deltaTime: number) {
        // Update all remote players
        for (const player of this.remotePlayers.values()) {
            player.update(deltaTime);
        }
    }
}