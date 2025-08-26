// Protocol messages between client and server
import { NETWORK } from '../common/gameSettings';

// Game constants - импортируем из gameSettings
export const TICK_RATE = NETWORK.TICK_RATE;
export const SYNC_INTERVAL = NETWORK.SYNC_INTERVAL;

// Player related
export interface PlayerPosition {
    x: number;
    y: number;
}

export interface PlayerState {
    id: string;
    position: PlayerPosition;
    direction: -1 | 1;  // -1 for left, 1 for right
    moving: boolean;
    movementVector?: { dx: number; dy: number };
}

// Client to Server messages
export interface ClientMessage {
    type: string;
}

export interface JoinGameMessage extends ClientMessage {
    type: 'join';
}

export interface MoveMessage extends ClientMessage {
    type: 'move';
    movementVector: {
        dx: number;
        dy: number;
    };
}

export interface DirectionChangeMessage extends ClientMessage {
    type: 'direction';
    direction: -1 | 1; // -1 for left, 1 for right
}

export interface AttackMessage extends ClientMessage {
    type: 'attack';
    position: PlayerPosition;
}

// Server to Client messages
export interface ServerMessage {
    type: string;
}

export interface InitialStateMessage extends ServerMessage {
    type: 'initialState';
    player: PlayerState;
    players: Record<string, PlayerState>;
    timestamp: number;
}

export interface PlayerJoinedMessage extends ServerMessage {
    type: 'playerJoined';
    player: PlayerState;
}

export interface PlayerLeftMessage extends ServerMessage {
    type: 'playerLeft';
    playerId: string;
}

export interface PlayerMovementMessage extends ServerMessage {
    type: 'playerMovement';
    playerId: string;
    movementVector: {
        dx: number;
        dy: number;
    };
}

export interface PlayerDirectionMessage extends ServerMessage {
    type: 'playerDirection';
    playerId: string;
    direction: -1 | 1;
}

export interface PlayerAttackMessage extends ServerMessage {
    type: 'playerAttack';
    playerId: string;
    position: PlayerPosition;
}

export interface GameStateMessage extends ServerMessage {
    type: 'gameState';
    players: Record<string, PlayerState>;
    timestamp: number;
}

export interface ServerCorrectionMessage extends ServerMessage {
    type: 'correction';
    playerId: string;
    position: PlayerPosition;
}

// Compact binary protocol helpers
export enum MessageType {
    JOIN = 1,
    LEAVE = 2,
    MOVE = 3,
    DIRECTION = 4,
    ATTACK = 5,
    GAME_STATE = 6,
    CORRECTION = 7,
    INITIAL_STATE = 8,
    PLAYER_JOINED = 9,
    PLAYER_LEFT = 10,
}