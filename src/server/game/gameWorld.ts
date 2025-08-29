/**
 * High-performance GameWorld optimized for 10k+ players
 * Maintains full viewport visibility with advanced performance optimizations
 */

import { ServerWebSocket } from "bun";
import { PlayerState, PlayerPosition, TICK_RATE } from "../../protocol/messages";
import { WORLD, MOVEMENT } from "../../common/gameSettings";
import { PerformanceVisibilityManager } from "../systems/performanceVisibilityManager";
import { PerformanceBroadcastManager } from "../systems/performanceBroadcastManager";

interface PerformanceMetrics {
    tickTime: number;
    avgTickTime: number;
    maxTickTime: number;
    playersCount: number;
    messagesPerSecond: number;
    visibilityStats: any;
    broadcastStats: any;
}

export class OptimizedGameWorld {
    private players: Map<string, PlayerState> = new Map();
    private connections: Map<string, ServerWebSocket<any>> = new Map();

    // High-performance systems
    private visibilityManager = new PerformanceVisibilityManager();
    private broadcastManager = new PerformanceBroadcastManager();

    // Performance tracking
    private performanceMetrics: PerformanceMetrics = {
        tickTime: 0,
        avgTickTime: 0,
        maxTickTime: 0,
        playersCount: 0,
        messagesPerSecond: 0,
        visibilityStats: {},
        broadcastStats: {}
    };

    private tickTimes: number[] = [];
    private readonly MAX_TICK_HISTORY = 100;
    private lastPerformanceReport = Date.now();

    constructor() {
        this.startGameLoop();
        this.startPerformanceMonitoring();
    }

    private startGameLoop(): void {
        const tickMs = 1000 / TICK_RATE;

        setInterval(() => {
            const startTime = performance.now();
            this.update();
            const endTime = performance.now();

            this.trackPerformance(endTime - startTime);
        }, tickMs);
    }

    private startPerformanceMonitoring(): void {
        setInterval(() => {
            this.reportPerformanceStats();
        }, 10000); // Report every 10 seconds
    }

    private update(): void {
        // Optimized movement update - batch position changes
        const positionUpdates: { playerId: string; oldPos: PlayerPosition; newPos: PlayerPosition }[] = [];

        for (const [playerId, playerState] of this.players.entries()) {
            const shouldMove = playerState.moving && playerState.movementVector && !playerState.attacking;

            if (shouldMove) {
                const oldPosition = { ...playerState.position };
                const { dx, dy } = playerState.movementVector!;
                const moveDistance = MOVEMENT.PLAYER_SPEED_PER_TICK;

                if (dx !== 0) {
                    playerState.position.x += dx * moveDistance;
                }
                if (dy !== 0) {
                    playerState.position.y += dy * moveDistance;
                }

                // Apply world boundaries
                const clampedX = Math.max(WORLD.BOUNDARIES.MIN_X,
                    Math.min(WORLD.BOUNDARIES.MAX_X, playerState.position.x));
                const clampedY = Math.max(WORLD.BOUNDARIES.MIN_Y,
                    Math.min(WORLD.BOUNDARIES.MAX_Y, playerState.position.y));

                if (clampedX !== playerState.position.x || clampedY !== playerState.position.y) {
                    playerState.position.x = clampedX;
                    playerState.position.y = clampedY;
                }

                // Track position updates for batch processing
                if (oldPosition.x !== playerState.position.x || oldPosition.y !== playerState.position.y) {
                    positionUpdates.push({
                        playerId,
                        oldPos: oldPosition,
                        newPos: { ...playerState.position }
                    });
                }
            }
        }

        // Batch update visibility system
        if (positionUpdates.length > 0) {
            this.processBatchPositionUpdates(positionUpdates);
        }
    }

    private processBatchPositionUpdates(updates: { playerId: string; oldPos: PlayerPosition; newPos: PlayerPosition }[]): void {
        // Update visibility manager with new positions
        for (const update of updates) {
            this.visibilityManager.updatePlayerPosition(
                update.playerId,
                update.newPos.x,
                update.newPos.y
            );
        }

        // Batch broadcast movement updates
        for (const update of updates) {
            const player = this.players.get(update.playerId);
            if (player && player.movementVector) {
                const visibleToPlayers = this.visibilityManager.getPlayersWhoCanSee(update.playerId);

                if (visibleToPlayers.size > 0) {
                    const moveMsg = {
                        type: 'playerMovement' as const,
                        playerId: update.playerId,
                        movementVector: player.movementVector,
                        position: update.newPos // Include position for client prediction
                    };

                    this.broadcastManager.broadcastToViewport(
                        moveMsg,
                        visibleToPlayers,
                        'normal',
                        update.playerId
                    );
                }
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

        // Register with performance systems
        this.broadcastManager.registerConnection(playerId, ws);
        this.visibilityManager.addPlayer(playerId, spawnPosition.x, spawnPosition.y, 1920, 1080);

        // Broadcast to players who can see the new player
        this.broadcastPlayerJoined(playerState);

        return playerState;
    }

    public removePlayer(playerId: string): void {
        this.players.delete(playerId);
        this.connections.delete(playerId);

        // Unregister from performance systems
        this.broadcastManager.unregisterConnection(playerId);
        this.visibilityManager.removePlayer(playerId);

        this.broadcastPlayerLeft(playerId);
    }

    public updatePlayerMovement(playerId: string, dx: number, dy: number, inputSequence?: number): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        if (inputSequence !== undefined) {
            player.inputSequence = inputSequence;
        }

        const wasMoving = player.moving;
        const oldMovementVector = { ...player.movementVector };

        if (dx !== 0 || dy !== 0) {
            player.movementVector = { dx, dy };
            player.moving = true;
        } else {
            player.moving = false;
            player.movementVector = { dx: 0, dy: 0 };
        }

        // Broadcast movement change (including stops) to visible players
        const movementChanged = wasMoving !== player.moving ||
                               oldMovementVector.dx !== player.movementVector.dx ||
                               oldMovementVector.dy !== player.movementVector.dy;

        if (movementChanged) {
            const visibleToPlayers = this.visibilityManager.getPlayersWhoCanSee(playerId);

            if (visibleToPlayers.size > 0) {
                const moveMsg = {
                    type: 'playerMovement' as const,
                    playerId,
                    movementVector: player.movementVector,
                    position: player.position
                };

                this.broadcastManager.broadcastToViewport(
                    moveMsg,
                    visibleToPlayers,
                    'normal',
                    playerId
                );
            }
        }

        // Send immediate acknowledgment to moving player
        if (inputSequence !== undefined) {
            this.sendMovementAcknowledgment(playerId, player.position, inputSequence);
        }

        return true;
    }

    public updatePlayerDirection(playerId: string, direction: -1 | 1): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        player.direction = direction;

        // Broadcast direction change to visible players
        const visibleToPlayers = this.visibilityManager.getPlayersWhoCanSee(playerId);

        if (visibleToPlayers.size > 0) {
            const dirMsg = {
                type: 'playerDirection' as const,
                playerId,
                direction
            };

            this.broadcastManager.broadcastToViewport(
                dirMsg,
                visibleToPlayers,
                'normal',
                playerId
            );
        }

        return true;
    }

    public handlePlayerAttack(playerId: string): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        player.attacking = true;

        // Broadcast attack to visible players
        const visibleToPlayers = this.visibilityManager.getPlayersWhoCanSee(playerId);

        if (visibleToPlayers.size > 0) {
            const attackMsg = {
                type: 'playerAttack' as const,
                playerId,
                position: player.position
            };

            this.broadcastManager.broadcastToViewport(
                attackMsg,
                visibleToPlayers,
                'high', // High priority for attacks
                playerId
            );
        }

        return true;
    }

    public handleAttackEnd(playerId: string): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        player.attacking = false;
        return true;
    }

    public updatePlayerViewport(playerId: string, width: number, height: number): void {
        this.visibilityManager.updatePlayerViewport(playerId, width, height);
    }

    private broadcastPlayerJoined(playerState: PlayerState): void {
        // Get players who can see the new player
        const visibleToPlayers = this.visibilityManager.getPlayersWhoCanSee(playerState.id);


        if (visibleToPlayers.size > 0) {
            const joinedMsg = {
                type: 'playerJoined' as const,
                player: playerState
            };

            this.broadcastManager.broadcastToViewport(
                joinedMsg,
                visibleToPlayers,
                'high', // High priority for join events
                playerState.id
            );
        }
    }

    private broadcastPlayerLeft(playerId: string): void {
        // Broadcast to all players since we need to clean up their client state
        const allPlayers = new Set(this.players.keys());

        if (allPlayers.size > 0) {
            const leftMsg = {
                type: 'playerLeft' as const,
                playerId
            };

            this.broadcastManager.broadcastToViewport(
                leftMsg,
                allPlayers,
                'high' // High priority for cleanup
            );
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

        // Send acknowledgment immediately to this specific player
        this.broadcastManager.broadcastImmediate(ackMsg, new Set([playerId]));
    }

    public sendCorrectionToPlayer(playerId: string): void {
        const player = this.players.get(playerId);
        if (!player) return;

        const correctionMsg = {
            type: 'correction' as const,
            playerId,
            position: player.position
        };

        this.broadcastManager.broadcastImmediate(correctionMsg, new Set([playerId]));
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

    private trackPerformance(tickTime: number): void {
        this.tickTimes.push(tickTime);
        if (this.tickTimes.length > this.MAX_TICK_HISTORY) {
            this.tickTimes.shift();
        }

        this.performanceMetrics.tickTime = tickTime;
        this.performanceMetrics.maxTickTime = Math.max(this.performanceMetrics.maxTickTime, tickTime);
        this.performanceMetrics.avgTickTime = this.tickTimes.reduce((a, b) => a + b, 0) / this.tickTimes.length;
        this.performanceMetrics.playersCount = this.players.size;
    }

    private reportPerformanceStats(): void {
        const now = Date.now();
        const uptime = Math.floor((now - this.lastPerformanceReport) / 1000);

        this.performanceMetrics.visibilityStats = this.visibilityManager.getPerformanceStats();
        this.performanceMetrics.broadcastStats = this.broadcastManager.getStats();

        const memUsage = process.memoryUsage();

        console.log(`🔥 Performance Stats (Uptime: ${uptime}s):`);
        console.log(`   📊 Players: ${this.performanceMetrics.playersCount}`);
        console.log(`   📨 Messages/sec: ${this.performanceMetrics.broadcastStats.messagesPerSecond.toFixed(1)}`);
        console.log(`   ⏱️  Tick time: ${this.performanceMetrics.tickTime.toFixed(2)}ms (avg: ${this.performanceMetrics.avgTickTime.toFixed(2)}ms, max: ${this.performanceMetrics.maxTickTime.toFixed(2)}ms)`);
        console.log(`   🧠 Memory: ${(memUsage.rss / 1024 / 1024).toFixed(1)}MB RSS, ${(memUsage.heapUsed / 1024 / 1024).toFixed(1)}MB Heap`);
        console.log(`   👁️  Visibility: ${this.performanceMetrics.visibilityStats.avgVisiblePlayers?.toFixed(1) || 0} avg visible/player, ${this.performanceMetrics.visibilityStats.maxVisiblePlayers || 0} max`);
        console.log(`   🎯 Grid cells: ${this.performanceMetrics.visibilityStats.totalPlayers || 0}`);
        console.log(`   🚦 Rate Limits: Movement ${(this.performanceMetrics.broadcastStats.messagesPerSecond || 0).toFixed(1)}/s, Direction 0.0/s, Attack 0.0/s`);

        // Emergency actions if performance degrades
        if (this.performanceMetrics.avgTickTime > 500) { // 500ms+ tick time is bad
            console.warn(`⚠️  High tick time detected: ${this.performanceMetrics.avgTickTime.toFixed(2)}ms`);
            this.broadcastManager.emergencyClearQueue();
        }

        this.lastPerformanceReport = now;
    }

    public getPerformanceMetrics(): PerformanceMetrics {
        return { ...this.performanceMetrics };
    }
}
