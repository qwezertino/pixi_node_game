import { parentPort } from "worker_threads";
import { PhysicsWorkerMessage, GameState } from "./gameState";

interface PhysicsState {
    position: Float64Array;
    velocity: Float64Array;
}

let currentState: PhysicsState | null = null;

parentPort!.on("message", (message: PhysicsWorkerMessage) => {
    switch (message.type) {
        case "tick":
            if (message.state) {
                currentState = {
                    position: message.state.position,
                    velocity: message.state.velocity,
                };
                processPhysics();
                parentPort!.postMessage({
                    type: "physics-update",
                    state: currentState,
                });
            }
            break;

        case "input":
            if (message.playerId !== undefined && currentState) {
                currentState.velocity[message.playerId * 2] =
                    (message.dx || 0) * 5;
                currentState.velocity[message.playerId * 2 + 1] =
                    (message.dy || 0) * 5;
            }
            break;
    }
});

function processPhysics(): void {
    if (!currentState) return;

    for (let i = 0; i < currentState.position.length / 2; i++) {
        currentState.position[i * 2] += currentState.velocity[i * 2];
        currentState.position[i * 2 + 1] += currentState.velocity[i * 2 + 1];

        currentState.position[i * 2] = Math.max(
            0,
            Math.min(5000, currentState.position[i * 2])
        );
        currentState.position[i * 2 + 1] = Math.max(
            0,
            Math.min(5000, currentState.position[i * 2 + 1])
        );
    }
}
