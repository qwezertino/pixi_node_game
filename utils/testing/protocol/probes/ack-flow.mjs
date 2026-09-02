// Movement ACKs must not ride on the delta payload. A player whose inputs change no
// replicated field — stopped, or held against a world boundary — still advances its
// applied input sequence. If the ACK depended on a non-empty delta, that client would
// never prune its pending-input ring and would be disconnected once it overflowed.
import { GameClient, loadProtocol, sleep, report } from '../lib/harness.mjs';

const NOOP_STEPS = Number(process.env.NOOP_STEPS ?? 40);

const protocol = await loadProtocol();
const client = await GameClient.connect(protocol);
await sleep(600);

// Warm up with real movement so a baseline exists on both sides.
for (let i = 0; i < 10; i++) { client.move(1, 0); await sleep(50); }
await sleep(400);

// The first STOP changes velocity. Every STOP after it is a no-op for every replicated
// field, yet each one still has to come back acknowledged.
client.move(0, 0);
await sleep(400);
const acksBefore = client.lastAckSequence;

for (let i = 0; i < NOOP_STEPS; i++) { client.move(0, 0); await sleep(50); }
await sleep(600);

const sent = client.sequence;
// Sample liveness before closing the socket ourselves.
const stillOpen = client.ws.readyState === WebSocket.OPEN;
const acked = client.lastAckSequence;
client.close();

report('ack flow: acknowledgements survive an empty delta', [
    { label: 'all inputs acknowledged', actual: `${acked}/${sent}`, pass: acked === sent },
    { label: 'acknowledgements advanced during no-op steps', actual: `${acksBefore} -> ${acked}`, pass: acked > acksBefore },
    { label: 'connection survived', actual: stillOpen ? 'open' : 'closed', pass: stillOpen },
], { noopSteps: NOOP_STEPS });
