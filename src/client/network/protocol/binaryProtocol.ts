import {
    MessageType,
    PlayerState,
    MoveMessage,
    DirectionChangeMessage,
    PlayerDirectionMessage,
    PlayerMovementMessage,
    GameStateMessage,
    PlayerJoinedMessage,
    PlayerLeftMessage,
    AttackMessage,
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

    private static decodePlayerId(data: Uint8Array, offset: number): { playerId: string; newOffset: number } {
        const decoder = new TextDecoder();
        const playerIdLength = data[offset];
        const playerId = decoder.decode(data.subarray(offset + 1, offset + 1 + playerIdLength));
        return { playerId, newOffset: offset + 1 + playerIdLength };
    }

    // Encode client messages
    static encodeMove(moveMsg: MoveMessage): Uint8Array {
        const buffer = new ArrayBuffer(14);
        const view = new DataView(buffer);
        view.setUint8(0, MessageType.MOVE);

        const dx = Math.sign(moveMsg.movementVector.dx) || 0;
        const dy = Math.sign(moveMsg.movementVector.dy) || 0;
        const packed = this.packMovement(dx, dy);

        view.setUint8(1, packed);
        view.setUint32(2, moveMsg.inputSequence, true);
        view.setUint32(6, moveMsg.position.x, true);
        view.setUint32(10, moveMsg.position.y, true);

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
            case MessageType.PLAYER_JOINED: return this.decodePlayerJoined(data, view);
            case MessageType.PLAYER_LEFT: return this.decodePlayerLeft(data, view);
            case MessageType.MOVEMENT_ACK: return this.decodeMovementAck(data, view);

            // Broadcast message types from server
            case 255: return this.decodePlayerMovementBroadcast(data, view);
            case 254: return this.decodePlayerDirectionBroadcast(data, view);
            case 253: return this.decodePlayerAttackBroadcast(data, view);

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
            }
        }

        // Client messages - ignore these on client side
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

        // Client message - ignore on client side
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

        // Client message - ignore on client side
        return null;
    }

    private static decodeAttackEnd() {
        return {
            type: "attackEnd",
        };
    }

    private static decodeGameState(data: Uint8Array, view: DataView): GameStateMessage {
        const playerCount = view.getUint32(1, true);
        const players: Record<string, PlayerState> = {};

        let offset = 5;
        for (let i = 0; i < playerCount; i++) {
            if (offset + 11 > data.length) break;

            const playerId = view.getUint32(offset, true).toString();
            offset += 4;

            const x = view.getUint16(offset, true);
            offset += 2;
            const y = view.getUint16(offset, true);
            offset += 2;

            // Skip vector bytes
            offset += 2;

            const flags = view.getUint8(offset);
            offset++;

            const direction = (flags & 0x80) ? 1 : -1;
            const state = flags & 0x7F;
            const moving = state === 1;
            const attacking = state === 2;

            players[playerId] = {
                id: playerId,
                direction,
                moving,
                attacking,
                position: { x, y },
            };
        }

        return {
            type: 'gameState',
            players,
            timestamp: Date.now()
        };
    }

    private static decodePlayerJoined(_data: Uint8Array, view: DataView): PlayerJoinedMessage {
        let offset = 1;

        const playerId = view.getUint32(offset, true).toString();
        offset += 4;

        const x = view.getUint16(offset, true);
        offset += 2;
        const y = view.getUint16(offset, true);
        offset += 2;

        // Skip vector bytes
        offset += 2;

        const flags = view.getUint8(offset);
        const direction = (flags & 0x80) ? 1 : -1;
        const state = flags & 0x7F;
        const moving = state === 1;
        const attacking = state === 2;

        return {
            type: 'playerJoined',
            player: {
                id: playerId,
                direction,
                moving,
                attacking,
                position: { x, y },
            },
        };
    }

    private static decodePlayerLeft(_data: Uint8Array, view: DataView): PlayerLeftMessage {
        const playerId = view.getUint32(1, true).toString();
        return {
            type: 'playerLeft',
            playerId
        };
    }

    private static decodeMovementAck(_data: Uint8Array, view: DataView) {
        const playerId = view.getUint32(1, true).toString();
        const x = view.getUint16(5, true);
        const y = view.getUint16(7, true);
        const inputSequence = view.getUint32(9, true);

        return {
            type: 'movementAck',
            playerId,
            position: { x, y },
            inputSequence,
        };
    }

    // Broadcast message decoders (types 255, 254, 253)
    private static decodePlayerMovementBroadcast(_data: Uint8Array, view: DataView): PlayerMovementMessage {
        let offset = 1; // Skip message type

        const playerId = view.getUint32(offset, true).toString();
        offset += 4;

        const dx = view.getInt8(offset);
        offset++;

        const dy = view.getInt8(offset);
        offset++;

        return {
            type: "playerMovement",
            playerId,
            movementVector: { dx, dy }
        };
    }

    private static decodePlayerDirectionBroadcast(_data: Uint8Array, view: DataView): PlayerDirectionMessage {
        let offset = 1; // Skip message type

        const playerId = view.getUint32(offset, true).toString();
        offset += 4;

        const direction = view.getUint8(offset) === 1 ? 1 : -1;
        offset++;

        return {
            type: "playerDirection",
            playerId,
            direction
        };
    }

    private static decodePlayerAttackBroadcast(_data: Uint8Array, view: DataView): PlayerAttackMessage {
        let offset = 1; // Skip message type

        const playerId = view.getUint32(offset, true).toString();
        offset += 4;

        const x = view.getUint16(offset, true);
        offset += 2;

        const y = view.getUint16(offset, true);
        offset += 2;

        return {
            type: "playerAttack",
            playerId,
            position: { x, y }
        };
    }
}
