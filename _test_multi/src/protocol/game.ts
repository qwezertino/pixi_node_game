export interface PlayerInput {
    seq: number;
    dx: number;
    dy: number;
}

export interface GameSnapshot {
    timestamp: number;
    players: PlayerUpdate[];
}

export interface PlayerUpdate {
    id: number;
    x: number;
    y: number;
}
