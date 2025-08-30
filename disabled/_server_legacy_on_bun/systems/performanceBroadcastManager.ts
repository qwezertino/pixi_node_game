/**
 * High-performance broadcasting system for 10k+ players
 * Optimized for full viewport visibility without grid restrictions
 */

import { ServerWebSocket } from "bun";
import { BinaryProtocol } from "../../protocol/binaryProtocol";

interface QueuedBroadcast {
    message: Uint8Array;
    targets: Set<string>;
    priority: 'high' | 'normal' | 'low';
    timestamp: number;
}

interface BroadcastStats {
    messagesPerSecond: number;
    avgTargetsPerMessage: number;
    queueLength: number;
    droppedMessages: number;
}

/**
 * Optimized broadcasting system that scales to 10k+ players
 * Key optimizations:
 * 1. Message deduplication and batching
 * 2. Priority-based queue processing
 * 3. Rate limiting to prevent overwhelming clients
 * 4. Connection pooling and caching
 */
export class PerformanceBroadcastManager {
    private connections = new Map<string, ServerWebSocket<any>>();
    private broadcastQueue: QueuedBroadcast[] = [];
    private messageCache = new Map<string, Uint8Array>(); // Cache encoded messages

    // Performance optimizations
    private readonly MAX_QUEUE_SIZE = 10000;
    private readonly BATCH_SIZE = 500; // Process 500 messages per batch
    private readonly BATCH_INTERVAL = 16; // ~60 FPS processing
    private readonly MAX_MESSAGES_PER_CLIENT_PER_BATCH = 10;

    // Statistics
    private stats: BroadcastStats = {
        messagesPerSecond: 0,
        avgTargetsPerMessage: 0,
        queueLength: 0,
        droppedMessages: 0
    };

    private messageCount = 0;
    private lastStatsReset = Date.now();

    constructor() {
        // Process broadcast queue in batches
        setInterval(() => {
            this.processBroadcastQueue();
        }, this.BATCH_INTERVAL);

        // Update statistics
        setInterval(() => {
            this.updateStats();
        }, 1000);
    }

    public registerConnection(playerId: string, connection: ServerWebSocket<any>): void {
        this.connections.set(playerId, connection);
    }

    public unregisterConnection(playerId: string): void {
        this.connections.delete(playerId);

        // Clean up any queued messages for this player
        this.broadcastQueue = this.broadcastQueue.filter(broadcast =>
            !broadcast.targets.has(playerId)
        );
    }

    /**
     * Broadcast to specific players with viewport-based targeting
     */
    public broadcastToViewport(
        message: any,
        visibleToPlayers: Set<string>,
        priority: 'high' | 'normal' | 'low' = 'normal',
        excludePlayer?: string
    ): void {
        if (visibleToPlayers.size === 0) return;

        // Filter out excluded player and invalid connections
        const validTargets = new Set<string>();
        for (const playerId of visibleToPlayers) {
            if (playerId !== excludePlayer && this.connections.has(playerId)) {
                validTargets.add(playerId);
            }
        }

        if (validTargets.size === 0) return;

        // Encode message once and cache it
        const messageKey = this.getMessageKey(message);
        let encodedMessage = this.messageCache.get(messageKey);

        if (!encodedMessage) {
            encodedMessage = this.encodeMessage(message);
            this.messageCache.set(messageKey, encodedMessage);

            // Limit cache size
            if (this.messageCache.size > 1000) {
                const firstKey = this.messageCache.keys().next().value;
                if (firstKey) {
                    this.messageCache.delete(firstKey);
                }
            }
        }

        // Add to broadcast queue
        const queuedBroadcast: QueuedBroadcast = {
            message: encodedMessage,
            targets: validTargets,
            priority,
            timestamp: Date.now()
        };

        this.addToBroadcastQueue(queuedBroadcast);
    }

    /**
     * High-priority immediate broadcast (for critical events)
     */
    public broadcastImmediate(message: any, targets: Set<string>, excludePlayer?: string): void {
        const encodedMessage = this.encodeMessage(message);
        const validTargets = new Set<string>();

        for (const playerId of targets) {
            if (playerId !== excludePlayer && this.connections.has(playerId)) {
                validTargets.add(playerId);
            }
        }

        this.sendToTargets(encodedMessage, validTargets);
    }

    private addToBroadcastQueue(broadcast: QueuedBroadcast): void {
        // Drop old low-priority messages if queue is full
        if (this.broadcastQueue.length >= this.MAX_QUEUE_SIZE) {
            if (broadcast.priority === 'low') {
                this.stats.droppedMessages++;
                return;
            }

            // Remove oldest low-priority message
            for (let i = this.broadcastQueue.length - 1; i >= 0; i--) {
                if (this.broadcastQueue[i].priority === 'low') {
                    this.broadcastQueue.splice(i, 1);
                    this.stats.droppedMessages++;
                    break;
                }
            }
        }

        // Insert based on priority
        if (broadcast.priority === 'high') {
            this.broadcastQueue.unshift(broadcast);
        } else {
            this.broadcastQueue.push(broadcast);
        }
    }

    private processBroadcastQueue(): void {
        if (this.broadcastQueue.length === 0) return;

        const batch = this.broadcastQueue.splice(0, this.BATCH_SIZE);
        const clientMessageCount = new Map<string, number>();

        // Group messages by target for efficient sending
        const targetMessages = new Map<string, Uint8Array[]>();

        for (const broadcast of batch) {
            for (const target of broadcast.targets) {
                // Rate limiting per client
                const currentCount = clientMessageCount.get(target) || 0;
                if (currentCount >= this.MAX_MESSAGES_PER_CLIENT_PER_BATCH) {
                    continue;
                }

                if (!targetMessages.has(target)) {
                    targetMessages.set(target, []);
                }
                targetMessages.get(target)!.push(broadcast.message);
                clientMessageCount.set(target, currentCount + 1);
            }
        }

        // Send batched messages
        for (const [playerId, messages] of targetMessages) {
            const connection = this.connections.get(playerId);
            if (connection && connection.readyState === 1) {
                try {
                    // Send messages in sequence to maintain order
                    for (const message of messages) {
                        connection.send(message);
                        this.messageCount++;
                    }
                } catch (error) {
                    console.warn(`Failed to send to ${playerId}:`, error);
                    // Connection might be dead, will be cleaned up by connection manager
                }
            }
        }
    }

    private sendToTargets(message: Uint8Array, targets: Set<string>): void {
        for (const playerId of targets) {
            const connection = this.connections.get(playerId);
            if (connection && connection.readyState === 1) {
                try {
                    connection.send(message);
                    this.messageCount++;
                } catch (error) {
                    console.warn(`Failed to send immediate message to ${playerId}:`, error);
                }
            }
        }
    }

    private encodeMessage(message: any): Uint8Array {
        // Route to appropriate encoder based on message type
        switch (message.type) {
            case 'playerMovement':
                return BinaryProtocol.encodePlayerMovement(message);
            case 'playerDirection':
                return BinaryProtocol.encodePlayerDirection(message);
            case 'playerAttack':
                return BinaryProtocol.encodePlayerAttack(message);
            case 'playerJoined':
                return BinaryProtocol.encodePlayerJoined(message);
            case 'playerLeft':
                return BinaryProtocol.encodePlayerLeft(message);
            case 'gameState':
                return BinaryProtocol.encodeGameState(message);
            case 'movementAck':
                return BinaryProtocol.encodeMovementAcknowledgment(message);
            case 'correction':
                return BinaryProtocol.encodeCorrection(message);
            default:
                // Fallback to JSON for unknown types
                console.warn(`Unknown message type for encoding: ${message.type}`);
                return new TextEncoder().encode(JSON.stringify(message));
        }
    }

    private getMessageKey(message: any): string {
        // Create a cache key based on message content
        switch (message.type) {
            case 'playerMovement':
                return `move_${message.playerId}_${message.movementVector.dx}_${message.movementVector.dy}`;
            case 'playerDirection':
                return `dir_${message.playerId}_${message.direction}`;
            case 'playerAttack':
                return `atk_${message.playerId}_${message.position.x}_${message.position.y}`;
            default:
                return `${message.type}_${JSON.stringify(message)}`;
        }
    }

    private updateStats(): void {
        const now = Date.now();
        const elapsed = (now - this.lastStatsReset) / 1000;

        this.stats.messagesPerSecond = this.messageCount / elapsed;
        this.stats.queueLength = this.broadcastQueue.length;

        // Calculate average targets per message from recent broadcasts
        let totalTargets = 0;
        let broadcastCount = 0;
        for (const broadcast of this.broadcastQueue.slice(-100)) {
            totalTargets += broadcast.targets.size;
            broadcastCount++;
        }
        this.stats.avgTargetsPerMessage = broadcastCount > 0 ? totalTargets / broadcastCount : 0;

        // Reset counters
        this.messageCount = 0;
        this.lastStatsReset = now;
    }

    public getStats(): BroadcastStats {
        return { ...this.stats };
    }

    public getConnectionCount(): number {
        return this.connections.size;
    }

    // Emergency function to clear queues if system is overwhelmed
    public emergencyClearQueue(): void {
        const queueSize = this.broadcastQueue.length;
        this.broadcastQueue = this.broadcastQueue.filter(b => b.priority === 'high');
        this.stats.droppedMessages += queueSize - this.broadcastQueue.length;
        console.warn(`🚨 Emergency queue clear: dropped ${queueSize - this.broadcastQueue.length} messages`);
    }
}
