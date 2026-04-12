# pixi_node_game — Project Knowledge Base

> Quick reference for AI assistant. Read this before any task to avoid re-exploring the codebase.

---

## Stack

| Layer | Technology | Version |
|---|---|---|
| Client renderer | Pixi.js | 8.6+ |
| Client language | TypeScript | 5.7 |
| Client bundler | Vite | 6.x |
| Client pkg mgr | Bun | 1.2 |
| Server language | Go | 1.26.2 (at /usr/local/go/bin/go) |
| Server WS lib | Gorilla WebSocket | 1.5.1 |
| Rate limiting | golang.org/x/time/rate | 0.5.0 |
| Go module name | pixi_game_server | — |
| OS | Ubuntu (WSL2) | Linux |

---

## Directory Map

```
/
├── Dockerfile               # Multi-stage: bun→vite, go build, alpine runtime
├── docker-compose.yml       # Single service 'game', env_file: .env
├── .dockerignore
├── .env                     # Server infra overrides (NOT game rules)
├── .gitignore               # .env and dist/ are ignored
├── Makefile                 # Main build entrypoint
├── package.json             # scripts: dev:client, build:client
├── vite.config.ts           # devPort:8109, build→dist/
├── tsconfig.json
├── index.html               # Client entry
├── public/
│   ├── style.css
│   └── assets/              # Sprite sheets (DarkElves, Dwarves, HighElves, Humans, Items, Orcs)
├── src/
│   ├── shared/
│   │   ├── gameConfig.json  # SOURCE OF TRUTH for game rules (shared client+server)
│   │   └── gameConfig.ts    # TS re-export of gameConfig.json
│   ├── client/
│   │   ├── main.ts          # App entry: Pixi.js init, WebGL/Canvas detection
│   │   ├── controllers/
│   │   │   ├── animationController.ts
│   │   │   └── movementController.ts  # uses MOVEMENT.playerSpeedPerTick
│   │   ├── game/
│   │   │   └── playerManager.ts       # uses PLAYER.*, MOVEMENT.*
│   │   ├── network/
│   │   │   ├── networkManager.ts      # WebSocket via Web Worker
│   │   │   ├── networkWorker.ts       # Worker thread for WS
│   │   │   └── protocol/
│   │   │       ├── messages.ts        # TICK_RATE, SYNC_INTERVAL from NETWORK.*
│   │   │       └── binaryProtocol.ts
│   │   └── utils/
│   │       ├── coordinateConverter.ts  # uses WORLD.*
│   │       ├── fpsDisplay.ts
│   │       ├── inputManager.ts
│   │       └── spriteLoader.ts
│   └── server/
│       ├── go.mod           # module pixi_game_server, go 1.21 (runs on 1.26.2)
│       ├── Makefile         # Server-only build targets
│       ├── cmd/server/main.go  # Entry: optimizeRuntime() + config.Load() + server.New(cfg).Start()
│       └── internal/
│           ├── config/
│           │   ├── config.go     # Config structs + Load() function
│           │   ├── embedded.go   # //go:embed gameConfig.json
│           │   └── gameConfig.json  # TEMP FILE — copied here by Makefile before go build, deleted after
│           ├── game/
│           │   └── world.go      # GameWorld: sync.Map players, eventChan, ticker, visibilityManager
│           ├── protocol/
│           │   └── binary.go     # Encode/decode binary messages, message type constants
│           ├── server/
│           │   └── server.go     # HTTP+WS server, /ws /health /metrics endpoints
│           ├── systems/
│           │   ├── broadcast.go  # BroadcastManager: worker pool, batch sending
│           │   └── visibility.go # VisibilityManager: spatial grid 100-unit cells, LRU cache 1000 entries
│           └── types/
│               └── types.go      # Player (atomic fields), GameEvent, EventType, PlayerState
└── utils/
    └── testing/artillery/
        ├── artillery-config.yml      # Load test config
        └── artillery-processor.cjs  # Binary protocol client simulator
```

---

## Config System — Three-Layer Priority

```
ENV VARS (.env or system)           ← highest priority
    ↓ overrides
Embedded gameConfig.json            ← game rules (shared with client)
    ↓ overrides
Hardcoded defaults in config.go     ← server infra fallbacks
```

### gameConfig.json — Game Rules Only (client + server)

Fields: `network.{tickRate,syncInterval,batchIntervalMs}`, `movement.playerSpeedPerTick`,
`world.{virtualSize,spawnArea,boundaries}`, `player.{baseScale,animationSpeed}`,
`game.debugMode`, `colors.worldBackground`

**Client uses:** `NETWORK.tickRate`, `NETWORK.syncInterval`, `MOVEMENT.playerSpeedPerTick`,
`PLAYER.*`, `COLORS.*`, `WORLD.*`

### .env — Server Infrastructure Only

Vars: `PORT`, `HOST`, `MAX_CONNECTIONS`, `EVENT_CHANNEL_SIZE`, `SEND_CHANNEL_SIZE`,
`WORKERS`, `BROADCAST_WORKERS`, `READ_BUFFER_SIZE`, `WRITE_BUFFER_SIZE`,
`RATE_LIMIT_MSG_SEC`, `RATE_LIMIT_BURST`, `GOGC`, `GOMAXPROCS`, `GOMEMLIMIT`

Also supported (game-rule overrides in env): `TICK_RATE`, `SYNC_INTERVAL_SEC`,
`BATCH_INTERVAL_MS`, `PLAYER_SPEED`, `WORLD_WIDTH`, `WORLD_HEIGHT`, `SPAWN_MIN_X/MAX_X/MIN_Y/MAX_Y`

### Embed Gotcha ⚠️

`embedded.go` uses `//go:embed gameConfig.json`. This file must exist at
`src/server/internal/config/gameConfig.json` **at compile time**.
Makefile handles this: copies from `src/shared/gameConfig.json` before build, deletes after.
Dockerfile stage 2 does the same COPY before go build.

---

## Build Pipeline

### Make targets (root Makefile)

| Target | What it does |
|---|---|
| `make install` | `bun install` + `go mod tidy && go mod download` |
| `make build` | build-client + build-server |
| `make build-client` | `bun run build:client` → `dist/` |
| `make build-server` | copy gameConfig.json, go build → `dist/server`, cleanup |
| `make build-server-linux` | same + `CGO_ENABLED=0 GOOS=linux` |
| `make build-release` | build-client + build-server-linux (prod) |
| `make dev-client` | `bun run dev:client` (Vite HMR on :8109) |
| `make dev-server` | build-server + load .env + run `dist/server` |
| `make run` | build + load .env + run `dist/server` |
| `make clean` | rm -rf dist/ + server binaries |
| `make load-test` | artillery run artillery-config.yml |

### Go build flags
```
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o dist/server
```

### Docker build (root Dockerfile)
Stage 1: `oven/bun:1.2-alpine` → bun install → vite build → `dist/`
Stage 2: `golang:1.26.2-alpine` → go mod download → COPY gameConfig.json for embed → go build → `/app/server`
Stage 3: `alpine:3.23` → copy binary + static, user `gameserver`, ~25MB final image
```bash
docker compose up --build   # build + run
docker compose up -d        # detached
```

---

## Runtime Architecture

### Ports
- `:8108` — Go server (HTTP static files + WebSocket at `/ws`)
- `:8109` — Vite dev server (dev only)
- `/health` — JSON health endpoint
- `/metrics` — JSON metrics endpoint

### Server Concurrency Model (server.go + world.go)

```
WebSocket connections (per-player goroutines)
    → write to eventChan (buffered, size=100000)
        → N event worker goroutines drain channel (N = WORKERS, default=CPU count)
            → update sync.Map player state (atomic fields)
                → game loop (ticker at TICK_RATE Hz)
                    → BroadcastManager worker pool (BROADCAST_WORKERS, default=CPU*2)
                        → VisibilityManager spatial grid lookup
                            → per-player viewport-filtered send
```

### Key types (types.go)

```go
Player {
    ID, X, Y          uint32  // atomic
    VX, VY            int8    // -1, 0, 1
    FacingRight        uint32  // atomic bool
    State              uint32  // atomic
    ClientTick         uint32  // for reconciliation
    ViewportWidth/Height uint16
    ConnPtr            uintptr // *websocket.Conn
    MessageCount       uint64  // atomic
}

GameEvent { PlayerID, Type, VectorX, VectorY, FacingRight, ClientTick, Timestamp }

EventType: EventMove, EventAttack, EventFace, EventDisconnect, EventViewportUpdate
```

### VisibilityManager (visibility.go)
- Spatial grid with 100-unit cells over world 6000×3000
- Grid: 60×30 cells
- LRU cache for viewport queries (1000 entries)
- Players only receive state for entities in their viewport

---

## Binary Protocol

### Client → Server

| Type | ID | Size | Format |
|---|---|---|---|
| JOIN | 1 | — | — |
| LEAVE | 2 | — | — |
| MOVE | 3 | 14 bytes | `type(1) + packed_dxdy(1) + inputSeq(4) + x(4) + y(4)` |
| DIRECTION | 4 | 2 bytes | `type(1) + facing(1)` (-1=left, 1=right) |
| ATTACK | 5 | 9 bytes | `type(1) + x_f32(4) + y_f32(4)` |
| ATTACK_END | 6 | 1 byte | `type(1)` |
| VIEWPORT | 13 | custom | viewport update |

packed_dxdy: `dx+1 & 0x03 | (dy+1 & 0x03) << 2`

### Server → Client

GAME_STATE frame: `PlayerCount(4)` + N × 11 bytes per player:
`ID(4) + X(2) + Y(2) + VX(1) + VY(1) + (facingRight<<7 | state)(1)`

---

## World Settings (defaults from gameConfig.json)

| Setting | Value |
|---|---|
| World size | 6000 × 3000 |
| Spawn area | X:1500–3000, Y:500–1500 |
| Boundaries | 0–6000, 0–3000 |
| Player speed | 4 px/tick |
| Tick rate | 32 Hz |
| Sync interval | 30 s (full state resync) |
| Batch interval | 50 ms |
| Max connections | 12000 |

---

## Artillery Load Testing

Config: `utils/testing/artillery/artillery-config.yml`
Processor: `utils/testing/artillery/artillery-processor.cjs`

Target: `ws://localhost:8108/ws`
Active phase: ramp to ~1200 clients (arrivalRate:10, 60s ramp + 60s sustain)
Each virtual user: MOVE every 0.5s, DIRECTION 15% chance/2s, ATTACK 5% chance/5s

```bash
cd utils/testing/artillery
artillery run artillery-config.yml
artillery run artillery-config.yml --output report.json && artillery report report.json
```

OS tuning before high-load: `ulimit -n 65536`

---

## Client Architecture Notes

- `main.ts`: detects WebGL (checks UNMASKED_RENDERER for SwiftShader/Mesa/llvmpipe) → falls back to Canvas
- `networkManager.ts`: spawns Web Worker (`networkWorker.ts`) for WebSocket to avoid blocking main thread
- `movementController.ts` + `playerManager.ts`: both use `MOVEMENT.playerSpeedPerTick` for client-side prediction
- `coordinateConverter.ts`: converts world coords ↔ screen coords using `WORLD.virtualSize`
- `binaryProtocol.ts`: client-side decode of server binary frames

---

## Go Module

```
module pixi_game_server
go 1.21   ← minimum, runs on 1.26.2
require:
  github.com/gorilla/websocket v1.5.1
  golang.org/x/time v0.5.0
```

Go binary at: `/usr/local/go/bin/go` (added to PATH in ~/.bashrc and ~/.zshrc)

---

## Common Gotchas

1. **gameConfig.json must be copied before `go build`** — Makefile and Dockerfile do this; if doing manual go build from `src/server/`, copy first and clean up after.
2. **`dist/server` working directory** — binary expects `STATIC_DIR` env or defaults to `../dist` relative to its location. In Docker: `/app/static`.
3. **`make dev-server` loads .env** via `set -a && . ./.env && set +a` from project root.
4. **Docker healthcheck** uses `wget` (busybox), not `curl` — curl not installed in alpine:3.23 image.
5. **Player IDs start at 1000** in GameWorld for easy debugging.
6. **go.mod says `go 1.21`** but installed Go is 1.26.2 — compatible, no need to change go.mod unless using 1.26-specific features.
7. **bun.lock** (text format, bun 1.2+) — Dockerfile uses `COPY bun.lock` (not bun.lockb).
