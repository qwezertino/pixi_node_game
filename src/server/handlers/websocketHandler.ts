import { ServerWebSocket } from "bun";
import { GameWorld } from "../game/gameWorld";
import { BinaryProtocol } from "../../protocol/binaryProtocol";
import { v4 as uuidv4 } from 'uuid';

// Create a single game world instance
const gameWorld = new GameWorld();

// Store websocket data
interface WebSocketData {
    playerId: string;
}

export function handleWebSocket() {
    return {
        // Connection is opened by a new client
        open(ws: ServerWebSocket<WebSocketData>) {
            // Generate unique player ID
            const playerId = uuidv4();
            ws.data = { playerId };

            console.log(`New player connected: ${playerId}`);

            // Add player to game world and get initial state
            const playerState = gameWorld.addPlayer(playerId, ws);

            // Send initial game state to the new player
            const initialState = {
                type: 'initialState',
                player: playerState,
                players: gameWorld.getAllPlayersState(),
                timestamp: Date.now()
            };

            // Send as JSON (initial state is only sent once)
            ws.send(JSON.stringify(initialState));
        },

        // Message received from client
        message(ws: ServerWebSocket<WebSocketData>, message: string | Uint8Array) {
            const playerId = ws.data.playerId;

            try {
                // Handle binary message
                if (message instanceof Uint8Array) {
                    const decodedMsg = BinaryProtocol.decodeMessage(message);

                    if (decodedMsg) {
                        switch (decodedMsg.type) {
                            case 'move':
                                const { dx, dy } = decodedMsg.movementVector;
                                gameWorld.updatePlayerMovement(playerId, dx, dy);
                                break;

                            case 'direction':
                                gameWorld.updatePlayerDirection(playerId, decodedMsg.direction);
                                break;
                        }
                    }
                }
                // Handle JSON message
                else {
                    const data = JSON.parse(message);

                    switch (data.type) {
                        case 'move':
                            if (data.movementVector) {
                                const { dx, dy } = data.movementVector;
                                gameWorld.updatePlayerMovement(playerId, dx, dy);
                            }
                            break;

                        case 'direction':
                            if (data.direction !== undefined) {
                                gameWorld.updatePlayerDirection(playerId, data.direction);
                            }
                            break;

                        case 'attack':
                            // Handle attack (future implementation)
                            // Broadcast attack to other players
                            break;

                        default:
                            console.log(`Unknown message type: ${data.type}`);
                    }
                }
            } catch (error) {
                console.error(`Error processing message from player ${playerId}:`, error);
                // Consider sending a correction on error
                gameWorld.sendCorrectionToPlayer(playerId);
            }
        },

        // Client disconnected
        close(ws: ServerWebSocket<WebSocketData>) {
            const playerId = ws.data.playerId;
            console.log(`Player disconnected: ${playerId}`);

            // Remove player from game world
            gameWorld.removePlayer(playerId);
        },
    };
}