import { ServerWebSocket } from "bun";
import {
    PlayerState,
    PlayerPosition,
    TICK_RATE,
    SYNC_INTERVAL
} from "../../protocol/messages";
import { BinaryProtocol } from "../../protocol/binaryProtocol";
import { MOVEMENT, WORLD } from "../../common/gameSettings";

// Используем настройки из файла gameSettings
const PLAYER_SPEED = MOVEMENT.PLAYER_SPEED;

export class GameWorld {
    private players: Map<string, PlayerState> = new Map();
    private connections: Map<string, ServerWebSocket<any>> = new Map();
    private lastTickTime: number = Date.now();
    private tickInterval: Timer | null = null;
    private syncInterval: Timer | null = null;

    constructor() {
        // Start the game loop
        this.startGameLoop();

        // Start the full sync interval
        this.syncInterval = setInterval(() => {
            this.sendFullSync();
        }, SYNC_INTERVAL) as unknown as Timer;
    }

    private startGameLoop() {
        // Fixed time step (32 ticks per second = ~31.25ms per tick)
        const tickMs = 1000 / TICK_RATE;

        this.tickInterval = setInterval(() => {
            const now = Date.now();
            const deltaTime = (now - this.lastTickTime) / 1000; // Convert to seconds
            this.lastTickTime = now;

            this.update(deltaTime);
        }, tickMs) as unknown as Timer;
    }

    private update(deltaTime: number) {
        // Update all player movements based on their movement vectors
        for (const [_, playerState] of this.players.entries()) {
            if (playerState.moving && playerState.movementVector) {
                const { dx, dy } = playerState.movementVector;

                // Calculate the distance to move this tick
                const moveX = dx * PLAYER_SPEED * deltaTime;
                const moveY = dy * PLAYER_SPEED * deltaTime;

                // Update player position
                playerState.position.x += moveX;
                playerState.position.y += moveY;
            }
        }
    }

    private sendFullSync() {
        // Create a full game state snapshot
        const gameStateMsg = {
            type: 'gameState' as const,
            players: Object.fromEntries(this.players.entries()),
            timestamp: Date.now()
        };

        // Encode as binary
        const binaryData = BinaryProtocol.encodeGameState(gameStateMsg);

        // Broadcast to all players
        for (const ws of this.connections.values()) {
            ws.send(binaryData);
        }
    }

    // Player management
    public addPlayer(playerId: string, ws: ServerWebSocket<any>): PlayerState {
        // Create random spawn position
        const spawnPosition = this.getRandomSpawnPosition();

        // Create player state
        const playerState: PlayerState = {
            id: playerId,
            position: spawnPosition,
            direction: 1, // Default facing right
            moving: false
        };

        // Store player and connection
        this.players.set(playerId, playerState);
        this.connections.set(playerId, ws);

        // Notify all existing players about the new player
        this.broadcastPlayerJoined(playerState);

        return playerState;
    }

    public removePlayer(playerId: string) {
        // Remove player from maps
        this.players.delete(playerId);
        this.connections.delete(playerId);

        // Notify all remaining players about the player leaving
        this.broadcastPlayerLeft(playerId);
    }

    // Player movement
    public updatePlayerMovement(playerId: string, dx: number, dy: number): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        // Normalize the movement vector if needed
        const magnitude = Math.sqrt(dx * dx + dy * dy);

        if (magnitude > 0) {
            // Only normalize if there's actual movement
            const normalizedDx = dx / magnitude;
            const normalizedDy = dy / magnitude;

            player.movementVector = {
                dx: normalizedDx,
                dy: normalizedDy
            };

            player.moving = true;
        } else {
            player.moving = false;
            player.movementVector = { dx: 0, dy: 0 };
        }

        // Broadcast movement update to other players
        this.broadcastPlayerMovement(playerId, player.movementVector);

        return true;
    }

    public updatePlayerDirection(playerId: string, direction: -1 | 1): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        player.direction = direction;

        // Broadcast direction change to other players
        this.broadcastPlayerDirection(playerId, direction);

        return true;
    }

    // Handle player attack
    public handlePlayerAttack(playerId: string, position: PlayerPosition): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        // Update player position (could be slightly different due to lag)
        player.position = position;

        // Broadcast attack to other players
        this.broadcastPlayerAttack(playerId, position);

        return true;
    }

    // Broadcasting methods
    private broadcastPlayerJoined(playerState: PlayerState) {
        const joinedMsg = {
            type: 'playerJoined' as const,
            player: playerState
        };

        // Encode as binary
        const binaryData = BinaryProtocol.encodePlayerJoined(joinedMsg);

        // Send to all players except the one who joined
        for (const [id, ws] of this.connections.entries()) {
            if (id !== playerState.id) {
                ws.send(binaryData);
            }
        }
    }

    private broadcastPlayerLeft(playerId: string) {
        const leftMsg = {
            type: 'playerLeft' as const,
            playerId
        };

        // Encode as binary
        const binaryData = BinaryProtocol.encodePlayerLeft(leftMsg);

        // Send to all remaining players
        for (const ws of this.connections.values()) {
            ws.send(binaryData);
        }
    }

    private broadcastPlayerMovement(playerId: string, movementVector: { dx: number; dy: number }) {
        const moveMsg = {
            type: 'playerMovement' as const,
            playerId,
            movementVector
        };

        // Encode as binary
        const binaryData = BinaryProtocol.encodePlayerMovement(moveMsg);

        // Send to all players except the one who moved
        for (const [id, ws] of this.connections.entries()) {
            if (id !== playerId) {
                ws.send(binaryData);
            }
        }
    }

    private broadcastPlayerDirection(playerId: string, direction: -1 | 1) {
        const dirMsg = {
            type: 'playerDirection' as const,
            playerId,
            direction
        };

        // Encode as binary
        const binaryData = BinaryProtocol.encodePlayerDirection(dirMsg);

        // Send to all players except the one who changed direction
        for (const [id, ws] of this.connections.entries()) {
            if (id !== playerId) {
                ws.send(binaryData);
            }
        }
    }

    // Broadcast player attack
    private broadcastPlayerAttack(playerId: string, position: PlayerPosition) {
        const attackMsg = {
            type: 'playerAttack' as const,
            playerId,
            position
        };

        // Encode as binary
        const binaryData = BinaryProtocol.encodePlayerAttack(attackMsg);

        // Send to all players except the attacker
        for (const [id, ws] of this.connections.entries()) {
            if (id !== playerId) {
                ws.send(binaryData);
            }
        }
    }

    public sendCorrectionToPlayer(playerId: string) {
        const player = this.players.get(playerId);
        const connection = this.connections.get(playerId);

        if (!player || !connection) return;

        const correctionMsg = {
            type: 'correction' as const,
            playerId,
            position: player.position
        };

        // Encode as binary
        const binaryData = BinaryProtocol.encodeCorrection(correctionMsg);

        // Send directly to the player who needs correction
        connection.send(binaryData);
    }

    // Helper methods
    private getRandomSpawnPosition(): PlayerPosition {
        // Используем настройки границ для спавна
        return {
            x: WORLD.SPAWN_AREA.MIN_X + Math.random() * (WORLD.SPAWN_AREA.MAX_X - WORLD.SPAWN_AREA.MIN_X),
            y: WORLD.SPAWN_AREA.MIN_Y + Math.random() * (WORLD.SPAWN_AREA.MAX_Y - WORLD.SPAWN_AREA.MIN_Y)
        };
    }

    // Cleanup
    public cleanup() {
        if (this.tickInterval) {
            clearInterval(this.tickInterval as unknown as any);
            this.tickInterval = null;
        }

        if (this.syncInterval) {
            clearInterval(this.syncInterval as unknown as any);
            this.syncInterval = null;
        }
    }

    // Get all game state for a new player
    public getAllPlayersState(): Record<string, PlayerState> {
        return Object.fromEntries(this.players.entries());
    }
}