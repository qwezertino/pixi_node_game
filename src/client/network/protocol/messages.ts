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
    // Which step (1-indexed) of its combo chain the current/most recent attack is
    // — see AnimationController.startAttackAnimation, world.go executeAttack.
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
    // Held-Shift intent (GDD §54 sprint) — only takes effect server-side while
    // moving and while stamina remains, see server world.go updatePlayerPosition.
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
    // Simulation tick the records describe. Players absent from a delta are
    // dead-reckoned forward by the tick difference since the previous frame.
    worldTick: number;
    // Server's current time-dilation factor as a percentage (100 = nominal tick
    // rate, EVE-style TiDi). Scale local prediction step by dilationPct/100 to stay
    // in lockstep with a dilated server instead of running ahead of it.
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
    // units.Definition.typeId assigned by the server (see shared/units.ts) — the
    // server validates/falls back the client's requested unit, so this is the
    // authoritative value even when it doesn't match what was requested.
    unitType: number;
}

// entries maps playerId -> unit type + current HP/stamina. None of these change
// after spawn today (nothing drains HP or stamina yet — see server types.go
// UnitAssignment doc), so they're replicated on this low-frequency channel instead
// of every world-state record — see server broadcast.go sendUnitRoster.
export interface PlayerAttributes {
    unitType: number; // units.Definition.typeId, see shared/units.ts
    hp: number;
    stamina: number;
}

export interface UnitRosterMessage extends ServerMessage {
    type: 'unitRoster';
    entries: Record<string, PlayerAttributes>;
}

// Must match protocol.ProtocolVersion on the server. The wire format carries no
// per-record framing, so a mismatched decoder does not fail — it silently yields
// wrong player IDs. Checking the version turns that into a visible error.
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
