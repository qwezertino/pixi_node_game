// Every input must produce exactly one simulation step — no step lost, duplicated or
// reordered — across concurrent clients. This is the core guarantee of the input-queue
// model, and the one that breaks loudest if the movement path regresses.
import { GameClient, connectAll, loadProtocol, sleep, SPEED, report } from '../lib/harness.mjs';

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
await sleep(800);

// Each client's own ACKs are the authoritative record of what the server applied.
const fullyAcked = clients.filter((c) => c.lastAckSequence === STEPS).length;
const travels = [];
for (const watcher of clients) {
    for (const other of clients) {
        const belief = watcher.belief(other.id);
        const start = startX[clients.indexOf(other)];
        if (belief && start !== null) travels.push(belief.x - start);
    }
}
const expected = STEPS * SPEED;
const wrong = travels.filter((t) => t !== expected);
const failures = clients.flatMap((c) => c.decodeFailures);
const gaps = clients.flatMap((c) => c.sequenceGaps());

clients.forEach((c) => c.close());
report('determinism: one input, one step', [
    { label: 'clients fully acknowledged', actual: `${fullyAcked}/${CLIENTS}`, pass: fullyAcked === CLIENTS },
    { label: `travel is exactly ${expected}px`, actual: `${travels.length - wrong.length}/${travels.length} samples`, pass: travels.length > 0 && wrong.length === 0 },
    { label: 'decoder failures', actual: failures.length, pass: failures.length === 0 },
    { label: 'state sequence gaps', actual: gaps.length, pass: gaps.length === 0 },
], { protocolVersion: clients[0].protocolVersion, wrongTravels: wrong.slice(0, 5) });
