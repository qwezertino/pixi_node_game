// Transient client misbehaviour must cost a message, not the session. Duplicate and
// out-of-order sequences are produced by retransmits and reordering middleboxes, and a
// burst can come from a proxy flushing a stalled queue.
import { GameClient, loadProtocol, sleep, report } from '../lib/harness.mjs';

const protocol = await loadProtocol();
const client = await GameClient.connect(protocol);
await sleep(600);

for (let i = 0; i < 10; i++) { client.move(1, 0); await sleep(50); }
await sleep(300);

// Replay the same sequence number twice.
const duplicate = client.sequence;
for (let i = 0; i < 2; i++) {
    client.ws.send(protocol.encodeMove({
        type: 'move', movementVector: { dx: 1, dy: 0 },
        inputSequence: duplicate, position: { x: 0, y: 0 },
    }));
}
await sleep(400);
const survivedDuplicate = client.ws.readyState === WebSocket.OPEN;

// A short burst well over the steady-state send rate.
for (let i = 0; i < 200; i++) client.move(1, 0);
await sleep(800);
const survivedBurst = client.ws.readyState === WebSocket.OPEN;

// Still simulating afterwards?
const before = client.lastAckSequence;
for (let i = 0; i < 10; i++) { client.move(1, 0); await sleep(50); }
await sleep(500);
const stillLive = client.lastAckSequence > before;

const stillOpen = client.ws.readyState === WebSocket.OPEN;
client.close();
report('resilience: transient faults do not drop the session', [
    { label: 'connection still open at the end', actual: stillOpen ? 'open' : 'closed', pass: stillOpen },
    { label: 'survived duplicate sequences', actual: survivedDuplicate, pass: survivedDuplicate },
    { label: 'survived a rate-limit burst', actual: survivedBurst, pass: survivedBurst },
    { label: 'still simulating afterwards', actual: `${before} -> ${client.lastAckSequence}`, pass: stillLive },
]);
