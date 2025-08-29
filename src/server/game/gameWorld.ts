import { ServerWebSocket } from "bun";
import {
    PlayerState,
    PlayerPosition,
    TICK_RATE,
    SYNC_INTERVAL
} from "../../protocol/messages";
import { BinaryProtocol } from "../../protocol/binaryProtocol";
import { WORLD, MOVEMENT } from "../../common/gameSettings";

interface PendingUpdate {
    type: 'movement' | 'direction' | 'attack' | 'attackEnd';
    playerId: string;
    data: any;
}

export class GameWorld {
    private players: Map<string, PlayerState> = new Map();
    private connections: Map<string, ServerWebSocket<any>> = new Map();

    // Batching system
    private pendingUpdates: PendingUpdate[] = [];
    private readonly BATCH_INTERVAL_MS = 16; // ~60 FPS batching

    // Connection pools for efficient broadcasting
    private connectionPool: ServerWebSocket<any>[] = [];
    private connectionPoolValid = false;    constructor() {
        this.startGameLoop();
        this.startBatchedUpdates();

        // Less frequent full sync - only for new connections
        // setInterval(() => {
        //     this.sendFullSync();
        // }, SYNC_INTERVAL);
    }

    private startBatchedUpdates() {
        setInterval(() => {
            this.processBatchedUpdates();
        }, this.BATCH_INTERVAL_MS);
    }

    private invalidateConnectionPool() {
        this.connectionPoolValid = false;
    }

    private getConnectionPool(): ServerWebSocket<any>[] {
        if (!this.connectionPoolValid) {
            this.connectionPool = Array.from(this.connections.values());
            this.connectionPoolValid = true;
        }
        return this.connectionPool;
    }

    private processBatchedUpdates() {
        if (this.pendingUpdates.length === 0) return;

        // Group updates by type for efficient encoding
        const batches = this.groupUpdatesByType(this.pendingUpdates);

        // Send batched updates
        this.sendBatchedUpdates(batches);

        // Clear pending updates
        this.pendingUpdates.length = 0;
    }

    private groupUpdatesByType(updates: PendingUpdate[]): Map<string, PendingUpdate[]> {
        const batches = new Map<string, PendingUpdate[]>();

        for (const update of updates) {
            const key = update.type;
            if (!batches.has(key)) {
                batches.set(key, []);
            }
            batches.get(key)!.push(update);
        }

        return batches;
    }

    private sendBatchedUpdates(batches: Map<string, PendingUpdate[]>) {
        const connections = this.getConnectionPool();

        for (const [updateType, updates] of batches) {
            switch (updateType) {
                case 'movement':
                    this.broadcastBatchedMovements(updates, connections);
                    break;
                case 'direction':
                    this.broadcastBatchedDirections(updates, connections);
                    break;
                case 'attack':
                    this.broadcastBatchedAttacks(updates, connections);
                    break;
            }
        }
    }

    private broadcastBatchedMovements(updates: PendingUpdate[], connections: ServerWebSocket<any>[]) {
        // Pre-encode each movement message once
        const encodedMessages = new Map<string, Uint8Array>();

        for (const update of updates) {
            const msgKey = `${update.playerId}_${update.data.dx}_${update.data.dy}`;
            if (!encodedMessages.has(msgKey)) {
                const moveMsg = {
                    type: 'playerMovement' as const,
                    playerId: update.playerId,
                    movementVector: { dx: update.data.dx, dy: update.data.dy }
                };
                encodedMessages.set(msgKey, BinaryProtocol.encodePlayerMovement(moveMsg));
            }
        }

        // Broadcast each unique message to all relevant connections
        for (const [msgKey, binaryData] of encodedMessages) {
            const playerId = msgKey.split('_')[0];
            this.broadcastToOthers(binaryData, playerId, connections);
        }
    }

    private broadcastBatchedDirections(updates: PendingUpdate[], connections: ServerWebSocket<any>[]) {
        const encodedMessages = new Map<string, Uint8Array>();

        for (const update of updates) {
            const msgKey = `${update.playerId}_${update.data.direction}`;
            if (!encodedMessages.has(msgKey)) {
                const dirMsg = {
                    type: 'playerDirection' as const,
                    playerId: update.playerId,
                    direction: update.data.direction
                };
                encodedMessages.set(msgKey, BinaryProtocol.encodePlayerDirection(dirMsg));
            }
        }

        for (const [msgKey, binaryData] of encodedMessages) {
            const playerId = msgKey.split('_')[0];
            this.broadcastToOthers(binaryData, playerId, connections);
        }
    }

    private broadcastBatchedAttacks(updates: PendingUpdate[], connections: ServerWebSocket<any>[]) {
        const encodedMessages = new Map<string, Uint8Array>();

        for (const update of updates) {
            const msgKey = `${update.playerId}_${update.data.position.x}_${update.data.position.y}`;
            if (!encodedMessages.has(msgKey)) {
                const attackMsg = {
                    type: 'playerAttack' as const,
                    playerId: update.playerId,
                    position: update.data.position
                };
                encodedMessages.set(msgKey, BinaryProtocol.encodePlayerAttack(attackMsg));
            }
        }

        for (const [msgKey, binaryData] of encodedMessages) {
            const playerId = msgKey.split('_')[0];
            this.broadcastToOthers(binaryData, playerId, connections);
        }
    }

    // Optimized broadcast method - single loop through connections
    private broadcastToOthers(binaryData: Uint8Array, excludePlayerId: string, connections: ServerWebSocket<any>[]) {
        for (let i = 0; i < connections.length; i++) {
            const ws = connections[i];
            if (ws.data.playerId !== excludePlayerId && ws.readyState === 1) { // 1 = OPEN
                try {
                    ws.send(binaryData);
                } catch (error) {
                    // Handle failed sends - connection might be closed
                    console.warn('Failed to send to client:', error);
                }
            }
        }
    }

    private startGameLoop() {
        // Fixed time step (32 ticks per second = ~31.25ms per tick)
        const tickMs = 1000 / TICK_RATE;

        setInterval(() => {
            this.update();
        }, tickMs);
    }

    private update() {
        for (const [, playerState] of this.players.entries()) {
            const shouldMove = playerState.moving && playerState.movementVector && !playerState.attacking;

            if (shouldMove) {
                const { dx, dy } = playerState.movementVector!;

                const moveDistance = MOVEMENT.PLAYER_SPEED_PER_TICK;

                if (dx !== 0) {
                    playerState.position.x += dx * moveDistance;
                }
                if (dy !== 0) {
                    playerState.position.y += dy * moveDistance;
                }

                const clampedX = Math.max(WORLD.BOUNDARIES.MIN_X,
                    Math.min(WORLD.BOUNDARIES.MAX_X, playerState.position.x));
                const clampedY = Math.max(WORLD.BOUNDARIES.MIN_Y,
                    Math.min(WORLD.BOUNDARIES.MAX_Y, playerState.position.y));

                if (clampedX !== playerState.position.x || clampedY !== playerState.position.y) {
                    playerState.position.x = clampedX;
                    playerState.position.y = clampedY;
                }
            }
        }
    }

    private sendFullSync() {
        // Only send full sync if we have players and not too many
        // For 10k+ clients, disable periodic full sync to reduce load
        if (this.players.size === 0 || this.players.size > 5000) {
            return;
        }

        const gameStateMsg = {
            type: 'gameState' as const,
            players: Object.fromEntries(this.players.entries()),
            timestamp: Date.now()
        };

        const binaryData = BinaryProtocol.encodeGameState(gameStateMsg);
        const connections = this.getConnectionPool();

        // Stagger sends to avoid overwhelming the network
        let delay = 0;
        const STAGGER_MS = 1; // 1ms between sends

        for (let i = 0; i < connections.length; i++) {
            const ws = connections[i];
            if (ws.readyState === 1) {
                setTimeout(() => {
                    try {
                        ws.send(binaryData);
                    } catch (error) {
                        console.warn('Failed to send full sync:', error);
                    }
                }, delay);
                delay += STAGGER_MS;
            }
        }
    }

    public addPlayer(playerId: string, ws: ServerWebSocket<any>): PlayerState {
        const spawnPosition = this.getRandomSpawnPosition();

        const playerState: PlayerState = {
            id: playerId,
            position: spawnPosition,
            direction: 1,
            moving: false,
            attacking: false
        };

        this.players.set(playerId, playerState);
        this.connections.set(playerId, ws);
        this.invalidateConnectionPool();

        this.broadcastPlayerJoined(playerState);

        return playerState;
    }

    public removePlayer(playerId: string) {
        this.players.delete(playerId);
        this.connections.delete(playerId);
        this.invalidateConnectionPool();

        this.broadcastPlayerLeft(playerId);
    }

    public updatePlayerMovement(playerId: string, dx: number, dy: number, inputSequence?: number): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        if (inputSequence !== undefined) {
            player.inputSequence = inputSequence;
        }

        if (dx !== 0 || dy !== 0) {
            player.movementVector = {
                dx: dx,
                dy: dy
            };

            player.moving = true;
        } else {
            player.moving = false;
            player.movementVector = { dx: 0, dy: 0 };
        }

        if (inputSequence !== undefined) {
            this.sendMovementAcknowledgment(playerId, player.position, inputSequence);
        }

        // Add to pending updates instead of immediate broadcast
        this.pendingUpdates.push({
            type: 'movement',
            playerId,
            data: { dx, dy }
        });

        return true;
    }

    public updatePlayerDirection(playerId: string, direction: -1 | 1): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        player.direction = direction;

        // Add to pending updates instead of immediate broadcast
        this.pendingUpdates.push({
            type: 'direction',
            playerId,
            data: { direction }
        });

        return true;
    }

    public handlePlayerAttack(playerId: string): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        player.attacking = true;

        // Add to pending updates instead of immediate broadcast
        this.pendingUpdates.push({
            type: 'attack',
            playerId,
            data: { position: player.position }
        });

        return true;
    }

    public handleAttackEnd(playerId: string): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        player.attacking = false;
        return true;
    }

    private broadcastPlayerJoined(playerState: PlayerState) {
        const joinedMsg = {
            type: 'playerJoined' as const,
            player: playerState
        };

        const binaryData = BinaryProtocol.encodePlayerJoined(joinedMsg);
        const connections = this.getConnectionPool();

        // Broadcast immediately for join events (important for visibility)
        for (let i = 0; i < connections.length; i++) {
            const ws = connections[i];
            if (ws.data.playerId !== playerState.id && ws.readyState === 1) {
                try {
                    ws.send(binaryData);
                } catch (error) {
                    console.warn('Failed to send player joined message:', error);
                }
            }
        }
    }

    private sendMovementAcknowledgment(playerId: string, position: PlayerPosition, inputSequence: number): void {
        const connection = this.connections.get(playerId);
        if (!connection || connection.readyState !== 1) return;

        const ackMsg = {
            type: 'movementAck' as const,
            playerId,
            acknowledgedPosition: position,
            inputSequence,
            timestamp: Date.now(),
        };

        const binaryData = BinaryProtocol.encodeMovementAcknowledgment(ackMsg);

        try {
            connection.send(binaryData);
        } catch (error) {
            console.warn('Failed to send movement acknowledgment:', error);
        }
    }

    private broadcastPlayerLeft(playerId: string) {
        const leftMsg = {
            type: 'playerLeft' as const,
            playerId
        };

        const binaryData = BinaryProtocol.encodePlayerLeft(leftMsg);
        const connections = this.getConnectionPool();

        // Broadcast immediately for leave events (important for cleanup)
        for (let i = 0; i < connections.length; i++) {
            const ws = connections[i];
            if (ws.readyState === 1) {
                try {
                    ws.send(binaryData);
                } catch (error) {
                    console.warn('Failed to send player left message:', error);
                }
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

        const binaryData = BinaryProtocol.encodeCorrection(correctionMsg);

        try {
            connection.send(binaryData);
        } catch (error) {
            console.warn('Failed to send correction:', error);
        }
    }

    private getRandomSpawnPosition(): PlayerPosition {
        const x = Math.floor(WORLD.SPAWN_AREA.MIN_X +
                           Math.random() * (WORLD.SPAWN_AREA.MAX_X - WORLD.SPAWN_AREA.MIN_X));
        const y = Math.floor(WORLD.SPAWN_AREA.MIN_Y +
                           Math.random() * (WORLD.SPAWN_AREA.MAX_Y - WORLD.SPAWN_AREA.MIN_Y));

        return { x, y };
    }


    public getAllPlayersState(): Record<string, PlayerState> {
        return Object.fromEntries(this.players.entries());
    }


}