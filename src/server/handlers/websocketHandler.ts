import { ServerWebSocket } from "bun";
import { GameWorld } from "../game/gameWorld";
import { BinaryProtocol } from "../../protocol/binaryProtocol";
import { v4 as uuidv4 } from 'uuid';
import { InitialStateMessage } from "../../protocol/messages";

// Create a single game world instance
const gameWorld = new GameWorld();

// Store websocket data
interface WebSocketData {
    playerId: string;
    lastActivity: number;
}

// Connection tracking for performance monitoring
let totalConnections = 0;
let activeConnections = 0;

export function handleWebSocket() {
    return {
        // Connection is opened by a new client
        open(ws: ServerWebSocket<WebSocketData>) {
            totalConnections++;
            activeConnections++;

            // Generate unique player ID
            const playerId = uuidv4();
            ws.data = {
                playerId,
                lastActivity: Date.now()
            };

            // Log connection stats periodically
            if (totalConnections % 100 === 0) {
                console.log(`Connections: ${activeConnections} active, ${totalConnections} total`);
            }

            // Get existing players before adding the new one
            const existingPlayers = gameWorld.getAllPlayersState();

            // Add player to game world and get initial state
            const playerState = gameWorld.addPlayer(playerId, ws);

            // Create initial state message with only existing players (not including self)
            const initialState: InitialStateMessage = {
                type: 'initialState',
                player: playerState,
                players: existingPlayers, // Only existing players, not including the new one
                timestamp: Date.now()
            };

            // Send as binary data
            try {
                const binaryData = BinaryProtocol.encodeInitialState(initialState);
                ws.send(binaryData);
            } catch (error) {
                console.error('Failed to send initial state:', error);
                ws.close();
                activeConnections--;
            }
        },

        // Message received from client
        message(ws: ServerWebSocket<WebSocketData>, message: string | Uint8Array) {
            const playerId = ws.data.playerId;
            ws.data.lastActivity = Date.now();

            try {
                // Handle binary message (prioritized for performance)
                if (message instanceof Uint8Array) {
                    const decodedMsg = BinaryProtocol.decodeMessage(message);

                    if (decodedMsg) {
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
                        }
                    }
                }
                // Handle JSON message (fallback for compatibility)
                else {
                    const data = JSON.parse(message);

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

                        default:
                            break;
                    }
                }
            } catch (error) {
                // Send correction on error to resync client
                gameWorld.sendCorrectionToPlayer(playerId);
            }
        },

        // Client disconnected
        close(ws: ServerWebSocket<WebSocketData>) {
            const playerId = ws.data.playerId;
            activeConnections--;

            // Remove player from game world
            gameWorld.removePlayer(playerId);

            // Log disconnection stats periodically
            if (activeConnections % 100 === 0 || activeConnections < 10) {
                console.log(`Player disconnected. Active connections: ${activeConnections}`);
            }
        },
    };
}