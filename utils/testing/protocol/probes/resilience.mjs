// WebSocket/TCP already handles packet retransmission and ordering. The application
// must ignore an idempotent duplicate, but must fail closed rather than silently lose
// a transition when the rate limit is exceeded.
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
        inputSequence: duplicate,
    }));
}
await sleep(400);
const survivedDuplicate = client.ws.readyState === WebSocket.OPEN;

// A short malicious transition burst well over the configured token bucket.
for (let i = 0; i < 200 && client.ws.readyState === WebSocket.OPEN; i++) {
    client.sequence++;
    client.ws.send(protocol.encodeMove({
        type: 'move', movementVector: { dx: i % 2 ? 1 : 0, dy: 0 },
        inputSequence: client.sequence,
    }));
}
await sleep(800);
const failedClosed = client.ws.readyState !== WebSocket.OPEN;

const stillOpen = client.ws.readyState === WebSocket.OPEN;
client.close();
report('resilience: ordered input stream fails closed', [
    { label: 'survived duplicate sequences', actual: survivedDuplicate, pass: survivedDuplicate },
    { label: 'rate-limit burst closed connection', actual: failedClosed, pass: failedClosed },
]);
