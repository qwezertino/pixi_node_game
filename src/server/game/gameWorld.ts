import { ServerWebSocket } from "bun";
import {
    PlayerState,
    PlayerPosition,
    TICK_RATE,
    SYNC_INTERVAL
} from "../../protocol/messages";
import { BinaryProtocol } from "../../protocol/binaryProtocol";
import { WORLD, MOVEMENT } from "../../common/gameSettings";

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

    private update(_deltaTime: number) {
        // Update all player movements based on their movement vectors
        for (const [playerId, playerState] of this.players.entries()) {
            if (playerState.moving && playerState.movementVector) {
                const { dx, dy } = playerState.movementVector;
                const prevX = playerState.position.x;
                const prevY = playerState.position.y;

                // Use FIXED tick time for consistent movement calculation
                // All clients and server must use the same tick duration
                const tickSeconds = 1 / TICK_RATE;  // Fixed: 1/32 = 0.03125 seconds per tick
                const moveDistance = MOVEMENT.PLAYER_SPEED * tickSeconds;

                console.log(`🎯 [SERVER] Updating position for ${playerId}: moveDistance=${moveDistance.toFixed(3)}, vector=(${dx.toFixed(3)}, ${dy.toFixed(3)})`);

                // Apply movement in virtual world coordinates
                if (dx !== 0) {
                    playerState.position.x += (dx > 0 ? moveDistance : -moveDistance);
                }
                if (dy !== 0) {
                    playerState.position.y += (dy > 0 ? moveDistance : -moveDistance);
                }

                // Round to discrete positions for consistency with Int16 protocol
                playerState.position.x = Math.round(playerState.position.x);
                playerState.position.y = Math.round(playerState.position.y);

                console.log(`📍 [SERVER] Position changed: ${playerId} from (${prevX.toFixed(2)}, ${prevY.toFixed(2)}) to discrete (${playerState.position.x}, ${playerState.position.y})`);

                // Keep player within world boundaries
                playerState.position.x = Math.max(WORLD.BOUNDARIES.MIN_X,
                    Math.min(WORLD.BOUNDARIES.MAX_X, playerState.position.x));
                playerState.position.y = Math.max(WORLD.BOUNDARIES.MIN_Y,
                    Math.min(WORLD.BOUNDARIES.MAX_Y, playerState.position.y));
            }
        }
    }

    private sendFullSync() {
        console.log(`🌍 [SERVER] Sending full sync to ${this.connections.size} players`);

        // Create a full game state snapshot
        const gameStateMsg = {
            type: 'gameState' as const,
            players: Object.fromEntries(this.players.entries()),
            timestamp: Date.now()
        };

        // Log all player positions being sent
        for (const [playerId, playerState] of this.players.entries()) {
            console.log(`📤 [SERVER] Full sync sending player ${playerId}: discrete pos=(${playerState.position.x}, ${playerState.position.y}), moving=${playerState.moving}`);
        }

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

        console.log(`🎯 [SERVER] Spawning player ${playerId} at random position: (${spawnPosition.x.toFixed(2)}, ${spawnPosition.y.toFixed(2)})`);

        // Round spawn position to discrete coordinates
        const discreteSpawnPosition = {
            x: Math.round(spawnPosition.x),
            y: Math.round(spawnPosition.y)
        };

        console.log(`🎯 [SERVER] Rounded spawn position: (${spawnPosition.x.toFixed(2)}, ${spawnPosition.y.toFixed(2)}) -> discrete (${discreteSpawnPosition.x}, ${discreteSpawnPosition.y})`);

        // Create player state
        const playerState: PlayerState = {
            id: playerId,
            position: discreteSpawnPosition,
            direction: 1, // Default facing right
            moving: false
        };

        // Store player and connection
        this.players.set(playerId, playerState);
        this.connections.set(playerId, ws);

        console.log(`✅ [SERVER] Player ${playerId} added with position: (${playerState.position.x.toFixed(2)}, ${playerState.position.y.toFixed(2)})`);

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

        console.log(`🏃 [SERVER] updatePlayerMovement: playerId=${playerId}, input dx=${dx}, dy=${dy}, current world pos=(${player.position.x.toFixed(2)}, ${player.position.y.toFixed(2)})`);

        // DON'T normalize! Keep integer values for consistency with client
        // Client already sends normalized integers (-1, 0, 1)
        if (dx !== 0 || dy !== 0) {
            // Use the exact same integer values as client sends
            console.log(`🔄 [SERVER] Using integer movement: dx=${dx}, dy=${dy} (NO normalization)`);

            player.movementVector = {
                dx: dx,  // Keep original integer values
                dy: dy
            };

            player.moving = true;
        } else {
            console.log(`⏸️ [SERVER] Player stopped moving`);
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

        console.log(`⚔️ [SERVER] Player ${playerId} attack: received pos (${position.x.toFixed(2)}, ${position.y.toFixed(2)}), current server pos (${player.position.x.toFixed(2)}, ${player.position.y.toFixed(2)})`);

        // DON'T update player position from attack - this could overwrite with screen coordinates!
        // Server should maintain authoritative position
        // player.position = position;

        // Broadcast attack to other players using server's authoritative position
        this.broadcastPlayerAttack(playerId, player.position);

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

        console.log(`Broadcasting movement from ${playerId}: dx=${movementVector.dx}, dy=${movementVector.dy}`);

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