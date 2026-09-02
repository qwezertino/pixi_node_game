// Under velocity replication the server withholds a player whenever integrating its
// velocity reproduces the record it would have sent. This probe reconstructs a mover's
// position from velocity alone and checks it against that mover's own MOVEMENT_ACK —
// the server's authoritative answer for that exact input. Any drift shows up here.
import { GameClient, loadProtocol, sleep, DIRECTIONS, stats, report } from '../lib/harness.mjs';

const STEPS = Number(process.env.STEPS ?? 300);
const TURN_EVERY = Number(process.env.TURN_EVERY ?? 15);

const protocol = await loadProtocol();
const mover = await GameClient.connect(protocol);
const watcher = await GameClient.connect(protocol);
await sleep(600);

let direction = DIRECTIONS[0];
for (let t = 0; t < STEPS; t++) {
    if (t % TURN_EVERY === 0) direction = DIRECTIONS[(t / TURN_EVERY) % DIRECTIONS.length];
    mover.move(direction[0], direction[1]);
    await sleep(1000 / 20);
}
await sleep(900);

const belief = watcher.belief(mover.id);
const authoritative = mover.acks.get(mover.lastAckSequence);
const error = belief && authoritative
    ? Math.hypot(belief.x - authoritative.x, belief.y - authoritative.y)
    : null;

const reckonShare = watcher.deadReckoned / Math.max(watcher.deadReckoned + watcher.serverRecords, 1);
const failures = [...mover.decodeFailures, ...watcher.decodeFailures];

mover.close(); watcher.close();
report('dead reckoning: converges on the authoritative position', [
    { label: 'every input acknowledged', actual: `${mover.lastAckSequence}/${STEPS}`, pass: mover.lastAckSequence === STEPS },
    { label: 'reconstructed position error', actual: error === null ? 'no samples' : `${error.toFixed(2)}px`, pass: error === 0 },
    { label: 'decoder failures', actual: failures.length, pass: failures.length === 0 },
], {
    reckonedShare: `${(100 * reckonShare).toFixed(1)}%`,
    recordsPerFrame: +(watcher.records / Math.max(watcher.frames, 1)).toFixed(2),
    bytesPerFrame: +(watcher.bytes / Math.max(watcher.frames, 1)).toFixed(1),
});
