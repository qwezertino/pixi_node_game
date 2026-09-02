#!/usr/bin/env bash
# A/B for velocity replication: same load, VELOCITY_REPLICATION off vs on.
# Reports records and bytes actually placed on the wire.
#
# Usage: utils/testing/protocol/ab-velocity.sh [clients] [turns-per-sec] [seconds]
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HERE="$ROOT/utils/testing/protocol"
WORK="${TMPDIR:-/tmp}/pixi-protocol-probes"
CLIENTS="${1:-12}"; TURNS="${2:-1.5}"; SECS="${3:-10}"
SERVER_PID=""

cleanup() { [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null; }
trap cleanup EXIT

mkdir -p "$WORK"
cp "$ROOT/src/shared/gameConfig.json" "$ROOT/src/server/internal/config/"
(cd "$ROOT/src/server" && go build -o "$WORK/gameserver" ./cmd/server) || exit 1
rm -f "$ROOT/src/server/internal/config/gameConfig.json"
(cd "$ROOT" && npx esbuild src/client/network/protocol/binaryProtocol.ts \
    --bundle --format=esm --platform=neutral --log-level=warning \
    --outfile="$HERE/lib/proto.mjs") || exit 1

run_mode() {
    VELOCITY_REPLICATION="$1" STATIC_DIR="$WORK" "$WORK/gameserver" > "$WORK/ab_$1.log" 2>&1 &
    SERVER_PID=$!
    for _ in $(seq 1 40); do
        curl -sf http://127.0.0.1:8108/health > /dev/null && break
        sleep 0.25
    done
    CLIENTS="$CLIENTS" TURNS_PER_SEC="$TURNS" SECONDS="$SECS" \
        node "$HERE/probes/bandwidth.mjs"
    kill "$SERVER_PID" 2>/dev/null; wait "$SERVER_PID" 2>/dev/null; SERVER_PID=""
}

echo "── velocity replication OFF ──"; run_mode false > "$WORK/off.json"; cat "$WORK/off.json"
echo "── velocity replication ON ───"; run_mode true  > "$WORK/on.json";  cat "$WORK/on.json"

node -e '
const off = require("'"$WORK"'/off.json"), on = require("'"$WORK"'/on.json");
const ratio = (a, b) => (b ? (a / b).toFixed(2) + "x" : "n/a");
console.log("\n── result ──");
console.log(`  records: ${off.recordsSent} -> ${on.recordsSent}  (${ratio(off.recordsSent, on.recordsSent)} fewer)`);
console.log(`  bytes:   ${off.payloadBytes} -> ${on.payloadBytes}  (${ratio(off.payloadBytes, on.payloadBytes)} fewer)`);
console.log("  The byte ratio trails the record ratio because the 13-byte frame header");
console.log("  is a large share of a small frame; at thousands of players they converge.");
'
