import {
    MessageType,
    PlayerState,
    MoveMessage,
    DirectionChangeMessage,
    PlayerDirectionMessage,
    PlayerMovementMessage,
    GameStateMessage,
    ServerCorrectionMessage
} from './messages';

// Binary message format to reduce network traffic
export class BinaryProtocol {
    // Encode client messages
    static encodeMove(moveMsg: MoveMessage): Uint8Array {
        // Format: [MessageType.MOVE(1), dx(4), dy(4)]
        const buffer = new ArrayBuffer(9);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.MOVE);
        view.setFloat32(1, moveMsg.movementVector.dx, true);
        view.setFloat32(5, moveMsg.movementVector.dy, true);
        return new Uint8Array(buffer);
    }

    static encodeDirection(dirMsg: DirectionChangeMessage): Uint8Array {
        // Format: [MessageType.DIRECTION(1), direction(1)]
        const buffer = new ArrayBuffer(2);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.DIRECTION);
        view.setInt8(1, dirMsg.direction);
        return new Uint8Array(buffer);
    }

    // Encode server messages
    static encodePlayerMovement(moveMsg: PlayerMovementMessage): Uint8Array {
        // Format: [MessageType.MOVE(1), playerIdLength(1), playerId(n), dx(4), dy(4)]
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(moveMsg.playerId);

        const buffer = new ArrayBuffer(10 + playerIdBytes.length);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.MOVE);
        view.setUint8(1, playerIdBytes.length);

        // Copy player ID
        const u8 = new Uint8Array(buffer);
        u8.set(playerIdBytes, 2);

        // Set movement vector
        view.setFloat32(2 + playerIdBytes.length, moveMsg.movementVector.dx, true);
        view.setFloat32(6 + playerIdBytes.length, moveMsg.movementVector.dy, true);

        return u8;
    }

    static encodePlayerDirection(dirMsg: PlayerDirectionMessage): Uint8Array {
        // Format: [MessageType.DIRECTION(1), playerIdLength(1), playerId(n), direction(1)]
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(dirMsg.playerId);

        const buffer = new ArrayBuffer(3 + playerIdBytes.length);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.DIRECTION);
        view.setUint8(1, playerIdBytes.length);

        // Copy player ID
        const u8 = new Uint8Array(buffer);
        u8.set(playerIdBytes, 2);

        // Set direction
        view.setInt8(2 + playerIdBytes.length, dirMsg.direction);

        return u8;
    }

    static encodeCorrection(msg: ServerCorrectionMessage): Uint8Array {
        // Format: [MessageType.CORRECTION(1), playerIdLength(1), playerId(n), x(4), y(4)]
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(msg.playerId);

        const buffer = new ArrayBuffer(10 + playerIdBytes.length);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.CORRECTION);
        view.setUint8(1, playerIdBytes.length);

        // Copy player ID
        const u8 = new Uint8Array(buffer);
        u8.set(playerIdBytes, 2);

        // Set position
        view.setFloat32(2 + playerIdBytes.length, msg.position.x, true);
        view.setFloat32(6 + playerIdBytes.length, msg.position.y, true);

        return u8;
    }

    static encodeGameState(msg: GameStateMessage): Uint8Array {
        // Calculate the total buffer size
        const encoder = new TextEncoder();
        let totalSize = 6; // 1 byte for type, 4 bytes for timestamp, 1 byte for player count

        // Pre-encode player IDs to get their lengths
        const playerIds = Object.keys(msg.players);
        const playerIdBuffers = playerIds.map(id => encoder.encode(id));

        // Each player entry: id length(1) + id bytes + direction(1) + moving(1) + x(4) + y(4)
        for (let i = 0; i < playerIdBuffers.length; i++) {
            totalSize += 1 + playerIdBuffers[i].length + 10;
        }

        const buffer = new ArrayBuffer(totalSize);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        // Header
        view.setUint8(0, MessageType.GAME_STATE);
        view.setUint32(1, msg.timestamp, true);
        view.setUint8(5, playerIds.length);

        // Encode each player
        let offset = 6;
        for (let i = 0; i < playerIds.length; i++) {
            const playerId = playerIds[i];
            const playerData = msg.players[playerId];
            const playerIdBytes = playerIdBuffers[i];

            // Player ID
            view.setUint8(offset, playerIdBytes.length);
            offset++;
            u8.set(playerIdBytes, offset);
            offset += playerIdBytes.length;

            // Player data
            view.setInt8(offset, playerData.direction);
            offset++;
            view.setUint8(offset, playerData.moving ? 1 : 0);
            offset++;
            view.setFloat32(offset, playerData.position.x, true);
            offset += 4;
            view.setFloat32(offset, playerData.position.y, true);
            offset += 4;
        }

        return u8;
    }

    // Decode messages
    static decodeMessage(data: Uint8Array): any {
        const view = new DataView(data.buffer);
        const messageType = view.getUint8(0);

        switch (messageType) {
            case MessageType.MOVE: {
                if (data.length === 9) {
                    // Client message
                    return {
                        type: 'move',
                        movementVector: {
                            dx: view.getFloat32(1, true),
                            dy: view.getFloat32(5, true)
                        }
                    };
                } else {
                    // Server message (player movement)
                    const playerIdLength = view.getUint8(1);
                    const decoder = new TextDecoder();
                    const playerId = decoder.decode(data.subarray(2, 2 + playerIdLength));

                    return {
                        type: 'playerMovement',
                        playerId,
                        movementVector: {
                            dx: view.getFloat32(2 + playerIdLength, true),
                            dy: view.getFloat32(6 + playerIdLength, true)
                        }
                    };
                }
            }

            case MessageType.DIRECTION: {
                if (data.length === 2) {
                    // Client message
                    return {
                        type: 'direction',
                        direction: view.getInt8(1) as -1 | 1
                    };
                } else {
                    // Server message
                    const playerIdLength = view.getUint8(1);
                    const decoder = new TextDecoder();
                    const playerId = decoder.decode(data.subarray(2, 2 + playerIdLength));

                    return {
                        type: 'playerDirection',
                        playerId,
                        direction: view.getInt8(2 + playerIdLength) as -1 | 1
                    };
                }
            }

            case MessageType.CORRECTION: {
                const playerIdLength = view.getUint8(1);
                const decoder = new TextDecoder();
                const playerId = decoder.decode(data.subarray(2, 2 + playerIdLength));

                return {
                    type: 'correction',
                    playerId,
                    position: {
                        x: view.getFloat32(2 + playerIdLength, true),
                        y: view.getFloat32(6 + playerIdLength, true)
                    }
                };
            }

            case MessageType.GAME_STATE: {
                const timestamp = view.getUint32(1, true);
                const playerCount = view.getUint8(5);

                const players: Record<string, PlayerState> = {};
                const decoder = new TextDecoder();

                let offset = 6;
                for (let i = 0; i < playerCount; i++) {
                    const playerIdLength = view.getUint8(offset);
                    offset++;

                    const playerId = decoder.decode(data.subarray(offset, offset + playerIdLength));
                    offset += playerIdLength;

                    const direction = view.getInt8(offset) as -1 | 1;
                    offset++;

                    const moving = view.getUint8(offset) === 1;
                    offset++;

                    const x = view.getFloat32(offset, true);
                    offset += 4;

                    const y = view.getFloat32(offset, true);
                    offset += 4;

                    players[playerId] = {
                        id: playerId,
                        direction,
                        moving,
                        position: { x, y }
                    };
                }

                return {
                    type: 'gameState',
                    players,
                    timestamp
                };
            }

            default:
                return null;
        }
    }
}