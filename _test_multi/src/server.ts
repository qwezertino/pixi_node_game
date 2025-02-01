import { Server, serve, WebSocket } from "bun";
import { World } from "@jakeklassen/ecs";
import { GameSnapshot, PlayerInput, PlayerUpdate } from "./protocol/game";
import { GameState } from "./gameState";
import { SpatialGrid } from "./spatialGrid";

const GRID_SIZE = 1000;
const TICK_RATE = 60;
const MAX_UPDATES_PER_SECOND = 20;

// ECS Setup
const PLAYER_ARCHETYPE = Symbol("player");
const world: World = new World();
world.registerArchetype(PLAYER_ARCHETYPE, "position", "velocity", "lastInput");

// Game State
let gameState: GameState = {
    players: new Uint32Array(1000),
    position: new Float64Array(1000 * 2),
    velocity: new Float64Array(1000 * 2),
    lastInput: new Uint32Array(1000),
};

// WebSocket Extensions
interface GameWebSocket extends WebSocket {
    lastInputTime: number;
    playerId?: number;
}

// Spatial Partitioning
const spatialGrid: SpatialGrid = new Map();

// Worker Thread Setup
const physicsWorker = new Worker("./src/physicsWorker.ts", {
    smol: false,
});

// Client Management
const clients = new Map<number, GameWebSocket>();

function updateSpatialGrid(): void {
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
        spatialGrid.get(gridKey)!.add(i);
    }
}

function broadcastUpdates(): void {
    const snapshot: GameSnapshot = {
        timestamp: Date.now(),
        players: [],
    };

    clients.forEach((ws, playerId) => {
        const x = gameState.position[playerId * 2];
        const y = gameState.position[playerId * 2 + 1];

        // Get nearby players
        const gridKey = `${Math.floor(x / GRID_SIZE)},${Math.floor(
            y / GRID_SIZE
        )}`;
        const visiblePlayers = new Set<number>();

        for (let dx = -1; dx <= 1; dx++) {
            for (let dy = -1; dy <= 1; dy++) {
                const [xCoord, yCoord] = gridKey.split(",").map(Number);
                const key = `${xCoord + dx},${yCoord + dy}`;
                spatialGrid.get(key)?.forEach((id) => visiblePlayers.add(id));
            }
        }

        // Build delta snapshot
        snapshot.players = Array.from(visiblePlayers).map((id) => ({
            id,
            x: gameState.position[id * 2],
            y: gameState.position[id * 2 + 1],
        }));

        ws.send(JSON.stringify(snapshot));
    });
}

// WebSocket Server
const server = serve<{ ws: GameWebSocket }>({
    port: 3000,
    websocket: {
        open: (ws: GameWebSocket) => {
            ws.lastInputTime = 0;
            const playerId = world.createEntity(PLAYER_ARCHETYPE);
            gameState.position[playerId * 2] = Math.random() * 5000;
            gameState.position[playerId * 2 + 1] = Math.random() * 5000;

            clients.set(playerId, ws);
            ws.playerId = playerId;

            const initialSnapshot: GameSnapshot = {
                timestamp: Date.now(),
                players: [
                    {
                        id: playerId,
                        x: gameState.position[playerId * 2],
                        y: gameState.position[playerId * 2 + 1],
                    },
                ],
            };

            ws.send(JSON.stringify(initialSnapshot));
        },

        message: (ws: GameWebSocket, message: string) => {
            if (Date.now() - ws.lastInputTime < 1000 / MAX_UPDATES_PER_SECOND)
                return;
            ws.lastInputTime = Date.now();

            try {
                const input = JSON.parse(message) as PlayerInput;
                if (!ws.playerId) return;

                physicsWorker.postMessage({
                    type: "input",
                    playerId: ws.playerId,
                    dx: input.dx,
                    dy: input.dy,
                    seq: input.seq,
                });
            } catch (e) {
                console.error("Invalid message:", e);
            }
        },

        close: (ws: GameWebSocket) => {
            if (ws.playerId) {
                world.destroyEntity(ws.playerId);
                clients.delete(ws.playerId);
            }
        },
    },
});

// Physics Worker Messages
physicsWorker.onmessage = (event: MessageEvent<PhysicsWorkerMessage>) => {
    if (event.data.type === "physics-update" && event.data.state) {
        gameState = event.data.state;
        updateSpatialGrid();
        broadcastUpdates();
    }
};

// Game Loop
setInterval(() => {
    physicsWorker.postMessage({
        type: "tick",
        state: gameState,
    });
}, 1000 / TICK_RATE);

console.log(`Server running at ${server.hostname}:${server.port}`);
