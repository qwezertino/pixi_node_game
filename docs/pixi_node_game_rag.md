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
| Server language | Go | 1.25 (module go 1.25.0, runtime binary golang:1.26.2-alpine in Docker) |
| Server WS lib | gobwas/ws | 1.4.0 (raw net.Conn, zero-copy framing via CompileFrame) |
| Prometheus client | prometheus/client_golang | 1.23.2 |
| Rate limiting | golang.org/x/time/rate | 0.5.0 |
| Go module name | pixi_game_server | — |
| OS | Ubuntu (WSL2) | Linux |

---

## Directory Map

```
/
├── Dockerfile               # Multi-stage: bun→vite, go build, alpine runtime
├── .env                     # Server infra overrides (NOT game rules); ignored by git
├── .gitignore               # .env and dist/ are ignored
├── Makefile                 # Main build + Docker entrypoint
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
│   │   │       └── binaryProtocol.ts  # encode/decode binary frames
│   │   └── utils/
│   │       ├── coordinateConverter.ts  # uses WORLD.*
│   │       ├── fpsDisplay.ts           # F3 toggle for detailed stats
│   │       ├── inputManager.ts
│   │       └── spriteLoader.ts
│   └── server/
│       ├── go.mod           # module pixi_game_server, go 1.23.0
│       ├── cmd/server/main.go  # Entry: optimizeRuntime() + config.Load() + server.New(cfg).Start()
│       └── internal/
│           ├── config/
│           │   ├── config.go        # Config structs + Load() function
│           │   ├── embedded.go      # //go:embed gameConfig.json
│           │   └── gameConfig.json  # Embedded config — synced from src/shared/ by Makefile; removed by make clean
│           ├── game/
│           │   └── world.go         # GameWorld: sync.Map players, delta tracking, ticker, VisibilityManager
│           ├── metrics/
│           │   └── metrics.go       # Prometheus metrics: players, ticks, events, broadcast, WS errors
│           ├── protocol/
│           │   └── binary.go        # Encode/decode binary messages, message type constants
│           ├── server/
│           │   ├── broadcast.go     # broadcastTick; connWriteQueue (lazy goroutine per conn); tickFrame pool
│           │   └── server.go        # HTTP+WS server; epoll setup; ping loop; pprof; rate limiting
│           ├── systems/
│           │   └── visibility.go    # VisibilityManager: spatial grid 100-unit cells, pool for temp IDs
│           └── types/
│               └── types.go         # Player (all atomic fields), GameEvent, EventType, PlayerState
├── docker/
│   ├── Dockerfile
│   ├── docker-compose.yml   # game + Prometheus + Grafana + Loki + Promtail + Artillery (profile:test)
│   ├── monitoring/
│   │   ├── prometheus.yml
│   │   ├── grafana/provisioning/{dashboards,datasources}
│   │   ├── loki/loki.yml
│   │   └── promtail/promtail.yml
│   └── data/                # Persistent volumes: prometheus/, grafana/, loki/
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

### gameConfig.json — Game Rules (client + server shared)

Location: `src/shared/gameConfig.json` — single source of truth.

```json
{
  "network":  { "tickRate": 30, "syncInterval": 30000, "batchIntervalMs": 50 },
  "movement": { "playerSpeedPerTick": 4 },
  "world": {
    "virtualSize": { "width": 6000, "height": 3000 },
    "spawnArea":   { "minX": 1500, "maxX": 3000, "minY": 500, "maxY": 1500 },
    "boundaries":  { "minX": 0, "maxX": 6000, "minY": 0, "maxY": 3000 }
  },
  "player":   { "baseScale": 2, "animationSpeed": 0.1, "attackDurationMs": 1000 },
  "game":     { "debugMode": false },
  "colors":   { "worldBackground": "#808080" }
}
```

**Client uses:** `NETWORK.tickRate`, `NETWORK.syncInterval`, `MOVEMENT.playerSpeedPerTick`, `PLAYER.*`, `COLORS.*`, `WORLD.*`

### .env — Server Infrastructure Only

| Variable | Default | Description |
|---|---|---|
| `PORT` | 8108 | Listen port |
| `HOST` | 0.0.0.0 | Listen host |
| `WORKERS` | CPU count | Epoll tick-worker goroutines |
| `MAX_CONNECTIONS` | 12000 | Max WebSocket connections |
| `RATE_LIMIT_MSG_SEC` | 120 | Per-connection message rate limit |
| `RATE_LIMIT_BURST` | 20 | Rate limit burst |
| `GOGC` | 400 | GC tuning (set in optimizeRuntime()) |
| `GOMAXPROCS` | CPU count | Runtime parallelism |
| `GOMEMLIMIT` | — | Go memory limit (read by runtime) |
| `STATIC_DIR` | ../dist | Path to static files |

Game-rule env overrides (take priority over gameConfig.json):
`TICK_RATE`, `SYNC_INTERVAL_SEC`, `PLAYER_SPEED`, `ATTACK_DURATION_MS`,
`WORLD_WIDTH`, `WORLD_HEIGHT`, `SPAWN_MIN_X`, `SPAWN_MAX_X`, `SPAWN_MIN_Y`, `SPAWN_MAX_Y`

### Embed Gotcha

`embedded.go` uses `//go:embed gameConfig.json`. The file must exist at
`src/server/internal/config/gameConfig.json` **at compile time**.
Makefile: copies from `src/shared/gameConfig.json` before build. The file is **not** deleted after build — only `make clean` removes it.
Dockerfile stage 2: same COPY before `go build`.
If doing a manual `go build`, just copy `src/shared/gameConfig.json` → `src/server/internal/config/gameConfig.json` first.

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
| `make build-release` | build-client + build-server-linux |
| `make dev-client` | Vite HMR on :8109 |
| `make dev-server` | build-server + load .env + run `dist/server` |
| `make dev` | build-server, then server in background + Vite in foreground |
| `make run` | build + load .env + run `dist/server` |
| `make clean` | rm -rf dist/ + temp config |
| `make lint` | golangci-lint run |
| `make load-test` | artillery run (local) |
| `make docker-init` | mkdir + chown data dirs (Prometheus=65534, Grafana=472, Loki=root) |
| `make docker-up` | docker compose up -d (no rebuild) |
| `make docker-upbuild` | docker compose up --build -d |
| `make docker-down` | docker compose down |
| `make docker-test` | artillery via docker compose profile=test |
| `make docker-monitoring` | print Prometheus + Grafana URLs |

### Go build flags
```
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o dist/server ./cmd/server/main.go
```

### Docker build (docker/Dockerfile)
- Stage 1 `client-builder`: `oven/bun:1.2-alpine` → `bun install --frozen-lockfile` → `bun run build:client` → `dist/`
- Stage 2 `server-builder`: `golang:1.26.2-alpine` → `go mod download` → COPY gameConfig.json for embed → go build → `/app/server`
- Stage 3 runtime: `alpine:3.23` → ca-certificates, user `gameserver`, copy binary + static → ~25 MB image

### Docker Compose services (docker/docker-compose.yml)

`-f docker/docker-compose.yml --project-name pixi_game --env-file .env`

| Service | Image | Port |
|---|---|---|
| game | local build | 8108 (→ `${PORT:-8108}`) |
| prometheus | prom/prometheus | 9090 (→ `${PROMETHEUS_PORT:-9090}`) |
| grafana | grafana/grafana | 3000 (→ `${GRAFANA_PORT:-3000}`) |
| loki | grafana/loki | 3100 (→ `${LOKI_PORT:-3100}`) |
| promtail | grafana/promtail | — (reads Docker socket) |
| artillery | artilleryio/artillery | profile=test only |

---

## Runtime Architecture

### Ports and endpoints
- `:8108` — Go server: HTTP static files + WebSocket `/ws`
- `:8109` — Vite dev server (dev only)
- `/health` — JSON health check
- `/metrics` — Prometheus metrics (via `promhttp.Handler()`)
- `/metrics/json` — Legacy JSON metrics
- `/debug/pprof/` — Go pprof (block + mutex profilers enabled at rate=1)

### Server Concurrency Model

```
Client WebSocket connections (Linux epoll EPOLLONESHOT, no per-connection read goroutine)
    → 2×GOMAXPROCS epoll read workers (persistent goroutines)
        → rate limiter (per-IP connection, per-connection message)
            → decode binary message
                → GameWorld.ProcessEvent() inline — all Player fields are atomic, no channel needed
                    → sendDirect() for movement ACK via Connection.writeCh

game loop ticker (30 Hz, single goroutine)
    → GOMAXPROCS tick workers — parallel position update + attack timeout
        → sequential ToState + delta tracking: compare vs prevStates
            → broadcastTick(all, changed, fullSync)
                → encode 1 WS frame (broadcastFramePool, ref-counted tickFrame, refs=N)
                    → non-blocking send into Connection.writeCh (chan writeJob, cap=4) per connection
                        → persistent write goroutine per connection (startWriteLoop)
                            → writes frame.frame under SetWriteDeadline(5 ms broadcast / 30 ms direct)
                            → frame.release() → ref-count 0 → returns to broadcastFramePool
                            → exits only on ctx.Done() or maxWriteFailures (150 consecutive)
```

**Write model (persistent goroutine + channel):** Each `Connection` has a `writeCh chan writeJob` (buffered 4). `broadcastTick` sends `writeJob{frame: *tickFrame}` non-blocking; direct messages (ACK, pong, initial state) send `writeJob{direct: []byte}`. One persistent goroutine per connection (`startWriteLoop`) blocks on the channel. `writeJob` is a 40-byte value struct — channel sends carry no heap allocation. Goroutines are long-lived, never created or destroyed per tick.

**Goroutine count:** `2×GOMAXPROCS` (epoll readers) + `GOMAXPROCS` (tick workers) + `1 per connection` (persistent write loops) + a few system goroutines. At 10 000 clients: ~10 050. GC scans write-goroutine stacks once per STW — it never creates or destroys them during gameplay.

**Delta tracking:** each tick computes which players changed state vs the previous tick; unchanged players are omitted in delta frames. A full sync is forced every `SYNC_INTERVAL` seconds.

**GC:** `GOGC=400` + `GOMEMLIMIT=2GiB` (set in `optimizeRuntime()` at startup). Default GoCollector replaced with STW-free runtime/metrics variant (avoids `ReadMemStats()` stop-the-world on Prometheus scrape).

### Key types (types.go)

```go
Player {
    ID              uint32  // atomic
    X, Y            uint32  // atomic (stores uint16)
    VX, VY          uint32  // atomic (stores int8: -1, 0, 1)
    FacingRight     uint32  // atomic bool (0/1)
    State           uint32  // atomic player state
    ClientTick      uint32  // atomic, for reconciliation
    AttackStartTime int64   // atomic ns timestamp of attack start (0 = not attacking)
    LastUpdate      int64   // atomic
    LastActivity    int64   // atomic
    JoinTime        time.Time
    MessageCount    uint64  // atomic
}

PlayerState { ID uint32; X, Y uint16; VX, VY int8; FacingRight bool; State uint8; ClientTick uint32 }

GameEvent { PlayerID, Type, VectorX, VectorY, FacingRight, ClientTick, Timestamp }

EventType: EventMove, EventAttack, EventFace
```

### VisibilityManager (systems/visibility.go)
- Flat array of `gridCell` structs, each with its own `sync.RWMutex` (avoids full-grid lock)
- Grid cell size: 100 world units
- World 6000×3000 → 60×30 = 1800 cells
- `playerCells sync.Map` tracks current cell per player for O(1) moves (`AddPlayer`, `RemovePlayer`, `MovePlayer`)
- Viewport-based culling (`GetVisibleIDs` / `ReleaseIDs`) removed — broadcasts go to all connections

---

## Binary Protocol

### Client → Server

| Type | ID | Size | Format |
|---|---|---|---|
| JOIN | 1 | 1 byte | `type(1)` |
| LEAVE | 2 | 1 byte | `type(1)` |
| MOVE | 3 | 6 bytes | `type(1) + packed_dxdy(1) + inputSeq_u32_LE(4)` |
| DIRECTION | 4 | 2 bytes | `type(1) + facing(1)` (0=left, 1=right) |
| ATTACK | 5 | 9 bytes | `type(1) + x_f32_LE(4) + y_f32_LE(4)` |
| ATTACK_END | 6 | 1 byte | `type(1)` |
| VIEWPORT | 13 | 5 bytes | `type(1) + width_u16_LE(2) + height_u16_LE(2)` |

`packed_dxdy`: `(dx+1 & 0x03) | ((dy+1 & 0x03) << 2)` — dx and dy each -1/0/1 packed into 4 bits total.

### Server → Client

| Type | ID | Description |
|---|---|---|
| GAME_STATE | 7 | Full state (also initial state on join): `PlayerCount(4)` + N×11 bytes |
| MOVEMENT_ACK | 8 | Input sequence acknowledgement |
| PLAYER_JOINED | 11 | Another player connected |
| PLAYER_LEFT | 12 | Another player disconnected |
| DELTA_GAME_STATE | 14 | Only players whose state changed this tick |

Per-player frame (11 bytes): `ID_u32(4) + X_u16(2) + Y_u16(2) + VX_i8(1) + VY_i8(1) + flags(1)`
`flags`: `(facingRight << 7) | state`

---

## World Settings (defaults from gameConfig.json)

| Setting | Value |
|---|---|
| World size | 6000 × 3000 |
| Spawn area | X: 1500–3000, Y: 500–1500 |
| Boundaries | 0–6000, 0–3000 |
| Player speed | 4 px/tick |
| Tick rate | 30 Hz |
| Sync interval | 30 s (full state resync period) |
| Max connections | 12 000 |
| Player scale | 2× |
| Animation speed | 0.1 |
| Attack duration | 1000 ms |
| Player IDs start at | 1000 (for easy debugging) |

---

## Prometheus Metrics (metrics/metrics.go)

All metrics use `promauto`. The default `GoCollector` is replaced with the STW-free runtime/metrics variant (avoids `ReadMemStats()` stop-the-world on Prometheus scrape).

| Metric | Type | Description |
|---|---|---|
| `game_players_connected` | Gauge | Current connected players |
| `game_connections_total` | Counter | Total connections ever |
| `game_disconnections_total` | Counter | Total disconnections |
| `game_session_duration_seconds` | Histogram | Session duration |
| `game_tick_duration_seconds` | Histogram | Time per game tick |
| `game_ticks_total` | Counter | Total ticks processed |
| `game_events_processed_total{type}` | Counter | Events by type |
| `game_messages_received_total{type}` | Counter | Messages by type |
| `game_messages_rate_limited_total` | Counter | Messages dropped by rate limiter |
| `game_bytes_received_total` | Counter | Total bytes received |
| `game_broadcasts_dropped_total` | Counter | Tick frames dropped (write channel full) |
| `game_bytes_sent_total` | Counter | Total bytes sent |
| `game_ws_upgrade_errors_total` | Counter | WS upgrade failures |
| `game_ws_read_errors_total` | Counter | WS read errors |
| `game_ws_write_errors_total` | Counter | WS write errors |
| `game_ip_rate_limited_total` | Counter | Connections rejected by IP rate limiter |
| `game_tick_phase_seconds{phase}` | Histogram | Time per tick phase (range/delta/encode/shard_send) |
| `game_delta_players_count` | Histogram | Players with changed state per tick |
| `game_delta_ratio` | Gauge | Fraction of players with changed state (0.0–1.0) |

---

## Artillery Load Testing

Config: `utils/testing/artillery/artillery-config.yml`
Processor: `utils/testing/artillery/artillery-processor.cjs`

Target: `ws://localhost:8108/ws`
Active phase: ramp to ~1200 clients (arrivalRate: 10, 60 s ramp + 60 s sustain)
Each virtual user: MOVE every 0.5 s, DIRECTION 15% chance/2 s, ATTACK 5% chance/5 s

```bash
# Local (artillery installed)
make load-test

# Via Docker (no install needed)
make docker-test

# With JSON report
cd utils/testing/artillery
artillery run artillery-config.yml --output report.json && artillery report report.json

# OS tuning before high-load
ulimit -n 65536
```

---

## Client Architecture Notes

- `main.ts`: detects WebGL renderer via `WEBGL_debug_renderer_info`; falls back to Canvas for SwiftShader/Mesa/llvmpipe
- `networkManager.ts`: spawns Web Worker (`networkWorker.ts`) for WebSocket — keeps WS off the main thread
- `fpsDisplay.ts`: shows FPS, ping, player count; F3 key toggles detailed stats
- `movementController.ts` + `playerManager.ts`: client-side prediction using `MOVEMENT.playerSpeedPerTick`
- `coordinateConverter.ts`: world ↔ screen coordinate conversion using `WORLD.virtualSize`
- `binaryProtocol.ts`: encodes all client messages and decodes server frames

---

## Go Module

```
module pixi_game_server
go 1.25.0
require:
  github.com/gobwas/ws v1.4.0
  github.com/gobwas/httphead v0.1.0   // indirect
  github.com/gobwas/pool v0.2.1       // indirect
  github.com/prometheus/client_golang v1.23.2
  golang.org/x/sys v0.43.0
  golang.org/x/time v0.5.0
```

Go binary at: `/usr/local/go/bin/go` (in PATH via ~/.bashrc and ~/.zshrc)

---

## Common Gotchas

1. **gameConfig.json must be copied before `go build`** — Makefile and Dockerfile handle this automatically; for manual builds, copy `src/shared/gameConfig.json` → `src/server/internal/config/gameConfig.json`. It is **not** deleted after build — `make clean` removes it.
2. **`dist/server` working directory** — binary resolves `STATIC_DIR` from env; default is `../dist` relative to the binary. In Docker: `/app/static`.
3. **All `make dev-*` and `make run` targets** load `.env` via `set -a && . ./.env && set +a` from project root.
4. **Docker healthcheck** uses `wget` (busybox), not `curl` — curl is not installed in alpine:3.23.
5. **bun.lock** (text format, bun 1.2+) — Dockerfile uses `COPY bun.lock` not `bun.lockb`.
6. **docker-compose.yml is in `docker/`**, not root — all compose commands use `-f docker/docker-compose.yml`. The Makefile `COMPOSE` variable handles this.
7. **First Docker run**: always run `make docker-init` first to create and chown `docker/data/` subdirectories (Prometheus needs uid 65534, Grafana needs uid 472).
8. **pprof is always on** — `/debug/pprof/` is registered in production; block and mutex profilers are set to rate=1 (every event). Disable or restrict access behind a firewall for public deployments.
9. **Tick rate default changed** from 32 Hz (old RAG) to 30 Hz — source of truth is `gameConfig.json`.
10. **No lazy drain goroutines / no event channel** — the old architecture (`BROADCAST_WORKERS`, per-player send channel, `eventChan` with N worker goroutines) is replaced. Writes go through `Connection.writeCh chan writeJob` with one persistent `startWriteLoop` goroutine per connection. Event processing is inline (`ProcessEvent()`) — all Player fields are atomic. `BROADCAST_WORKERS`, `SEND_CHANNEL_SIZE`, and `EVENT_CHANNEL_SIZE` env vars are no longer read.
11. **gobwas/ws, not gorilla/websocket** — the server uses raw `net.Conn` with `ws.Upgrade()` and `ws.CompileFrame()`. No `WriteMessage`/`ReadMessage` API. Pong frames are pushed via `writeQueue.push()` (not inline, to avoid mutex contention with the drain goroutine).
