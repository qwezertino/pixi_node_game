# pixi_node_game

A 2D multiplayer browser game. The client is built with TypeScript and Pixi.js; the server is a high-performance Go WebSocket server designed for up to ~12 000 simultaneous connections. Communication uses a compact binary protocol over WebSocket. Sprite assets cover six races: Dark Elves, Dwarves, High Elves, Humans, Items, and Orcs.

---

## Stack

| Layer | Technology |
|---|---|
| Client renderer | Pixi.js 8.6+ |
| Client language | TypeScript 5.7 |
| Client bundler | Vite 6 / Bun 1.2 |
| Server language | Go 1.23 |
| WebSocket library | Gorilla WebSocket 1.5.1 |
| Metrics | Prometheus (client_golang 1.23.2) |
| Monitoring | Prometheus + Grafana + Loki + Promtail |

---

## Prerequisites

| Tool | Version |
|---|---|
| Bun | 1.2+ |
| Go | 1.23+ (`/usr/local/go/bin/go`) |
| Docker + Compose | any recent |

---

## Quick start

### Development (local, no Docker)

```bash
# Install all dependencies
make install

# Terminal 1 — Vite dev server (http://localhost:8109, HMR)
make dev-client

# Terminal 2 — Go game server (http://localhost:8108)
make dev-server
```

Or run both together (server in background, Vite in foreground):

```bash
make dev
```

The Go server reads `.env` from the project root. Copy `.env.example` (if present) or create `.env` with any overrides before starting.

### Production build (local)

```bash
make build       # build client → dist/  +  server → dist/server
make run         # build + start server (loads .env)
```

### Docker

```bash
# First run — set up data directory permissions
make docker-init

# Build image and start all services (game + Prometheus + Grafana + Loki + Promtail)
make docker-upbuild

# Start without rebuilding
make docker-up

# Stop
make docker-down
```

Service URLs after `docker-up`:

| Service | URL | Default credentials |
|---|---|---|
| Game | http://localhost:8108 | — |
| Prometheus | http://localhost:9090 | — |
| Grafana | http://localhost:3000 | admin / admin |
| Loki | http://localhost:3100 | — |

Run `make docker-monitoring` to print the current URLs with resolved ports.

### Load testing (Artillery)

```bash
# Locally (artillery must be installed)
make load-test

# Via Docker (no local install needed)
make docker-test
```

Before a high-load run locally, raise the file descriptor limit:
```bash
ulimit -n 65536
```

---

## Make targets reference

| Target | Description |
|---|---|
| `make install` | `bun install` + `go mod tidy && go mod download` |
| `make build` | Build client and server |
| `make build-client` | Vite build → `dist/` |
| `make build-server` | Go build → `dist/server` (copies gameConfig.json for embed, cleans up after) |
| `make build-server-linux` | Same + `CGO_ENABLED=0 GOOS=linux` |
| `make build-release` | `build-client` + `build-server-linux` |
| `make dev-client` | Vite dev server on `:8109` with HMR |
| `make dev-server` | Build server + start with `.env` |
| `make dev` | Build server, then run server + Vite client in parallel |
| `make run` | Full build + start server |
| `make clean` | Remove `dist/` and temp build files |
| `make lint` | `golangci-lint run` |
| `make load-test` | Artillery load test (local) |
| `make docker-init` | Create and chown data directories for Prometheus/Grafana/Loki |
| `make docker-up` | Start Docker services without rebuilding |
| `make docker-upbuild` | Build image and start Docker services |
| `make docker-down` | Stop Docker services |
| `make docker-test` | Run Artillery load test inside Docker |
| `make docker-monitoring` | Print Prometheus and Grafana URLs |

---

## Key endpoints (game server, port 8108)

| Path | Description |
|---|---|
| `/ws` | WebSocket game connection |
| `/health` | JSON health check |
| `/metrics` | Prometheus metrics |
| `/metrics/json` | Legacy JSON metrics |
| `/debug/pprof/` | Go pprof profiling (block + mutex profilers enabled) |

---

## Configuration

Game rules (tick rate, world size, player speed, etc.) live in `src/shared/gameConfig.json` — the single source of truth shared between the TypeScript client and the Go server (embedded at compile time via `//go:embed`).

Server infrastructure (port, worker counts, rate limits, memory limits, etc.) is configured via environment variables, typically in `.env`. See `src/server/internal/config/config.go` for all supported variables.
