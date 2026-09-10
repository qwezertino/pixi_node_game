#!/usr/bin/env bash
# Builds the server, bundles the real client decoder and movement formula, then runs
# every probe against a freshly started server. Any probe failing fails the run.
#
# Usage: utils/testing/protocol/run.sh [probe-name ...]
#
# Env:
#   GAME_PORT, GAME_MGMT_PORT  pin the game/metrics ports instead of picking free ones.
#   GAME_TEST_ISOLATED_DB=off  reuse whatever POSTGRES_HOST/REDIS_HOST already point at
#                              instead of spinning up scratch containers for this run.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HERE="$ROOT/utils/testing/protocol"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/pixi-protocol-probes.XXXXXX")"
SERVER_PID=""
PG_CONTAINER=""
REDIS_CONTAINER=""
DB_ENV=()

free_port() {
    node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})'
}

PORT="${GAME_PORT:-$(free_port)}"
MGMT_PORT="${GAME_MGMT_PORT:-$(free_port)}"

cleanup() {
    if [ -n "$SERVER_PID" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    if [ -n "$PG_CONTAINER" ]; then
        docker rm -f "$PG_CONTAINER" > /dev/null 2>&1 || true
    fi
    if [ -n "$REDIS_CONTAINER" ]; then
        docker rm -f "$REDIS_CONTAINER" > /dev/null 2>&1 || true
    fi
    rm -f "$WORK/gameserver.pid"
}
trap cleanup EXIT

mkdir -p "$WORK"

# The server (src/server/cmd/server/main.go) refuses to start without a reachable
# Postgres/Redis — game settings and units live only in the database now. Rather than
# depending on a shared dev stack (docker/docker-compose.yml) being up on the default
# ports, spin up scratch containers scoped to this run so probes never collide with, or
# mutate, real dev data. Set GAME_TEST_ISOLATED_DB=off to reuse an existing stack instead.
start_isolated_db() {
    if [ "${GAME_TEST_ISOLATED_DB:-auto}" = "off" ]; then
        echo "▶ GAME_TEST_ISOLATED_DB=off — reusing POSTGRES_HOST/REDIS_HOST as configured"
        return
    fi
    if ! command -v docker > /dev/null; then
        echo "✗ docker not found and GAME_TEST_ISOLATED_DB is not 'off'"
        echo "  refusing to fall back to POSTGRES_HOST/REDIS_HOST implicitly: this run would"
        echo "  EnsureSchema/Seed and subscribe against whatever those already point at,"
        echo "  which may be a real dev/CI database."
        echo "  install docker, or set GAME_TEST_ISOLATED_DB=off to explicitly opt in to"
        echo "  reusing the already-configured POSTGRES_HOST/REDIS_HOST."
        exit 1
    fi

    # The scratch fixture (DB_ENV, below) must be the only source of DB/Redis coordinates
    # for the server this script starts. A REDIS_PASSWORD inherited from the calling
    # shell's dev/CI environment would otherwise leak into this scratch run — the scratch
    # Redis started below has no password, so a client that tries to AUTH with one left
    # over from the host environment would simply fail to connect. Scoped to this
    # function, not the top of the script, so GAME_TEST_ISOLATED_DB=off still gets
    # whatever REDIS_PASSWORD the real stack it points at actually needs.
    unset REDIS_PASSWORD

    local pg_port redis_port suffix
    suffix="$$-$RANDOM"
    pg_port="$(free_port)"
    redis_port="$(free_port)"
    PG_CONTAINER="pixi-proto-pg-$suffix"
    REDIS_CONTAINER="pixi-proto-redis-$suffix"

    echo "▶ starting scratch Postgres (:$pg_port) and Redis (:$redis_port) for this run"
    docker run -d --rm --name "$PG_CONTAINER" \
        -p "127.0.0.1:$pg_port:5432" \
        -e POSTGRES_USER=game -e POSTGRES_PASSWORD=game -e POSTGRES_DB=game \
        -v "$ROOT/docker/postgres/init:/docker-entrypoint-initdb.d:ro" \
        postgres:16-alpine > /dev/null
    docker run -d --rm --name "$REDIS_CONTAINER" \
        -p "127.0.0.1:$redis_port:6379" \
        redis:7-alpine > /dev/null

    # The official postgres image restarts itself once mid-init (init DB, then relaunch
    # as the real server); pg_isready can report ready during the brief init-only window
    # and then drop the connection. Require a few consecutive successes to get past it.
    local pg_ready=0 pg_streak=0
    for _ in $(seq 1 120); do
        if docker exec "$PG_CONTAINER" pg_isready -U game -d game > /dev/null 2>&1; then
            pg_streak=$((pg_streak + 1))
            if [ "$pg_streak" -ge 3 ]; then
                pg_ready=1
                break
            fi
        else
            pg_streak=0
        fi
        sleep 0.5
    done
    [ "$pg_ready" -eq 1 ] || { echo "scratch Postgres did not become ready"; docker logs "$PG_CONTAINER" 2>&1 | tail -20 || true; exit 1; }

    local redis_ready=0
    for _ in $(seq 1 60); do
        if docker exec "$REDIS_CONTAINER" redis-cli ping > /dev/null 2>&1; then
            redis_ready=1
            break
        fi
        sleep 0.5
    done
    [ "$redis_ready" -eq 1 ] || { echo "scratch Redis did not become ready"; exit 1; }

    DB_ENV=(POSTGRES_HOST=127.0.0.1 "POSTGRES_PORT=$pg_port" POSTGRES_USER=game POSTGRES_PASSWORD=game POSTGRES_DB=game \
            REDIS_HOST=127.0.0.1 "REDIS_PORT=$redis_port")
}

echo "▶ building server"
(cd "$ROOT/src/server" && go build -o "$WORK/gameserver" ./cmd/server)

echo "▶ bundling the client decoder and movement formula"
# Probes decode with the shipped decoder, and predict movement with the shipped
# formula, rather than copies of either: a world-state frame has no per-record framing,
# so a drifted decoder yields wrong player IDs instead of an error, and a hand-rolled
# speed formula can hide or fake the exact rounding drift this is meant to catch.
# Bundled into this run's own $WORK, never into the repo tree, so concurrent runs (e.g.
# parallel CI jobs) never race over the same decoder/binary/log files.
(cd "$ROOT" && npx esbuild src/client/network/protocol/binaryProtocol.ts \
    --bundle --format=esm --platform=neutral --log-level=warning \
    --outfile="$WORK/proto.mjs")
(cd "$ROOT" && npx esbuild src/client/utils/movement.ts \
    --bundle --format=esm --platform=neutral --log-level=warning \
    --outfile="$WORK/movement.mjs")
export GAME_PROTOCOL_DIR="$WORK"

start_isolated_db

echo "▶ starting server on :$PORT (management :$MGMT_PORT)"
env "${DB_ENV[@]}" PORT="$PORT" MANAGEMENT_ADDR="127.0.0.1:$MGMT_PORT" STATIC_DIR="$WORK" \
    "$WORK/gameserver" > "$WORK/server.log" 2>&1 &
SERVER_PID=$!
echo "$SERVER_PID" > "$WORK/gameserver.pid"

health_ok=0
for _ in $(seq 1 40); do
    if curl -sf "http://127.0.0.1:$PORT/health" > /dev/null; then
        health_ok=1
        break
    fi
    sleep 0.25
done
if [ "$health_ok" -ne 1 ]; then
    echo "server did not start"
    tail -20 "$WORK/server.log"
    exit 1
fi

# A random port makes collision with an unrelated already-running server unlikely, but
# not impossible — confirm the socket that answered /health actually belongs to the PID
# this script just started, not some other process that happened to be listening there.
port_owner_pid() {
    if command -v lsof > /dev/null; then
        lsof -tiTCP:"$1" -sTCP:LISTEN 2>/dev/null | head -1 || true
    elif command -v ss > /dev/null; then
        ss -ltnp 2>/dev/null | awk -v p=":$1" '$4 ~ p"$" { match($0, /pid=[0-9]+/); if (RSTART) print substr($0, RSTART+4, RLENGTH-4) }' | head -1 || true
    else
        echo ""
    fi
}

owner="$(port_owner_pid "$PORT")"
if [ -n "$owner" ] && [ "$owner" != "$SERVER_PID" ]; then
    echo "port $PORT answered /health but is held by pid $owner, not the server this script started (pid $SERVER_PID)"
    exit 1
fi
if [ -z "$owner" ]; then
    echo "⚠ could not verify the listening pid on :$PORT (no lsof/ss) — trusting /health"
fi

export GAME_WS_URL="ws://127.0.0.1:$PORT/ws"
export GAME_METRICS_URL="http://127.0.0.1:$MGMT_PORT/metrics"
export GAME_HTTP_URL="http://127.0.0.1:$PORT"

PROBES=("$@")
if [ ${#PROBES[@]} -eq 0 ]; then
    PROBES=(determinism pacing dead-reckoning ack-flow resilience)
fi

failed=0
for probe in "${PROBES[@]}"; do
    node "$HERE/probes/$probe.mjs" || failed=1
done

# grep -c prints 0 and exits 1 when nothing matches, so the fallback must be an
# assignment rather than another echo into the substitution.
errors=$(grep -c '"level":"ERROR"' "$WORK/server.log" 2>/dev/null) || errors=0
echo ""
if [ "$errors" != "0" ]; then
    echo "✗ server logged $errors error(s)"
    grep '"level":"ERROR"' "$WORK/server.log" | head -3
    failed=1
else
    echo "✓ server log clean"
fi

[ "$failed" -eq 0 ] && echo "✓ all probes passed" || echo "✗ probes failed"
exit "$failed"
