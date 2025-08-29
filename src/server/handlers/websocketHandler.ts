/**
 * Optimized WebSocket handler for 10k+ concurrent players
 * Uses high-performance systems while maintaining full viewport visibility
 */

import { ServerWebSocket } from "bun";
import { OptimizedGameWorld } from "../game/gameWorld";
import { BinaryProtocol } from "../../protocol/binaryProtocol";
import { v4 as uuidv4 } from 'uuid';
import { InitialStateMessage } from "../../protocol/messages";

// Create optimized game world instance
const gameWorld = new OptimizedGameWorld();

// WebSocket data interface
interface WebSocketData {
    playerId: string;
    lastActivity: number;
    messageCount: number;
    joinTime: number;
}

// High-performance connection tracking
let totalConnections = 0;
let activeConnections = 0;
const CONNECTION_STATS_INTERVAL = 100; // Report every 100 connections

// Rate limiting to prevent abuse
const RATE_LIMITS = {
    MESSAGES_PER_SECOND: 60,
    BURST_LIMIT: 10,
    WINDOW_MS: 1000
};

const playerRateLimits = new Map<string, {
    messageCount: number;
    windowStart: number;
    burstCount: number;
    lastMessage: number;
}>();

export function handleOptimizedWebSocket() {
    // Rate limiting implementation
    function checkRateLimit(playerId: string, now: number): boolean {
        const limits = playerRateLimits.get(playerId);
        if (!limits) return true;

        // Reset window if needed
        if (now - limits.windowStart > RATE_LIMITS.WINDOW_MS) {
            limits.messageCount = 0;
            limits.windowStart = now;
            limits.burstCount = 0;
        }

        // Check burst limit
        if (now - limits.lastMessage < 100) { // 100ms burst window
            limits.burstCount++;
            if (limits.burstCount > RATE_LIMITS.BURST_LIMIT) {
                return false;
            }
        } else {
            limits.burstCount = 0;
        }

        // Check messages per second limit
        limits.messageCount++;
        limits.lastMessage = now;

        return limits.messageCount <= RATE_LIMITS.MESSAGES_PER_SECOND;
    }

    // Optimized binary message processing
    function handleBinaryMessage(playerId: string, message: Uint8Array): void {
        try {
            const decodedMsg = BinaryProtocol.decodeMessage(message);
            if (!decodedMsg || typeof decodedMsg !== 'object' || !decodedMsg.type) {
                console.warn(`⚠️  Invalid binary message from player ${playerId}`);
                return;
            }

            switch (decodedMsg.type) {
            case 'move':
                const { dx, dy } = decodedMsg.movementVector;
                const inputSequence = decodedMsg.inputSequence || 0;
                gameWorld.updatePlayerMovement(playerId, dx, dy, inputSequence);
                break;

            case 'direction':
                gameWorld.updatePlayerDirection(playerId, decodedMsg.direction);
                break;

            case 'attack':
                gameWorld.handlePlayerAttack(playerId);
                break;

            case 'attackEnd':
                gameWorld.handleAttackEnd(playerId);
                break;

            case 'viewportUpdate':
                // Handle viewport size updates for dynamic visibility
                if (decodedMsg.viewport) {
                    gameWorld.updatePlayerViewport(
                        playerId,
                        decodedMsg.viewport.width,
                        decodedMsg.viewport.height
                    );
                }
                break;

            case 'noop':
                // No operation - used by load testing to send valid messages without action
                break;
        }
        } catch (error) {
            console.error(`⚠️  Binary message processing error for ${playerId}:`, error);
        }
    }

    // Fallback text message processing
    function handleTextMessage(playerId: string, message: string): void {
        let data;
        try {
            data = JSON.parse(message);
        } catch (error) {
            console.warn(`⚠️  Invalid JSON from player ${playerId}:`, message);
            return;
        }

        // Validate that data is an object with a type property
        if (!data || typeof data !== 'object' || !data.type) {
            console.warn(`⚠️  Invalid message format from player ${playerId}:`, data);
            return;
        }

        switch (data.type) {
            case 'move':
                if (data.movementVector) {
                    const { dx, dy } = data.movementVector;
                    const inputSequence = data.inputSequence || 0;
                    gameWorld.updatePlayerMovement(playerId, dx, dy, inputSequence);
                }
                break;

            case 'direction':
                if (data.direction !== undefined) {
                    gameWorld.updatePlayerDirection(playerId, data.direction);
                }
                break;

            case 'attack':
                gameWorld.handlePlayerAttack(playerId);
                break;

            case 'attackEnd':
                gameWorld.handleAttackEnd(playerId);
                break;

            case 'viewportSize':
                // Handle viewport size updates
                if (data.width && data.height) {
                    gameWorld.updatePlayerViewport(playerId, data.width, data.height);
                    console.log(`[WebSocket] Received viewport size from ${playerId}: ${data.width}x${data.height}`);
                }
                break;

            case 'noop':
                // No operation - used by load testing to send valid messages without action
                break;

            default:
                console.warn(`Unknown message type: ${data.type}`);
                break;
        }
    }

    return {
        // Optimized connection handler
        open(ws: ServerWebSocket<WebSocketData>) {
            totalConnections++;
            activeConnections++;

            const playerId = uuidv4();
            const joinTime = Date.now();

            ws.data = {
                playerId,
                lastActivity: joinTime,
                messageCount: 0,
                joinTime
            };

            // Initialize rate limiting
            playerRateLimits.set(playerId, {
                messageCount: 0,
                windowStart: joinTime,
                burstCount: 0,
                lastMessage: 0
            });

            // Optimized connection logging
            if (totalConnections % CONNECTION_STATS_INTERVAL === 0) {
                console.log(`🔗 Connections: ${activeConnections} active, ${totalConnections} total`);
            }

            try {
                // Get existing players efficiently
                const existingPlayers = gameWorld.getAllPlayersState();

                // Add player to optimized game world
                const playerState = gameWorld.addPlayer(playerId, ws);

                // Create optimized initial state
                const initialState: InitialStateMessage = {
                    type: 'initialState',
                    player: playerState,
                    players: existingPlayers,
                    timestamp: joinTime
                };

                // Send binary initial state
                const binaryData = BinaryProtocol.encodeInitialState(initialState);
                ws.send(binaryData);

            } catch (error) {
                console.error(`❌ Failed to initialize player ${playerId}:`, error);
                ws.close();
                activeConnections--;
                playerRateLimits.delete(playerId);
            }
        },

        // High-performance message handler
        message(ws: ServerWebSocket<WebSocketData>, message: string | Uint8Array) {
            const playerId = ws.data.playerId;
            const now = Date.now();

            // Apply rate limiting
            if (!checkRateLimit(playerId, now)) {
                console.warn(`🚫 Rate limit exceeded for player ${playerId}`);
                return; // Drop message
            }

            ws.data.lastActivity = now;
            ws.data.messageCount++;

            try {
                // Prioritize binary messages for performance
                if (message instanceof Uint8Array) {
                    handleBinaryMessage(playerId, message);
                } else {
                    handleTextMessage(playerId, message);
                }
            } catch (error) {
                console.error(`⚠️  Message processing error for ${playerId}:`, error);
                // Send correction to resync client
                gameWorld.sendCorrectionToPlayer(playerId);
            }
        },

        // Optimized disconnection handler
        close(ws: ServerWebSocket<WebSocketData>) {
            const playerId = ws.data.playerId;
            activeConnections--;

            // Cleanup
            gameWorld.removePlayer(playerId);
            playerRateLimits.delete(playerId);

            // Optimized disconnection logging
            if (activeConnections % CONNECTION_STATS_INTERVAL === 0 || activeConnections < 10) {
                console.log(`🔌 Player disconnected. Active connections: ${activeConnections}`);
            }
        }
    };
}

// Cleanup function for graceful shutdown
export function shutdownOptimizedWebSocket(): void {
    console.log('🛑 Shutting down optimized WebSocket handler...');
    playerRateLimits.clear();
    console.log(`📊 Final stats: ${totalConnections} total connections processed`);
}
