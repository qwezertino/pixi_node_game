// server.js
import { Server, serve } from "bun";
import { World } from "@jakeklassen/ecs";
import { join } from "path";
import protobuf from "protobufjs";

// Load Protocol Buffers schema
const root = await protobuf.load(join(import.meta.dir, "protocol/game.proto"));
const PlayerInput = root.lookupType("PlayerInput");
const GameSnapshot = root.lookupType("GameSnapshot");

// ECS World
const world = new World();
const PLAYER_ARCHETYPE = world.registerArchetype(
    "position",
    "velocity",
    "lastInput"
);

// Spatial Grid
const GRID_SIZE = 1000;
const spatialGrid = new Map();

// Worker Thread for Physics
const physicsWorker = new Worker("./physics-worker.js", {
    smol: false, // Use full Worker API
});

// WebSocket Clients (Map<playerId, WebSocket>)
const clients = new Map();

// Game State
let gameState = {
    players: new Uint32Array(1000),
    position: new Float64Array(1000 * 2),
    velocity: new Float64Array(1000 * 2),
    lastInput: new Uint32Array(1000),
};

// Main Server
Bun.serve({
    port: 3000,
    websocket: {
        async open(ws) {
            // Rate limiting
            ws.lastInputTime = 0;

            // Create new player entity
            const playerId = world.createEntity(PLAYER_ARCHETYPE);
            gameState.position[playerId * 2] = Math.random() * 5000;
            gameState.position[playerId * 2 + 1] = Math.random() * 5000;

            clients.set(playerId, ws);
            ws.sendBinary(
                GameSnapshot.encode({
                    timestamp: Date.now(),
                    players: [
                        {
                            id: playerId,
                            x: gameState.position[playerId * 2],
                            y: gameState.position[playerId * 2 + 1],
                        },
                    ],
                }).finish()
            );
        },

        async message(ws, data) {
            // Rate limit (max 20 updates/sec)
            if (Date.now() - ws.lastInputTime < 50) return;
            ws.lastInputTime = Date.now();

            try {
                const input = PlayerInput.decode(new Uint8Array(data));

                // Update input buffer
                const playerId = [...clients.entries()].find(
                    ([, w]) => w === ws
                )?.[0];
                if (playerId === undefined) return;

                // Send input to physics worker
                physicsWorker.postMessage({
                    type: "input",
                    playerId,
                    dx: input.dx,
                    dy: input.dy,
                    seq: input.seq,
                });
            } catch (e) {
                console.error("Invalid message:", e);
            }
        },

        close(ws) {
            const playerId = [...clients.entries()].find(
                ([, w]) => w === ws
            )?.[0];
            if (playerId) {
                world.destroyEntity(playerId);
                clients.delete(playerId);
            }
        },
    },
});

// Physics Worker Communication
physicsWorker.onmessage = (event) => {
    const { type, results } = event.data;

    if (type === "physics-update") {
        // Update main game state
        gameState = results;
        updateSpatialGrid();
        broadcastUpdates();
    }
};

function updateSpatialGrid() {
    spatialGrid.clear();
    for (let i = 0; i < gameState.players.length; i++) {
        const x = gameState.position[i * 2];
        const y = gameState.position[i * 2 + 1];
        const gridKey = `${Math.floor(x / GRID_SIZE)},${Math.floor(
            y / GRID_SIZE
        )}`;

        if (!spatialGrid.has(gridKey)) {
            spatialGrid.set(gridKey, new Set());
        }
        spatialGrid.get(gridKey).add(i);
    }
}

function broadcastUpdates() {
    const snapshot = GameSnapshot.create({
        timestamp: Date.now(),
        players: [],
    });

    for (const [playerId, ws] of clients) {
        const x = gameState.position[playerId * 2];
        const y = gameState.position[playerId * 2 + 1];

        // Get nearby players (current grid + adjacent)
        const gridKey = `${Math.floor(x / GRID_SIZE)},${Math.floor(
            y / GRID_SIZE
        )}`;
        const visiblePlayers = new Set();

        for (let dx = -1; dx <= 1; dx++) {
            for (let dy = -1; dy <= 1; dy++) {
                const key = `${gridKey[0] + dx},${gridKey[1] + dy}`;
                spatialGrid.get(key)?.forEach((id) => visiblePlayers.add(id));
            }
        }

        // Build delta snapshot
        snapshot.players = Array.from(visiblePlayers).map((id) => ({
            id,
            x: gameState.position[id * 2],
            y: gameState.position[id * 2 + 1],
        }));

        ws.sendBinary(GameSnapshot.encode(snapshot).finish());
    }
}

// Game loop (60Hz)
setInterval(() => {
    physicsWorker.postMessage({
        type: "tick",
        state: gameState,
    });
}, 1000 / 60);
