/**
 * Optimized binary protocol with player ID caching for high-performance messaging
 * Reduces overhead by using short player IDs for frequent messages
 */

export class OptimizedBinaryProtocol {
    private static playerIdMap = new Map<string, number>(); // UUID -> short ID
    private static reversePlayerIdMap = new Map<number, string>(); // short ID -> UUID
    private static nextPlayerId = 1;

    /**
     * Register a player and get their short ID
     */
    static registerPlayer(playerId: string): number {
        if (!this.playerIdMap.has(playerId)) {
            const shortId = this.nextPlayerId++;
            this.playerIdMap.set(playerId, shortId);
            this.reversePlayerIdMap.set(shortId, playerId);
            return shortId;
        }
        return this.playerIdMap.get(playerId)!;
    }

    /**
     * Unregister a player
     */
    static unregisterPlayer(playerId: string): void {
        const shortId = this.playerIdMap.get(playerId);
        if (shortId) {
            this.playerIdMap.delete(playerId);
            this.reversePlayerIdMap.delete(shortId);
        }
    }

    /**
     * Get short ID for player
     */
    static getShortId(playerId: string): number {
        return this.playerIdMap.get(playerId) || 0;
    }

    /**
     * Get full player ID from short ID
     */
    static getFullId(shortId: number): string {
        return this.reversePlayerIdMap.get(shortId) || "";
    }

    /**
     * Encode movement with short player ID (3 bytes total)
     */
    static encodeOptimizedMovement(playerId: string, dx: number, dy: number): Uint8Array {
        const buffer = new ArrayBuffer(4);
        const view = new DataView(buffer);

        const shortId = this.getShortId(playerId);
        const packed = this.packMovement(dx, dy);

        view.setUint8(0, 0x01); // Message type: movement
        view.setUint16(1, shortId, true); // Player short ID (2 bytes)
        view.setUint8(3, packed); // Movement data (1 byte)

        return new Uint8Array(buffer);
    }

    /**
     * Encode direction with short player ID (4 bytes total)
     */
    static encodeOptimizedDirection(playerId: string, direction: number): Uint8Array {
        const buffer = new ArrayBuffer(4);
        const view = new DataView(buffer);

        const shortId = this.getShortId(playerId);

        view.setUint8(0, 0x02); // Message type: direction
        view.setUint16(1, shortId, true); // Player short ID
        view.setInt8(3, direction); // Direction

        return new Uint8Array(buffer);
    }

    /**
     * Encode attack with short player ID and compressed position
     */
    static encodeOptimizedAttack(playerId: string, x: number, y: number): Uint8Array {
        const buffer = new ArrayBuffer(7);
        const view = new DataView(buffer);

        const shortId = this.getShortId(playerId);

        view.setUint8(0, 0x03); // Message type: attack
        view.setUint16(1, shortId, true); // Player short ID
        view.setUint16(3, Math.round(x), true); // X position as int16
        view.setUint16(5, Math.round(y), true); // Y position as int16

        return new Uint8Array(buffer);
    }

    /**
     * Decode optimized message
     */
    static decodeOptimizedMessage(data: Uint8Array): any {
        if (data.length < 3) return null;

        const view = new DataView(data.buffer, data.byteOffset);
        const type = view.getUint8(0);
        const shortId = view.getUint16(1, true);
        const playerId = this.getFullId(shortId);

        if (!playerId) return null;

        switch (type) {
            case 0x01: // Movement
                if (data.length < 4) return null;
                const packed = view.getUint8(3);
                const { dx, dy } = this.unpackMovement(packed);
                return {
                    type: 'playerMovement',
                    playerId,
                    movementVector: { dx, dy }
                };

            case 0x02: // Direction
                if (data.length < 4) return null;
                const direction = view.getInt8(3);
                return {
                    type: 'playerDirection',
                    playerId,
                    direction
                };

            case 0x03: // Attack
                if (data.length < 7) return null;
                const x = view.getUint16(3, true);
                const y = view.getUint16(5, true);
                return {
                    type: 'playerAttack',
                    playerId,
                    position: { x, y }
                };
        }

        return null;
    }

    /**
     * Pack movement into single byte
     */
    private static packMovement(dx: number, dy: number): number {
        let packed = 0;
        packed |= (dx + 1) & 0x03; // dx: -1->0, 0->1, 1->2 (2 bits)
        packed |= ((dy + 1) & 0x03) << 2; // dy: same, shifted 2 bits
        return packed;
    }

    /**
     * Unpack movement from single byte
     */
    private static unpackMovement(packed: number): { dx: number; dy: number } {
        const dx = (packed & 0x03) - 1;
        const dy = ((packed >> 2) & 0x03) - 1;
        return { dx, dy };
    }

    /**
     * Batch multiple movement updates into single message
     */
    static encodeBatchedMovements(movements: Array<{playerId: string, dx: number, dy: number}>): Uint8Array {
        const buffer = new ArrayBuffer(2 + movements.length * 3); // header + 3 bytes per movement
        const view = new DataView(buffer);

        view.setUint8(0, 0x10); // Batch movement type
        view.setUint8(1, movements.length); // Count

        let offset = 2;
        for (const movement of movements) {
            const shortId = this.getShortId(movement.playerId);
            const packed = this.packMovement(movement.dx, movement.dy);

            view.setUint16(offset, shortId, true);
            view.setUint8(offset + 2, packed);
            offset += 3;
        }

        return new Uint8Array(buffer);
    }

    /**
     * Get stats
     */
    static getStats(): { registeredPlayers: number; nextId: number } {
        return {
            registeredPlayers: this.playerIdMap.size,
            nextId: this.nextPlayerId
        };
    }
}
