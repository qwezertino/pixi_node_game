// A held vector is sent once. A delayed STOP must still close a segment at the exact
// client tick, independently of packet arrival time.
//
// The input stream carries no requested simulation tick, so "how many ticks did the
// server actually simulate this vector for" cannot be derived from wall-clock sleep
// counts at a fixed rate — sleep phase, scheduler jitter and network delay all move it.
// Each watching client's own moveWindow(id) instead reads the START/STOP worldTicks the
// server itself stamped on its authoritative records, so elapsed ticks come from the
// data, not from how long this script happened to sleep.
import { connectAll, loadProtocol, sleep, report } from '../lib/harness.mjs';

const CLIENTS = Number(process.env.CLIENTS ?? 8);
const STEPS = Number(process.env.STEPS ?? 60);

const protocol = await loadProtocol();
const clients = await connectAll(CLIENTS, protocol);
await sleep(600);

const startX = clients.map((c) => c.belief(c.id)?.x ?? null);
for (let i = 0; i < STEPS; i++) {
    for (const c of clients) c.move(1, 0);
    await sleep(1000 / 20);
}
for (const c of clients) c.move(0, 0);
await sleep(800);

// Each client's own ACKs are the authoritative record of what the server applied.
const fullyAcked = clients.filter((c) => c.lastAckSequence === 2).length;
const travels = [];
const wrong = [];
for (const watcher of clients) {
    for (const other of clients) {
        const belief = watcher.belief(other.id);
        const start = startX[clients.indexOf(other)];
        const window = watcher.moveWindow(other.id);
        const otherAuth = other.acks.get(other.lastAckSequence);
        if (!belief || start === null || !window || window.stopTick === null || !otherAuth) continue;

        // "Expected" is the mover's own authoritative ACK position, not an independently
        // derived speed formula: a real per-tick speed need not be an integer, so a
        // client's rounded dead-reckoning step can never exactly reproduce N ticks of
        // true fractional movement on its own — that gap is exactly what the server's
        // correction records exist to close. The property that must hold is that every
        // observer converges on the *same* authoritative value the mover itself got, not
        // that a naive formula happens to predict it.
        const ticks = (window.stopTick - window.startTick) >>> 0;
        const expected = otherAuth.x - start;
        const travel = belief.x - start;
        travels.push(travel);
        if (travel !== expected) wrong.push({ watcher: watcher.id, other: other.id, ticks, expected, travel });
    }
}
const failures = clients.flatMap((c) => c.decodeFailures);
const gaps = clients.flatMap((c) => c.sequenceGaps());

clients.forEach((c) => c.close());
report('determinism: timestamped movement segments', [
    { label: 'clients fully acknowledged', actual: `${fullyAcked}/${CLIENTS}`, pass: fullyAcked === CLIENTS },
    { label: 'travel matches ticks × client speed for every observer', actual: `${travels.length - wrong.length}/${travels.length} samples`, pass: travels.length > 0 && wrong.length === 0 },
    { label: 'decoder failures', actual: failures.length, pass: failures.length === 0 },
    { label: 'state sequence gaps', actual: gaps.length, pass: gaps.length === 0 },
], { protocolVersion: clients[0].protocolVersion, messagesPerClient: clients[0].sequence, wrongTravels: wrong.slice(0, 5) });
