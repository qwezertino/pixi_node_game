#!/usr/bin/env bash
# Builds the server, bundles the real client decoder, then runs every probe against a
# freshly started server. Any probe failing fails the run.
#
# Usage: utils/testing/protocol/run.sh [probe-name ...]
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HERE="$ROOT/utils/testing/protocol"
WORK="${TMPDIR:-/tmp}/pixi-protocol-probes"
PORT="${GAME_PORT:-8108}"
SERVER_PID=""

cleanup() {
    [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null
    wait "$SERVER_PID" 2>/dev/null
    rm -f "$ROOT/src/server/internal/config/gameConfig.json"
}
trap cleanup EXIT

mkdir -p "$WORK"

echo "▶ building server"
# The server embeds gameConfig.json; the Makefile stages it the same way.
cp "$ROOT/src/shared/gameConfig.json" "$ROOT/src/server/internal/config/"
(cd "$ROOT/src/server" && go build -o "$WORK/gameserver" ./cmd/server) || exit 1

echo "▶ bundling the client decoder"
# Probes decode with the shipped decoder, not a copy of it: a world-state frame has no
# per-record framing, so a drifted decoder yields wrong player IDs instead of an error.
(cd "$ROOT" && npx esbuild src/client/network/protocol/binaryProtocol.ts \
    --bundle --format=esm --platform=neutral --log-level=warning \
    --outfile="$HERE/lib/proto.mjs") || exit 1

echo "▶ starting server on :$PORT"
PORT="$PORT" STATIC_DIR="$WORK" "$WORK/gameserver" > "$WORK/server.log" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 40); do
    curl -sf "http://127.0.0.1:$PORT/health" > /dev/null && break
    sleep 0.25
done
curl -sf "http://127.0.0.1:$PORT/health" > /dev/null || { echo "server did not start"; tail -5 "$WORK/server.log"; exit 1; }

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
