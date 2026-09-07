import { NETWORK } from '../../../shared/gameConfig';
import type { Direction } from '../../utils/animationLayout';

export type { Direction };

export const TICK_RATE = NETWORK.tickRate;
export const SYNC_INTERVAL = NETWORK.syncInterval;

export interface PlayerPosition {
    x: number;
    y: number;
}

export interface PlayerState {
    id: string;
    position: PlayerPosition;
    direction: Direction;
    moving: boolean;
    attacking?: boolean;
    blocking?: boolean;
    sprinting?: boolean;

    comboStep?: number;
    vx?: number;
    vy?: number;
    movementVector?: { dx: number; dy: number };
    inputSequence?: number;
}

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
    inputSequence: number;

    sprint: boolean;
}

export interface DirectionChangeMessage extends ClientMessage {
    type: 'direction';
    direction: Direction;
}

export interface AttackMessage extends ClientMessage {
    type: 'attack';
    position: PlayerPosition;
}

export interface AttackEndMessage extends ClientMessage {
    type: 'attackEnd';
}

export interface BlockStartMessage extends ClientMessage {
    type: 'blockStart';
}

export interface BlockEndMessage extends ClientMessage {
    type: 'blockEnd';
}

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
    direction: Direction;
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
    stateSequence?: number;

    worldTick: number;

    dilationPct: number;
}

export interface MovementAcknowledgmentMessage extends ServerMessage {
    type: 'movementAck';
    playerId: string;
    position: PlayerPosition;
    inputSequence: number;
}

export interface ServerCorrectionMessage extends ServerMessage {
    type: 'correction';
    playerId: string;
    position: PlayerPosition;
}

export interface WelcomeMessage extends ServerMessage {
    type: 'welcome';
    protocolVersion: number;
    tickRate: number;
    playerId: string;

    unitType: number;
}

export interface PlayerAttributes {
    unitType: number;
    hp: number;
    stamina: number;
}

export interface UnitRosterMessage extends ServerMessage {
    type: 'unitRoster';
    entries: Record<string, PlayerAttributes>;
}

export const PROTOCOL_VERSION = 12;

export enum MessageType {
    JOIN = 1,
    LEAVE = 2,
    MOVE = 3,
    DIRECTION = 4,
    ATTACK = 5,
    ATTACK_END = 6,
    GAME_STATE = 7,
    MOVEMENT_ACK = 8,
    CORRECTION = 9,
    INITIAL_STATE = 10,
    PLAYER_JOINED = 11,
    PLAYER_LEFT = 12,
    DELTA_GAME_STATE = 14,
    WELCOME = 15,
    SYNC_REQUEST = 16,
    PING = 17,
    PONG = 18,
    UNIT_ROSTER = 19,
    BLOCK_START = 20,
    BLOCK_END = 21,
}
