import {
    MessageType,
    PlayerState,
    PlayerPosition,
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

export class BinaryProtocol {
    // Helper methods for common operations
    private static packMovement(dx: number, dy: number): number {
        let packed = 0;
        packed |= (dx + 1) & 0x03; // dx: -1->0, 0->1, 1->2 (2 bits)
        packed |= ((dy + 1) & 0x03) << 2; // dy: same, shifted 2 bits
        return packed;
    }

    private static unpackMovement(packed: number): { dx: number; dy: number } {
        const dx = (packed & 0x03) - 1; // Extract bits 0-1, convert back to -1,0,1
        const dy = ((packed >> 2) & 0x03) - 1; // Extract bits 2-3, convert back to -1,0,1
        return { dx, dy };
    }

    private static encodePlayerId(_buffer: ArrayBuffer, view: DataView, u8: Uint8Array, offset: number, playerId: string): number {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(playerId);
        view.setUint8(offset, playerIdBytes.length);
        u8.set(playerIdBytes, offset + 1);
        return offset + 1 + playerIdBytes.length;
    }

    private static decodePlayerId(data: Uint8Array, offset: number): { playerId: string; newOffset: number } {
        const decoder = new TextDecoder();
        const playerIdLength = data[offset];
        const playerId = decoder.decode(data.subarray(offset + 1, offset + 1 + playerIdLength));
        return { playerId, newOffset: offset + 1 + playerIdLength };
    }

    // Encode client messages
    static encodeMove(moveMsg: MoveMessage): Uint8Array {
        const buffer = new ArrayBuffer(6);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.MOVE);

        const dx = Math.sign(moveMsg.movementVector.dx) || 0;
        const dy = Math.sign(moveMsg.movementVector.dy) || 0;
        const packed = this.packMovement(dx, dy);

        view.setUint8(1, packed);
        view.setUint32(2, moveMsg.inputSequence, true);
        return new Uint8Array(buffer);
    }

    static encodeDirection(dirMsg: DirectionChangeMessage): Uint8Array {
        const buffer = new ArrayBuffer(2);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.DIRECTION);
        view.setInt8(1, dirMsg.direction);
        return new Uint8Array(buffer);
    }

    static encodeAttack(msg: AttackMessage): Uint8Array {
        const buffer = new ArrayBuffer(9);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.ATTACK);
        view.setFloat32(1, msg.position.x, true);
        view.setFloat32(5, msg.position.y, true);
        return new Uint8Array(buffer);
    }

    static encodeAttackEnd(): Uint8Array {
        const buffer = new ArrayBuffer(1);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.ATTACK_END);
        return new Uint8Array(buffer);
    }

    // Encode server messages
    static encodePlayerMovement(moveMsg: PlayerMovementMessage): Uint8Array {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(moveMsg.playerId);
        const buffer = new ArrayBuffer(3 + playerIdBytes.length);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        view.setUint8(0, MessageType.MOVE);
        const offset = this.encodePlayerId(buffer, view, u8, 1, moveMsg.playerId);

        const dx = Math.sign(moveMsg.movementVector.dx) || 0;
        const dy = Math.sign(moveMsg.movementVector.dy) || 0;
        const packed = this.packMovement(dx, dy);
        view.setUint8(offset, packed);

        return u8;
    }

    static encodePlayerDirection(dirMsg: PlayerDirectionMessage): Uint8Array {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(dirMsg.playerId);
        const buffer = new ArrayBuffer(3 + playerIdBytes.length);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        view.setUint8(0, MessageType.DIRECTION);
        const offset = this.encodePlayerId(buffer, view, u8, 1, dirMsg.playerId);
        view.setInt8(offset, dirMsg.direction);

        return u8;
    }

    static encodeCorrection(msg: ServerCorrectionMessage): Uint8Array {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(msg.playerId);
        const buffer = new ArrayBuffer(10 + playerIdBytes.length);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        view.setUint8(0, MessageType.CORRECTION);
        const offset = this.encodePlayerId(buffer, view, u8, 1, msg.playerId);
        view.setFloat32(offset, msg.position.x, true);
        view.setFloat32(offset + 4, msg.position.y, true);

        return u8;
    }

    static encodeGameState(msg: GameStateMessage): Uint8Array {
        const encoder = new TextEncoder();
        const playerIds = Object.keys(msg.players);
        const playerIdBuffers = playerIds.map(id => encoder.encode(id));

        let totalSize = 6; // Header: type(1) + timestamp(4) + playerCount(1)
        for (const idBuffer of playerIdBuffers) {
            totalSize += 1 + idBuffer.length + 5; // idLength(1) + id(n) + flags(1) + x(2) + y(2)
        }

        const buffer = new ArrayBuffer(totalSize);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        view.setUint8(0, MessageType.GAME_STATE);
        view.setUint32(1, msg.timestamp, true);
        view.setUint8(5, playerIds.length);

        let offset = 6;
        for (let i = 0; i < playerIds.length; i++) {
            const playerData = msg.players[playerIds[i]];
            const playerIdBytes = playerIdBuffers[i];

            view.setUint8(offset, playerIdBytes.length);
            offset++;
            u8.set(playerIdBytes, offset);
            offset += playerIdBytes.length;

            let packedFlags = 0;
            packedFlags |= playerData.direction === 1 ? 1 : 0;
            packedFlags |= (playerData.moving ? 1 : 0) << 1;
            packedFlags |= ((playerData.attacking || false) ? 1 : 0) << 2;
            view.setUint8(offset, packedFlags);
            offset++;

            view.setInt16(offset, Math.round(playerData.position.x), true);
            offset += 2;
            view.setInt16(offset, Math.round(playerData.position.y), true);
            offset += 2;
        }

        return u8;
    }

    static encodeInitialState(msg: InitialStateMessage): Uint8Array {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(msg.player.id);
        const playerIds = Object.keys(msg.players);
        const playerIdBuffers = playerIds.map(id => encoder.encode(id));

        let totalSize = 5; // Header: type(1) + timestamp(4)
        totalSize += 1 + playerIdBytes.length + 6; // Main player
        totalSize += 1; // Player count
        for (const idBuffer of playerIdBuffers) {
            totalSize += 1 + idBuffer.length + 6; // Other players
        }

        const buffer = new ArrayBuffer(totalSize);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        let offset = 0;
        view.setUint8(offset, MessageType.INITIAL_STATE);
        offset++;
        view.setUint32(offset, msg.timestamp, true);
        offset += 4;

        // Main player
        offset = this.encodePlayerId(buffer, view, u8, offset, msg.player.id);
        view.setInt8(offset, msg.player.direction);
        offset++;
        view.setUint8(offset, msg.player.moving ? 1 : 0);
        offset++;
        view.setInt16(offset, Math.round(msg.player.position.x), true);
        offset += 2;
        view.setInt16(offset, Math.round(msg.player.position.y), true);
        offset += 2;

        view.setUint8(offset, playerIds.length);
        offset++;

        // Other players
        for (let i = 0; i < playerIds.length; i++) {
            const playerData = msg.players[playerIds[i]];
            const playerIdBytes = playerIdBuffers[i];

            view.setUint8(offset, playerIdBytes.length);
            offset++;
            u8.set(playerIdBytes, offset);
            offset += playerIdBytes.length;

            view.setInt8(offset, playerData.direction);
            offset++;
            view.setUint8(offset, playerData.moving ? 1 : 0);
            offset++;
            view.setInt16(offset, Math.round(playerData.position.x), true);
            offset += 2;
            view.setInt16(offset, Math.round(playerData.position.y), true);
            offset += 2;
        }

        return u8;
    }

    static encodePlayerJoined(msg: PlayerJoinedMessage): Uint8Array {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(msg.player.id);
        const buffer = new ArrayBuffer(12 + playerIdBytes.length);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        view.setUint8(0, MessageType.PLAYER_JOINED);
        const offset = this.encodePlayerId(buffer, view, u8, 1, msg.player.id);
        view.setInt8(offset, msg.player.direction);
        view.setUint8(offset + 1, msg.player.moving ? 1 : 0);
        view.setFloat32(offset + 2, msg.player.position.x, true);
        view.setFloat32(offset + 6, msg.player.position.y, true);

        return u8;
    }

    static encodePlayerLeft(msg: PlayerLeftMessage): Uint8Array {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(msg.playerId);
        const buffer = new ArrayBuffer(2 + playerIdBytes.length);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        view.setUint8(0, MessageType.PLAYER_LEFT);
        this.encodePlayerId(buffer, view, u8, 1, msg.playerId);

        return u8;
    }

    static encodePlayerAttack(msg: PlayerAttackMessage): Uint8Array {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(msg.playerId);
        const buffer = new ArrayBuffer(10 + playerIdBytes.length);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        view.setUint8(0, MessageType.ATTACK);
        const offset = this.encodePlayerId(buffer, view, u8, 1, msg.playerId);
        view.setFloat32(offset, msg.position.x, true);
        view.setFloat32(offset + 4, msg.position.y, true);

        return u8;
    }

    static encodeMovementAcknowledgment(msg: { type: 'movementAck'; playerId: string; acknowledgedPosition: PlayerPosition; inputSequence: number; timestamp: number }): Uint8Array {
        const encoder = new TextEncoder();
        const playerIdBytes = encoder.encode(msg.playerId);
        const buffer = new ArrayBuffer(14 + playerIdBytes.length);
        const view = new DataView(buffer);
        const u8 = new Uint8Array(buffer);

        let offset = 0;
        view.setUint8(offset, MessageType.MOVEMENT_ACK);
        offset++;
        view.setUint32(offset, msg.timestamp, true);
        offset += 4;

        offset = this.encodePlayerId(buffer, view, u8, offset, msg.playerId);
        view.setInt16(offset, Math.round(msg.acknowledgedPosition.x), true);
        offset += 2;
        view.setInt16(offset, Math.round(msg.acknowledgedPosition.y), true);
        offset += 2;
        view.setUint32(offset, msg.inputSequence, true);

        return new Uint8Array(buffer);
    }

    // Decode messages
    static decodeMessage(data: Uint8Array): any {
        if (data.length === 0) return null;

        const view = new DataView(data.buffer);
        const messageType = view.getUint8(0);

        switch (messageType) {
            case MessageType.MOVE: return this.decodeMove(data, view);
            case MessageType.DIRECTION: return this.decodeDirection(data, view);
            case MessageType.ATTACK: return this.decodeAttack(data, view);
            case MessageType.ATTACK_END: return this.decodeAttackEnd();
            case MessageType.GAME_STATE: return this.decodeGameState(data, view);
            case MessageType.INITIAL_STATE: return this.decodeInitialState(data, view);
            case MessageType.PLAYER_JOINED: return this.decodePlayerJoined(data, view);
            case MessageType.PLAYER_LEFT: return this.decodePlayerLeft(data, view);
            case MessageType.CORRECTION: return this.decodeCorrection(data, view);
            case MessageType.MOVEMENT_ACK: return this.decodeMovementAck(data, view);
            default: return null;
        }
    }

    private static decodeMove(data: Uint8Array, view: DataView) {
        // Check if this is a server message (has playerId) or client message
        if (data.length > 6 && data[1] > 0 && data[1] < 256) {
            // Server message (player movement) - has playerId after message type
            const { playerId, newOffset } = this.decodePlayerId(data, 1);

            if (data.length === newOffset + 1) {
                // Optimized server message with packed movement
                const packed = view.getUint8(newOffset);
                const movement = this.unpackMovement(packed);
                return {
                    type: "playerMovement",
                    playerId,
                    movementVector: movement,
                };
            } else if (data.length === newOffset + 8) {
                // Legacy server message with float32 movement
                return {
                    type: "playerMovement",
                    playerId,
                    movementVector: {
                        dx: view.getFloat32(newOffset, true),
                        dy: view.getFloat32(newOffset + 4, true),
                    },
                };
            }
        }

        // Client messages
        if (data.length === 2) {
            // Optimized client message (legacy)
            const packed = view.getUint8(1);
            const movement = this.unpackMovement(packed);
            return {
                type: "move",
                movementVector: movement,
                inputSequence: 0,
            };
        } else if (data.length === 6) {
            // New optimized client message with inputSequence
            const packed = view.getUint8(1);
            const movement = this.unpackMovement(packed);
            const inputSequence = view.getUint32(2, true);
            return {
                type: "move",
                movementVector: movement,
                inputSequence,
            };
        } else if (data.length === 9) {
            // Legacy client message
            return {
                type: "move",
                movementVector: {
                    dx: view.getFloat32(1, true),
                    dy: view.getFloat32(5, true),
                },
                inputSequence: 0,
            };
        }

        return null;
    }

    private static decodeDirection(data: Uint8Array, view: DataView) {
        // Check if this is a server message (has playerId) or client message
        if (data.length > 2 && data[1] > 0 && data[1] < 256) {
            // Server message (player direction) - has playerId after message type
            const { playerId, newOffset } = this.decodePlayerId(data, 1);
            return {
                type: "playerDirection",
                playerId,
                direction: view.getInt8(newOffset) as -1 | 1,
            };
        }

        // Client message
        if (data.length === 2) {
            return {
                type: "direction",
                direction: view.getInt8(1) as -1 | 1,
            };
        }

        return null;
    }

    private static decodeAttack(data: Uint8Array, view: DataView) {
        // Check if this is a server message (has playerId) or client message
        if (data.length > 9 && data[1] > 0 && data[1] < 256) {
            // Server message (player attack) - has playerId after message type
            const { playerId, newOffset } = this.decodePlayerId(data, 1);
            return {
                type: "playerAttack",
                playerId,
                position: {
                    x: view.getFloat32(newOffset, true),
                    y: view.getFloat32(newOffset + 4, true),
                },
            };
        }

        // Client message
        if (data.length === 9) {
            return {
                type: "attack",
                position: {
                    x: view.getFloat32(1, true),
                    y: view.getFloat32(5, true),
                },
            };
        }

        return null;
    }

    private static decodeAttackEnd() {
        return { type: "attackEnd" };
    }

    private static decodeGameState(data: Uint8Array, view: DataView) {
        if (data.length < 6) return null;

        const timestamp = view.getUint32(1, true);
        const playerCount = view.getUint8(5);
        const players: Record<string, PlayerState> = {};

        let offset = 6;
        for (let i = 0; i < playerCount; i++) {
            if (offset >= data.length) break;

            const { playerId, newOffset } = this.decodePlayerId(data, offset);
            offset = newOffset;

            if (offset + 5 > data.length) break;

            const packedFlags = view.getUint8(offset);
            offset++;

            const direction = packedFlags & 0x01 ? 1 : -1;
            const moving = (packedFlags >> 1) & 0x01 ? true : false;
            const attacking = (packedFlags >> 2) & 0x01 ? true : false;

            const x = view.getInt16(offset, true);
            offset += 2;
            const y = view.getInt16(offset, true);
            offset += 2;

            players[playerId] = {
                id: playerId,
                direction,
                moving,
                attacking,
                position: { x, y },
            };
        }

        return { type: "gameState", players, timestamp };
    }

    private static decodeInitialState(data: Uint8Array, view: DataView) {
        const timestamp = view.getUint32(1, true);

        // Main player
        const { playerId, newOffset } = this.decodePlayerId(data, 5);
        let offset = newOffset;

        const direction = view.getInt8(offset++) as -1 | 1;
        const moving = view.getUint8(offset++) === 1;
        const x = view.getInt16(offset, true);
        offset += 2;
        const y = view.getInt16(offset, true);
        offset += 2;

        const player = {
            id: playerId,
            direction,
            moving,
            attacking: false,
            position: { x, y },
        };

        // Other players
        const playerCount = view.getUint8(offset++);
        const players: Record<string, PlayerState> = {};

        for (let i = 0; i < playerCount; i++) {
            if (offset >= data.length) break;

            const { playerId: otherPlayerId, newOffset: newOffset2 } = this.decodePlayerId(data, offset);
            offset = newOffset2;

            if (offset + 4 > data.length) break;

            const direction2 = view.getInt8(offset++) as -1 | 1;
            const moving2 = view.getUint8(offset++) === 1;
            const x2 = view.getInt16(offset, true);
            offset += 2;
            const y2 = view.getInt16(offset, true);
            offset += 2;

            players[otherPlayerId] = {
                id: otherPlayerId,
                direction: direction2,
                moving: moving2,
                position: { x: x2, y: y2 },
            };
        }

        return { type: "initialState", player, players, timestamp };
    }

    private static decodePlayerJoined(data: Uint8Array, view: DataView) {
        const { playerId, newOffset } = this.decodePlayerId(data, 1);
        let offset = newOffset;

        const direction = view.getInt8(offset++) as -1 | 1;
        const moving = view.getUint8(offset++) === 1;

        return {
            type: "playerJoined",
            player: {
                id: playerId,
                direction,
                moving,
                position: {
                    x: view.getFloat32(offset, true),
                    y: view.getFloat32(offset + 4, true),
                },
            },
        };
    }

    private static decodePlayerLeft(data: Uint8Array, _view: DataView) {
        const { playerId } = this.decodePlayerId(data, 1);
        return { type: "playerLeft", playerId };
    }

    private static decodeCorrection(data: Uint8Array, view: DataView) {
        const { playerId, newOffset } = this.decodePlayerId(data, 1);
        return {
            type: "correction",
            playerId,
            position: {
                x: view.getFloat32(newOffset, true),
                y: view.getFloat32(newOffset + 4, true),
            },
        };
    }

    private static decodeMovementAck(data: Uint8Array, view: DataView) {
        const timestamp = view.getUint32(1, true);
        const { playerId, newOffset } = this.decodePlayerId(data, 5);

        return {
            type: "movementAck",
            playerId,
            acknowledgedPosition: {
                x: view.getInt16(newOffset, true),
                y: view.getInt16(newOffset + 2, true),
            },
            inputSequence: view.getUint32(newOffset + 4, true),
            timestamp,
        };
    }
}

