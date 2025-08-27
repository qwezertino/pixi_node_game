import {
    MessageType,
    PlayerState,
    MoveMessage,
    DirectionChangeMessage,
    PlayerDirectionMessage,
    PlayerMovementMessage,
    GameStateMessage,
    ServerCorrectionMessage,
    PlayerJoinedMessage,
    PlayerLeftMessage,
    AttackMessage,
    InitialStateMessage,
    PlayerAttackMessage,
} from "./messages";

// Binary message format to reduce network traffic
export class BinaryProtocol {
    // Encode client messages with bit packing optimization
    static encodeMove(moveMsg: MoveMessage): Uint8Array {
        // Optimized Format: [MessageType.MOVE(1), packed_movement(1)]
        // Pack dx(-1,0,1) and dy(-1,0,1) into 2 bits each, total 4 bits + 4 spare bits
        const buffer = new ArrayBuffer(2);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.MOVE);

        // Convert float movement to integers (-1, 0, 1)
        const dx = Math.sign(moveMsg.movementVector.dx) || 0;
        const dy = Math.sign(moveMsg.movementVector.dy) || 0;

        // Pack movement: dx in bits 0-1, dy in bits 2-3
        let packed = 0;
        packed |= (dx + 1) & 0x03; // dx: -1->0, 0->1, 1->2 (2 bits)
        packed |= ((dy + 1) & 0x03) << 2; // dy: same, shifted 2 bits

        view.setUint8(1, packed);
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

    // Encode server messages with optimization
    static encodePlayerMovement(moveMsg: PlayerMovementMessage): Uint8Array {
        // Optimized Format: [MessageType.MOVE(1), playerIdLength(1), playerId(n), packed_movement(1)]
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(moveMsg.playerId);

        const buffer = new ArrayBuffer(3 + playerIdBytes.length);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.MOVE);
        view.setUint8(1, playerIdBytes.length);

        // Copy player ID
        const u8 = new Uint8Array(buffer);
        u8.set(playerIdBytes, 2);

        // Pack movement vector (same bit packing as client)
        const dx = Math.sign(moveMsg.movementVector.dx) || 0;
        const dy = Math.sign(moveMsg.movementVector.dy) || 0;

        let packed = 0;
        packed |= (dx + 1) & 0x03; // dx: -1->0, 0->1, 1->2 (2 bits)
        packed |= ((dy + 1) & 0x03) << 2; // dy: same, shifted 2 bits

        view.setUint8(2 + playerIdBytes.length, packed);

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
        // Optimized game state with integer positions and bit packing
        const encoder = new TextEncoder();
        let totalSize = 6; // 1 byte for type, 4 bytes for timestamp, 1 byte for player count

        // Pre-encode player IDs to get their lengths
        const playerIds = Object.keys(msg.players);
        const playerIdBuffers = playerIds.map((id) => encoder.encode(id));

        // Optimized: Each player entry: id length(1) + id bytes + packed_flags(1) + x(2) + y(2)
        // packed_flags: direction(1bit) + moving(1bit) + 6 spare bits
        // Use Int16 for discrete positions (perfect for integer movement system)
        for (let i = 0; i < playerIdBuffers.length; i++) {
            totalSize += 1 + playerIdBuffers[i].length + 5; // 1 flag byte + 2 bytes x + 2 bytes y
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

            // Pack direction and moving into a single byte
            let packedFlags = 0;
            packedFlags |= playerData.direction === 1 ? 1 : 0; // 1 bit for direction (0=left, 1=right)
            packedFlags |= (playerData.moving ? 1 : 0) << 1; // 1 bit for moving
            view.setUint8(offset, packedFlags);
            offset++;

            // Use Int16 positions for discrete movement system
            const discreteX = Math.round(playerData.position.x);
            const discreteY = Math.round(playerData.position.y);
            console.log(
                `📦 [PROTOCOL] Encoding gameState player ${playerId}: float pos (${playerData.position.x.toFixed(
                    2
                )}, ${playerData.position.y.toFixed(
                    2
                )}) -> discrete int16 (${discreteX}, ${discreteY})`
            );

            view.setInt16(offset, discreteX, true);
            offset += 2;
            view.setInt16(offset, discreteY, true);
            offset += 2;
        }

        return u8;
    }

    // New methods for additional message types
    static encodeInitialState(msg: InitialStateMessage): Uint8Array {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(msg.player.id);

        // Calculate size more carefully
        // Header: type(1) + timestamp(4) = 5
        // Main player: playerIdLength(1) + playerId(n) + direction(1) + moving(1) + posX(4) + posY(4) = 11 + n
        // Other players count: playerCount(1) = 1
        // Each other player: idLength(1) + id(n) + direction(1) + moving(1) + x(4) + y(4) = 11 + n

        let totalSize = 5; // Header
        totalSize += 1 + playerIdBytes.length + 10; // Main player data
        totalSize += 1; // Player count

        // Pre-encode other player IDs to get their lengths
        const playerIds = Object.keys(msg.players);
        const playerIdBuffers = playerIds.map((id) => encoder.encode(id));

        // Each other player entry: id length(1) + id bytes + direction(1) + moving(1) + x(4) + y(4)
        for (let i = 0; i < playerIdBuffers.length; i++) {
            totalSize += 1 + playerIdBuffers[i].length + 10;
        }

        const buffer = new ArrayBuffer(totalSize);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        let offset = 0;

        // Message type and timestamp
        view.setUint8(offset, MessageType.INITIAL_STATE);
        offset++;
        view.setUint32(offset, msg.timestamp, true);
        offset += 4;

        // Main player data
        view.setUint8(offset, playerIdBytes.length);
        offset++;
        u8.set(playerIdBytes, offset);
        offset += playerIdBytes.length;
        view.setInt8(offset, msg.player.direction);
        offset++;
        view.setUint8(offset, msg.player.moving ? 1 : 0);
        offset++;
        view.setFloat32(offset, msg.player.position.x, true);
        offset += 4;
        view.setFloat32(offset, msg.player.position.y, true);
        offset += 4;

        // Other players count
        view.setUint8(offset, playerIds.length);
        offset++;

        // Encode each other player
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

    static encodePlayerJoined(msg: PlayerJoinedMessage): Uint8Array {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(msg.player.id);

        // Calculate size: type(1) + idLength(1) + id(n) + direction(1) + moving(1) + posX(4) + posY(4)
        const totalSize = 12 + playerIdBytes.length;

        const buffer = new ArrayBuffer(totalSize);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        // Header
        view.setUint8(0, MessageType.PLAYER_JOINED);
        view.setUint8(1, playerIdBytes.length);

        // Copy player ID
        u8.set(playerIdBytes, 2);

        // Player data
        let offset = 2 + playerIdBytes.length;
        view.setInt8(offset, msg.player.direction);
        offset++;
        view.setUint8(offset, msg.player.moving ? 1 : 0);
        offset++;
        view.setFloat32(offset, msg.player.position.x, true);
        offset += 4;
        view.setFloat32(offset, msg.player.position.y, true);

        return u8;
    }

    static encodePlayerLeft(msg: PlayerLeftMessage): Uint8Array {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(msg.playerId);

        // Calculate size: type(1) + idLength(1) + id(n)
        const totalSize = 2 + playerIdBytes.length;

        const buffer = new ArrayBuffer(totalSize);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        // Header
        view.setUint8(0, MessageType.PLAYER_LEFT);
        view.setUint8(1, playerIdBytes.length);

        // Copy player ID
        u8.set(playerIdBytes, 2);

        return u8;
    }

    static encodeAttack(msg: AttackMessage): Uint8Array {
        // Format: [MessageType.ATTACK(1), x(4), y(4)]
        const buffer = new ArrayBuffer(9);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.ATTACK);
        view.setFloat32(1, msg.position.x, true);
        view.setFloat32(5, msg.position.y, true);
        return new Uint8Array(buffer);
    }

    static encodePlayerAttack(msg: PlayerAttackMessage): Uint8Array {
        // Format: [MessageType.ATTACK(1), playerIdLength(1), playerId(n), x(4), y(4)]
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(msg.playerId);

        const buffer = new ArrayBuffer(10 + playerIdBytes.length);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.ATTACK);
        view.setUint8(1, playerIdBytes.length);

        // Copy player ID
        const u8 = new Uint8Array(buffer);
        u8.set(playerIdBytes, 2);

        // Set position
        view.setFloat32(2 + playerIdBytes.length, msg.position.x, true);
        view.setFloat32(6 + playerIdBytes.length, msg.position.y, true);

        return u8;
    }

    // Decode messages
    static decodeMessage(data: Uint8Array): any {
        const view = new DataView(data.buffer);
        const messageType = view.getUint8(0);

        switch (messageType) {
            case MessageType.MOVE: {
                if (data.length === 2) {
                    // Optimized client message
                    const packed = view.getUint8(1);
                    const dx = (packed & 0x03) - 1; // Extract bits 0-1, convert back to -1,0,1
                    const dy = ((packed >> 2) & 0x03) - 1; // Extract bits 2-3, convert back to -1,0,1

                    return {
                        type: "move",
                        movementVector: { dx, dy },
                    };
                } else if (data.length === 9) {
                    // Legacy client message (for backward compatibility)
                    return {
                        type: "move",
                        movementVector: {
                            dx: view.getFloat32(1, true),
                            dy: view.getFloat32(5, true),
                        },
                    };
                } else {
                    // Server message (player movement) - check if optimized or legacy
                    const playerIdLength = view.getUint8(1);
                    const decoder = new TextDecoder();
                    const playerId = decoder.decode(
                        data.subarray(2, 2 + playerIdLength)
                    );

                    if (data.length === 3 + playerIdLength) {
                        // Optimized server message
                        const packed = view.getUint8(2 + playerIdLength);
                        const dx = (packed & 0x03) - 1; // Extract bits 0-1
                        const dy = ((packed >> 2) & 0x03) - 1; // Extract bits 2-3

                        return {
                            type: "playerMovement",
                            playerId,
                            movementVector: { dx, dy },
                        };
                    } else {
                        // Legacy server message
                        return {
                            type: "playerMovement",
                            playerId,
                            movementVector: {
                                dx: view.getFloat32(2 + playerIdLength, true),
                                dy: view.getFloat32(6 + playerIdLength, true),
                            },
                        };
                    }
                }
            }

            case MessageType.DIRECTION: {
                if (data.length === 2) {
                    // Client message
                    return {
                        type: "direction",
                        direction: view.getInt8(1) as -1 | 1,
                    };
                } else {
                    // Server message
                    const playerIdLength = view.getUint8(1);
                    const decoder = new TextDecoder();
                    const playerId = decoder.decode(
                        data.subarray(2, 2 + playerIdLength)
                    );

                    return {
                        type: "playerDirection",
                        playerId,
                        direction: view.getInt8(2 + playerIdLength) as -1 | 1,
                    };
                }
            }

            case MessageType.CORRECTION: {
                const playerIdLength = view.getUint8(1);
                const decoder = new TextDecoder();
                const playerId = decoder.decode(
                    data.subarray(2, 2 + playerIdLength)
                );

                return {
                    type: "correction",
                    playerId,
                    position: {
                        x: view.getFloat32(2 + playerIdLength, true),
                        y: view.getFloat32(6 + playerIdLength, true),
                    },
                };
            }

            case MessageType.GAME_STATE: {
                if (data.length < 6) {
                    console.error(
                        "GameState decode error: insufficient data for header"
                    );
                    return null;
                }

                const timestamp = view.getUint32(1, true);
                const playerCount = view.getUint8(5);

                const players: Record<string, PlayerState> = {};
                const decoder = new TextDecoder();

                let offset = 6;
                for (let i = 0; i < playerCount; i++) {
                    // Bounds check for player ID length
                    if (offset >= data.length) {
                        console.error(
                            "GameState decode error: insufficient data for player ID length"
                        );
                        break;
                    }

                    const playerIdLength = view.getUint8(offset);
                    offset++;

                    // Bounds check for player ID
                    if (offset + playerIdLength > data.length) {
                        console.error(
                            `GameState decode error: insufficient data for player ID. Need ${playerIdLength} bytes, have ${
                                data.length - offset
                            }`
                        );
                        break;
                    }

                    const playerId = decoder.decode(
                        data.subarray(offset, offset + playerIdLength)
                    );
                    offset += playerIdLength;

                    // GameState format: packed_flags(1) + x(2) + y(2) = 5 bytes per player
                    const remainingData = data.length - offset;

                    if (remainingData < 5) {
                        console.error(
                            `GameState decode error: insufficient data for player ${playerId}, need 5 bytes, have ${remainingData}`
                        );
                        break;
                    }

                    const packedFlags = view.getUint8(offset);
                    offset++;

                    const direction = packedFlags & 0x01 ? 1 : -1;
                    const moving = (packedFlags >> 1) & 0x01 ? true : false;

                    const x = view.getInt16(offset, true);
                    offset += 2;

                    const y = view.getInt16(offset, true);
                    offset += 2;

                    console.log(
                        `📦 [PROTOCOL] Decoding gameState player ${playerId}: discrete int16 (${x}, ${y}) -> discrete pos (${x}, ${y})`
                    );

                    players[playerId] = {
                        id: playerId,
                        direction,
                        moving,
                        position: { x, y },
                    };
                }

                return {
                    type: "gameState",
                    players,
                    timestamp,
                };
            }

            case MessageType.PLAYER_JOINED: {
                const playerIdLength = view.getUint8(1);
                const decoder = new TextDecoder();
                const playerId = decoder.decode(
                    data.subarray(2, 2 + playerIdLength)
                );
                let offset = 2 + playerIdLength;

                return {
                    type: "playerJoined",
                    player: {
                        id: playerId,
                        direction: view.getInt8(offset++) as -1 | 1,
                        moving: view.getUint8(offset++) === 1,
                        position: {
                            x: view.getFloat32(offset, true),
                            y: view.getFloat32(offset + 4, true),
                        },
                    },
                };
            }

            case MessageType.PLAYER_LEFT: {
                const playerIdLength = view.getUint8(1);
                const decoder = new TextDecoder();
                const playerId = decoder.decode(
                    data.subarray(2, 2 + playerIdLength)
                );

                return {
                    type: "playerLeft",
                    playerId,
                };
            }

            case MessageType.ATTACK: {
                // Check if it's a client message (9 bytes) or server message (longer)
                if (data.length === 9) {
                    return {
                        type: "attack",
                        position: {
                            x: view.getFloat32(1, true),
                            y: view.getFloat32(5, true),
                        },
                    };
                } else {
                    const playerIdLength = view.getUint8(1);
                    const decoder = new TextDecoder();
                    const playerId = decoder.decode(
                        data.subarray(2, 2 + playerIdLength)
                    );
                    const offset = 2 + playerIdLength;

                    return {
                        type: "playerAttack",
                        playerId,
                        position: {
                            x: view.getFloat32(offset, true),
                            y: view.getFloat32(offset + 4, true),
                        },
                    };
                }
            }

            case MessageType.INITIAL_STATE: {
                const timestamp = view.getUint32(1, true);
                const decoder = new TextDecoder();

                // Main player
                const playerIdLength = view.getUint8(5);
                const playerId = decoder.decode(
                    data.subarray(6, 6 + playerIdLength)
                );
                let offset = 6 + playerIdLength;

                const direction = view.getInt8(offset++) as -1 | 1;
                const moving = view.getUint8(offset++) === 1;
                const x = view.getFloat32(offset, true);
                offset += 4;
                const y = view.getFloat32(offset, true);
                offset += 4;

                const player = {
                    id: playerId,
                    direction,
                    moving,
                    position: { x, y },
                };

                // Other players
                const playerCount = view.getUint8(offset++);
                const players: Record<string, PlayerState> = {};

                for (let i = 0; i < playerCount; i++) {
                    const playerIdLength = view.getUint8(offset++);
                    const playerId = decoder.decode(
                        data.subarray(offset, offset + playerIdLength)
                    );
                    offset += playerIdLength;

                    const direction = view.getInt8(offset++) as -1 | 1;
                    const moving = view.getUint8(offset++) === 1;
                    const x = view.getFloat32(offset, true);
                    offset += 4;
                    const y = view.getFloat32(offset, true);
                    offset += 4;

                    players[playerId] = {
                        id: playerId,
                        direction,
                        moving,
                        position: { x, y },
                    };
                }

                return {
                    type: "initialState",
                    player,
                    players,
                    timestamp,
                };
            }

            default:
                return null;
        }
    }
}
