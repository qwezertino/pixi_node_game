export interface GameState {
    players: Uint32Array;
    position: Float64Array;
    velocity: Float64Array;
    lastInput: Uint32Array;
}
