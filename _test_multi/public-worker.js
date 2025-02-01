import { World } from "@jakeklassen/ecs";
import { parentPort } from "worker_threads";

const world = new World();
let currentState = null;

parentPort.onmessage = (event) => {
    const { type, state, playerId, dx, dy, seq } = event.data;

    if (type === "tick") {
        currentState = state;
        processPhysics();
        parentPort.postMessage({
            type: "physics-update",
            results: currentState,
        });
    } else if (type === "input") {
        // Update player velocity
        currentState.velocity[playerId * 2] = dx * 5; // 5px/frame
        currentState.velocity[playerId * 2 + 1] = dy * 5;
    }
};

function processPhysics() {
    // Simple velocity integration
    for (let i = 0; i < currentState.players.length; i++) {
        currentState.position[i * 2] += currentState.velocity[i * 2];
        currentState.position[i * 2 + 1] += currentState.velocity[i * 2 + 1];

        // Simple bounds checking
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
