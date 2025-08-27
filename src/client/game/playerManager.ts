import { Container, Point, AnimatedSprite, Texture } from "pixi.js";
import { NetworkManager } from "../network/networkManager";
import { PlayerState, TICK_RATE } from "../../protocol/messages";
import { SpriteLoader } from "../utils/spriteLoader";
import {
    AnimationController,
    PlayerState as AnimationPlayerState,
} from "../controllers/animationController";
import { PLAYER, MOVEMENT } from "../../common/gameSettings";
import { CoordinateConverter } from "../utils/coordinateConverter";

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

    // Remote players don't need interpolation - they use server-authoritative positions directly
    private worldPosition: Point; // Position in world coordinates
    private coordinateConverter: CoordinateConverter | null = null;

    // Prediction limiting for remote players
    private predictionFrames: number = 0;
    private lastMovementTime: number = 0;

    constructor(
        public id: string,
        public position: Point, // This is screen position
        characterVisual: CharacterVisual,
        coordinateConverter?: CoordinateConverter,
        worldPosition?: { x: number; y: number } // Server's world position
    ) {
        this.coordinateConverter = coordinateConverter || null;
        // Initialize world position from server data with discrete rounding
        if (worldPosition) {
            // Immediately round to discrete coordinates to match server
            console.log(`🎯 [CLIENT] RemotePlayer ${id} init: server pos (${worldPosition.x.toFixed(2)}, ${worldPosition.y.toFixed(2)}) -> discrete (${Math.round(worldPosition.x)}, ${Math.round(worldPosition.y)})`);
            this.worldPosition = new Point(Math.round(worldPosition.x), Math.round(worldPosition.y));
        } else {
            // Fallback: convert screen position back to world position
            if (this.coordinateConverter) {
                const worldPos = this.coordinateConverter.screenToWorld(
                    position.x,
                    position.y
                );
                console.log(`🎯 [CLIENT] RemotePlayer ${id} init fallback: screen (${position.x.toFixed(2)}, ${position.y.toFixed(2)}) -> world (${worldPos.x.toFixed(2)}, ${worldPos.y.toFixed(2)}) -> discrete (${Math.round(worldPos.x)}, ${Math.round(worldPos.y)})`);
                this.worldPosition = new Point(Math.round(worldPos.x), Math.round(worldPos.y));
            } else {
                console.log(`🎯 [CLIENT] RemotePlayer ${id} init raw: (${position.x.toFixed(2)}, ${position.y.toFixed(2)}) -> discrete (${Math.round(position.x)}, ${Math.round(position.y)})`);
                this.worldPosition = new Point(Math.round(position.x), Math.round(position.y));
            }
        }
        // Setup player sprite - use the helper method from characterVisual
        this.sprite = characterVisual.getAnimation("idle")!;

        // Update screen position based on discrete world position
        if (this.coordinateConverter) {
            const screenPos = this.coordinateConverter.worldToScreen(this.worldPosition.x, this.worldPosition.y);
            this.position.x = screenPos.x;
            this.position.y = screenPos.y;
            console.log(`📺 [CLIENT] RemotePlayer ${id} init screen: discrete world (${this.worldPosition.x}, ${this.worldPosition.y}) -> screen (${screenPos.x.toFixed(2)}, ${screenPos.y.toFixed(2)})`);
            this.sprite.position.copyFrom(this.position);
        } else {
            this.sprite.position.copyFrom(position);
        }

        this.sprite.scale.set(PLAYER.BASE_SCALE); // Используем масштаб из настроек
        this.sprite.animationSpeed = PLAYER.ANIMATION_SPEED; // Используем скорость анимации из настроек
        this.sprite.play();

        // Setup animation controller
        this.animationController = new AnimationController(
            characterVisual.animations,
            this.sprite
        );
    }

        update(_deltaTime: number) {
        // Don't move during attacks - animation controller handles state blocking
        const isAttacking =
            this.animationController.playerState ===
            AnimationPlayerState.ATTACKING;

        // Set animation state based on movement (but only if not attacking)
        if (!isAttacking && this.isMoving && this.movementVector) {
            this.animationController.setState(AnimationPlayerState.MOVING);

            // REMOTE PLAYER PREDICTION LIMITING
            // Continue moving BUT only for limited frames to prevent massive desync
            const maxPredictionFrames = 5; // Allow slightly more for remote players
            const maxPredictionTime = 200; // Max 200ms prediction
            const timeSinceLastUpdate = Date.now() - this.lastMovementTime;

            if (this.predictionFrames < maxPredictionFrames && timeSinceLastUpdate < maxPredictionTime) {
                // Use FIXED tick time same as server: 1/TICK_RATE seconds per tick
                const tickSeconds = 1 / TICK_RATE;  // Fixed: 1/32 = 0.03125 seconds per tick
                const moveDistance = MOVEMENT.PLAYER_SPEED * tickSeconds;
                const prevWorldX = this.worldPosition.x;
                const prevWorldY = this.worldPosition.y;

                console.log(`🏃 [CLIENT] RemotePlayer ${this.id} moving: dx=${this.movementVector.dx.toFixed(3)}, dy=${this.movementVector.dy.toFixed(3)}, moveDistance=${moveDistance.toFixed(3)}, prediction=${this.predictionFrames}/${maxPredictionFrames}`);

                // Update world position
                if (this.movementVector.dx !== 0) {
                    this.worldPosition.x += (this.movementVector.dx > 0 ? moveDistance : -moveDistance);
                }
                if (this.movementVector.dy !== 0) {
                    this.worldPosition.y += (this.movementVector.dy > 0 ? moveDistance : -moveDistance);
                }

                // Round to discrete positions to match server and protocol
                this.worldPosition.x = Math.round(this.worldPosition.x);
                this.worldPosition.y = Math.round(this.worldPosition.y);

                console.log(`🌍 [CLIENT] RemotePlayer ${this.id} world position: from (${prevWorldX.toFixed(2)}, ${prevWorldY.toFixed(2)}) to discrete (${this.worldPosition.x}, ${this.worldPosition.y})`);

                // Convert world position to screen coordinates
                if (this.coordinateConverter) {
                    const screenPos = this.coordinateConverter.worldToScreen(this.worldPosition.x, this.worldPosition.y);
                    const prevScreenX = this.position.x;
                    const prevScreenY = this.position.y;
                    this.position.x = screenPos.x;
                    this.position.y = screenPos.y;

                    console.log(`📺 [CLIENT] RemotePlayer ${this.id} screen position: from (${prevScreenX.toFixed(2)}, ${prevScreenY.toFixed(2)}) to (${screenPos.x.toFixed(2)}, ${screenPos.y.toFixed(2)})`);
                }

                // Increment prediction counter
                this.predictionFrames++;
            } else {
                console.log(`⏸️ [CLIENT] RemotePlayer ${this.id} prediction limit reached (${this.predictionFrames}/${maxPredictionFrames}, ${timeSinceLastUpdate}ms)`);
            }
        } else if (!isAttacking) {
            // Only set idle if not attacking (animation controller manages attack->idle transition)
            this.animationController.setState(AnimationPlayerState.IDLE);
        }

        // Update sprite position directly
        this.sprite.position.copyFrom(this.position);

        // Update sprite direction
        this.sprite.scale.x = this.direction * Math.abs(this.sprite.scale.x);
    }

    setMovementVector(dx: number, dy: number) {
        this.movementVector.dx = dx;
        this.movementVector.dy = dy;
        this.isMoving = dx !== 0 || dy !== 0;

        // Reset prediction counter when new movement arrives from server
        this.predictionFrames = 0;
        this.lastMovementTime = Date.now();

        console.log(`🎮 [CLIENT] RemotePlayer ${this.id} setMovementVector: dx=${dx}, dy=${dy}, isMoving=${this.isMoving}`);

        // Movement direction is set, continuous movement happens in update() loop
        // This allows smooth movement when holding keys
    }

    setDirection(direction: -1 | 1) {
        this.direction = direction;
    }

    performAttack() {
        // Stop movement when attacking
        this.movementVector.dx = 0;
        this.movementVector.dy = 0;
        this.isMoving = false;

        // Trigger attack animation for remote player using proper state management
        this.animationController.setState(AnimationPlayerState.ATTACKING);
    }

        // Sync position during game state updates (e.g., full sync every 30 seconds)
    syncPosition(worldX: number, worldY: number) {
        const prevWorldX = this.worldPosition.x;
        const prevWorldY = this.worldPosition.y;

                console.log(`🔄 [CLIENT] RemotePlayer ${this.id} sync position: from discrete world (${prevWorldX}, ${prevWorldY}) to discrete (${worldX}, ${worldY})`);

        // Calculate the distance for the sync (should be smaller now with discrete positions)
        const deltaX = worldX - prevWorldX;
        const deltaY = worldY - prevWorldY;
        const syncDistance = Math.sqrt(deltaX * deltaX + deltaY * deltaY);

        console.log(`📏 [CLIENT] RemotePlayer ${this.id} sync distance: ${syncDistance} discrete units`);

        // If the distance is large (indicating a significant teleport), snap immediately
        // Otherwise, use a smoother approach
        if (syncDistance > 50) {  // Large teleport threshold
            console.log(`⚡ [CLIENT] RemotePlayer ${this.id} large sync detected, snapping immediately`);
                    // Update world position from server immediately
        this.worldPosition.x = worldX;
        this.worldPosition.y = worldY;

        // Reset prediction counter since we got server update
        this.predictionFrames = 0;
        this.lastMovementTime = Date.now();

            // Convert to screen coordinates
            if (this.coordinateConverter) {
                const screenPos = this.coordinateConverter.worldToScreen(worldX, worldY);
                this.position.x = screenPos.x;
                this.position.y = screenPos.y;

                console.log(`📺 [CLIENT] RemotePlayer ${this.id} snapped to screen position: (${screenPos.x.toFixed(2)}, ${screenPos.y.toFixed(2)})`);
            } else {
                // Fallback
                console.log(`⚠️ [CLIENT] RemotePlayer ${this.id} using fallback sync (no coordinate converter)`);
                this.position.x = worldX;
                this.position.y = worldY;
            }
        } else {
            console.log(`🎯 [CLIENT] RemotePlayer ${this.id} small sync, smooth update`);
            // For smaller corrections, update world position directly
            // The normal update loop will handle the smooth transition
            this.worldPosition.x = worldX;
            this.worldPosition.y = worldY;

            // Reset prediction counter for small sync too
            this.predictionFrames = 0;
            this.lastMovementTime = Date.now();
        }

        this.sprite.position.copyFrom(this.position);
    }
}

export class PlayerManager {
    private remotePlayers: Map<string, RemotePlayer> = new Map();
    private playerContainer: Container;
    private networkManager: NetworkManager;
    private coordinateConverter: CoordinateConverter;
    private movementController: any = null; // Will be set externally

    constructor(
        playerContainer: Container,
        networkManager: NetworkManager,
        coordinateConverter: CoordinateConverter
    ) {
        this.playerContainer = playerContainer;
        this.networkManager = networkManager;
        this.coordinateConverter = coordinateConverter;

        this.setupNetworkCallbacks();

        // Handle case where initialState was already received before this manager was created
        this.processExistingPlayers();
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
            const currentPlayerId = this.networkManager.getPlayerId();

            // If this is our own movement being echoed back, ignore it
            // (We handle our own movement via client prediction)
            if (playerId === currentPlayerId) {
                console.log(`🔄 [CLIENT] Ignoring own movement echo: dx=${dx}, dy=${dy}`);
                return;
            }

            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.setMovementVector(dx, dy);
            } else {
                console.warn(`Player ${playerId} not found in remote players`);
            }
        });

        // When a player changes direction
        this.networkManager.onPlayerDirection((playerId, direction) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.setDirection(direction);
            }
        });

        // When a player attacks
        this.networkManager.onPlayerAttack((playerId, _position) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.performAttack();
            } else {
                console.warn(`Player ${playerId} not found for attack`);
            }
        });

        // When game state is received
        this.networkManager.onGameState(async (players) => {
            // Handle initial game state or full sync
            const currentPlayerId = this.networkManager.getPlayerId();
            console.log(
                `PlayerManager received gameState with ${
                    Object.keys(players).length
                } players, myId=${currentPlayerId}`
            );

            for (const [playerId, playerState] of Object.entries(players)) {
                console.log(
                    `Processing player: ${playerId} (current: ${currentPlayerId}) - skip: ${
                        playerId === currentPlayerId
                    }`
                );

                // Handle ourselves - apply server correction
                if (playerId === currentPlayerId) {
                    console.log(`🏠 [CLIENT] Processing LOCAL player: server pos (${playerState.position.x}, ${playerState.position.y})`);
                    if (this.movementController) {
                        console.log(`🔄 [CLIENT] Applying server correction for local player: (${playerState.position.x}, ${playerState.position.y})`);
                        this.movementController.handleServerCorrection({
                            x: playerState.position.x,
                            y: playerState.position.y
                        });
                    } else {
                        console.log(`❌ [CLIENT] MovementController not available for server correction`);
                    }
                    continue;
                }

                const existingPlayer = this.remotePlayers.get(playerId);

                if (existingPlayer) {
                    // Update existing player with sync (important for full state updates)
                    existingPlayer.syncPosition(
                        playerState.position.x,
                        playerState.position.y
                    );
                    existingPlayer.direction = playerState.direction;
                    existingPlayer.isMoving = playerState.moving;

                    if (playerState.movementVector) {
                        existingPlayer.setMovementVector(
                            playerState.movementVector.dx,
                            playerState.movementVector.dy
                        );
                    }
                } else {
                    // Add new player
                    console.log(`Adding new remote player: ${playerId}`);
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
        const characterVisual = await SpriteLoader.loadCharacterVisual(
            "/assets/16x16_knight_2_v3.png"
        );

        // Convert world coordinates to screen coordinates
        const screenPos = this.coordinateConverter.worldToScreen(
            playerState.position.x,
            playerState.position.y
        );
        const position = new Point(screenPos.x, screenPos.y);

        // Create remote player
        const remotePlayer = new RemotePlayer(
            playerState.id,
            position,
            characterVisual,
            this.coordinateConverter,
            playerState.position // Pass the world position from server
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

        console.log(`Player joined: ${playerState.id}`);
    }

    // Method to set the movement controller reference
    setMovementController(movementController: any) {
        this.movementController = movementController;
    }

    removeRemotePlayer(playerId: string) {
        const player = this.remotePlayers.get(playerId);

        if (player) {
            // Remove sprite from container
            this.playerContainer.removeChild(player.sprite);

            // Remove from map
            this.remotePlayers.delete(playerId);

            console.log(`Player left: ${playerId}`);
        }
    }

    update(deltaTime: number) {
        // Update all remote players
        for (const player of this.remotePlayers.values()) {
            player.update(deltaTime);
        }
    }

    private async processExistingPlayers() {
        // Check if NetworkManager already has players from initialState
        const existingPlayers = this.networkManager.getPlayers();
        const currentPlayerId = this.networkManager.getPlayerId();

        console.log(
            `Processing existing players: ${
                Object.keys(existingPlayers).length
            } total, myId=${currentPlayerId}`
        );

        for (const [playerId, playerState] of Object.entries(existingPlayers)) {
            // Skip ourselves
            if (playerId === currentPlayerId) continue;

            console.log(`Adding existing player: ${playerId}`);
            await this.addRemotePlayer(playerState);
        }
    }
}
