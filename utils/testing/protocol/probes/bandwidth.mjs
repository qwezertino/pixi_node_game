// Measures what the server actually puts on the wire under a configurable input
// profile, and reads back its own delta-composition metrics. Run it once with
// VELOCITY_REPLICATION=false and once with true for an A/B (see ab-velocity.sh).
//
// Direction-change rate is the variable that matters: velocity replication removes the
// records a client can dead-reckon, so its payoff falls as players change direction
// more often. Bots do not turn like people — treat the numbers as a shape, not a
// forecast, and prefer the same metrics gathered from live sessions.
import { connectAll, loadProtocol, sleep, DIRECTIONS, readMetrics } from '../lib/harness.mjs';

const CLIENTS = Number(process.env.CLIENTS ?? 12);
const TURNS_PER_SEC = Number(process.env.TURNS_PER_SEC ?? 1.5);
const SECONDS = Number(process.env.SECONDS ?? 10);

const protocol = await loadProtocol();
const clients = await connectAll(CLIENTS, protocol);
await sleep(600);

const ticksPerTurn = Math.max(1, Math.round(20 / TURNS_PER_SEC));
const direction = clients.map((_, i) => DIRECTIONS[i % DIRECTIONS.length]);

for (let t = 0; t < SECONDS * 20; t++) {
    clients.forEach((c, i) => {
        if (t > 0 && (t + i) % ticksPerTurn === 0) {
            direction[i] = DIRECTIONS[(t + i) % DIRECTIONS.length];
        }
        c.move(direction[i][0], direction[i][1]);
    });
    await sleep(1000 / 20);
}
await sleep(700);

const m = await readMetrics([
    'game_broadcast_records',
    'game_broadcast_payload_bytes',
    'game_delta_vector_changes',
    'game_delta_position_only',
    'game_delta_clamped_players',
    'game_delta_keyframes',
]);
clients.forEach((c) => c.close());

const frames = m.game_broadcast_records.count || 1;
const changed = m.game_delta_vector_changes.sum + m.game_delta_position_only.sum;

console.log(JSON.stringify({
    clients: CLIENTS,
    turnsPerSecPerClient: TURNS_PER_SEC,
    frames,
    recordsSent: m.game_broadcast_records.sum,
    payloadBytes: m.game_broadcast_payload_bytes.sum,
    recordsPerFrame: +(m.game_broadcast_records.sum / frames).toFixed(2),
    bytesPerFrame: +(m.game_broadcast_payload_bytes.sum / frames).toFixed(1),
    composition: {
        changedPerFrame: +(changed / frames).toFixed(2),
        vectorChangesPerFrame: +(m.game_delta_vector_changes.sum / frames).toFixed(2),
        predictablePerFrame: +(m.game_delta_position_only.sum / frames).toFixed(2),
        divergedPerFrame: +(m.game_delta_clamped_players.sum / frames).toFixed(2),
        keyframesPerFrame: +(m.game_delta_keyframes.sum / frames).toFixed(2),
        predictablePct: changed ? +(100 * m.game_delta_position_only.sum / changed).toFixed(1) : 0,
    },
}, null, 2));
process.exit(0);
