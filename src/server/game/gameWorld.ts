import { ServerWebSocket } from "bun";
import {
    PlayerState,
    PlayerPosition,
    TICK_RATE,
    SYNC_INTERVAL
} from "../../protocol/messages";
import { BinaryProtocol } from "../../protocol/binaryProtocol";
import { WORLD, MOVEMENT } from "../../common/gameSettings";


export class GameWorld {
    private players: Map<string, PlayerState> = new Map();
    private connections: Map<string, ServerWebSocket<any>> = new Map();


    constructor() {
        this.startGameLoop();

        setInterval(() => {
            this.sendFullSync();
        }, SYNC_INTERVAL);
    }

    private startGameLoop() {
        // Fixed time step (32 ticks per second = ~31.25ms per tick)
        const tickMs = 1000 / TICK_RATE;

        setInterval(() => {
            this.update();
        }, tickMs);
    }

    private update() {
        for (const [, playerState] of this.players.entries()) {
            const shouldMove = playerState.moving && playerState.movementVector && !playerState.attacking;

            if (shouldMove) {
                const { dx, dy } = playerState.movementVector!;

                const moveDistance = MOVEMENT.PLAYER_SPEED_PER_TICK;

                if (dx !== 0) {
                    playerState.position.x += dx * moveDistance;
                }
                if (dy !== 0) {
                    playerState.position.y += dy * moveDistance;
                }

                const clampedX = Math.max(WORLD.BOUNDARIES.MIN_X,
                    Math.min(WORLD.BOUNDARIES.MAX_X, playerState.position.x));
                const clampedY = Math.max(WORLD.BOUNDARIES.MIN_Y,
                    Math.min(WORLD.BOUNDARIES.MAX_Y, playerState.position.y));

                if (clampedX !== playerState.position.x || clampedY !== playerState.position.y) {
                    playerState.position.x = clampedX;
                    playerState.position.y = clampedY;
                }
            }
        }
    }

    private sendFullSync() {
        const gameStateMsg = {
            type: 'gameState' as const,
            players: Object.fromEntries(this.players.entries()),
            timestamp: Date.now()
        };

        const binaryData = BinaryProtocol.encodeGameState(gameStateMsg);

        for (const ws of this.connections.values()) {
            ws.send(binaryData);
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

        this.broadcastPlayerJoined(playerState);

        return playerState;
    }

    public removePlayer(playerId: string) {
        this.players.delete(playerId);
        this.connections.delete(playerId);

        this.broadcastPlayerLeft(playerId);
    }

    public updatePlayerMovement(playerId: string, dx: number, dy: number, inputSequence?: number): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        if (inputSequence !== undefined) {
            player.inputSequence = inputSequence;
        }

        if (dx !== 0 || dy !== 0) {
            player.movementVector = {
                dx: dx,
                dy: dy
            };

            player.moving = true;
        } else {
            player.moving = false;
            player.movementVector = { dx: 0, dy: 0 };
        }

        if (inputSequence !== undefined) {
            this.sendMovementAcknowledgment(playerId, player.position, inputSequence);
        }

        this.broadcastPlayerMovement(playerId, player.movementVector);

        return true;
    }

    public updatePlayerDirection(playerId: string, direction: -1 | 1): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        player.direction = direction;

        this.broadcastPlayerDirection(playerId, direction);

        return true;
    }

    public handlePlayerAttack(playerId: string): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        player.attacking = true;
        this.broadcastPlayerAttack(playerId, player.position);

        return true;
    }

    public handleAttackEnd(playerId: string): boolean {
        const player = this.players.get(playerId);
        if (!player) return false;

        player.attacking = false;
        return true;
    }

    private broadcastPlayerJoined(playerState: PlayerState) {
        const joinedMsg = {
            type: 'playerJoined' as const,
            player: playerState
        };

        const binaryData = BinaryProtocol.encodePlayerJoined(joinedMsg);

        for (const [id, ws] of this.connections.entries()) {
            if (id !== playerState.id) {
                ws.send(binaryData);
            }
        }
    }

    private sendMovementAcknowledgment(playerId: string, position: PlayerPosition, inputSequence: number): void {
        const connection = this.connections.get(playerId);
        if (!connection) return;

        const ackMsg = {
            type: 'movementAck' as const,
            playerId,
            acknowledgedPosition: position,
            inputSequence,
            timestamp: Date.now(),
        };

        const binaryData = BinaryProtocol.encodeMovementAcknowledgment(ackMsg);
        connection.send(binaryData);
    }

    private broadcastPlayerLeft(playerId: string) {
        const leftMsg = {
            type: 'playerLeft' as const,
            playerId
        };

        const binaryData = BinaryProtocol.encodePlayerLeft(leftMsg);

        for (const ws of this.connections.values()) {
            ws.send(binaryData);
        }
    }

        private broadcastPlayerMovement(playerId: string, movementVector: { dx: number; dy: number }) {
        const moveMsg = {
            type: 'playerMovement' as const,
            playerId,
            movementVector
        };

        const binaryData = BinaryProtocol.encodePlayerMovement(moveMsg);

        for (const [id, ws] of this.connections.entries()) {
            if (id !== playerId) {
                ws.send(binaryData);
            }
        }
    }

    private broadcastPlayerDirection(playerId: string, direction: -1 | 1) {
        const dirMsg = {
            type: 'playerDirection' as const,
            playerId,
            direction
        };

        const binaryData = BinaryProtocol.encodePlayerDirection(dirMsg);

        for (const [id, ws] of this.connections.entries()) {
            if (id !== playerId) {
                ws.send(binaryData);
            }
        }
    }

    private broadcastPlayerAttack(playerId: string, position: PlayerPosition) {
        const attackMsg = {
            type: 'playerAttack' as const,
            playerId,
            position
        };

        const binaryData = BinaryProtocol.encodePlayerAttack(attackMsg);

        for (const [id, ws] of this.connections.entries()) {
            if (id !== playerId) {
                ws.send(binaryData);
            }
        }
    }

    public sendCorrectionToPlayer(playerId: string) {
        const player = this.players.get(playerId);
        const connection = this.connections.get(playerId);

        if (!player || !connection) return;

        const correctionMsg = {
            type: 'correction' as const,
            playerId,
            position: player.position
        };

        const binaryData = BinaryProtocol.encodeCorrection(correctionMsg);

        connection.send(binaryData);
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


}