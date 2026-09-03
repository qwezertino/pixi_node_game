// Several transitions may land inside one replication interval. ACK coalescing must
// confirm the latest applied transition without requiring one ACK per simulation tick.
import { GameClient, loadProtocol, sleep, report } from '../lib/harness.mjs';

const protocol = await loadProtocol();
const client = await GameClient.connect(protocol);
await sleep(600);

for (const [dx, dy] of [[1, 0], [0, 1], [-1, 0], [0, 0]]) {
    client.move(dx, dy);
    await sleep(20);
}
await sleep(600);

const sent = client.sequence;
// Sample liveness before closing the socket ourselves.
const stillOpen = client.ws.readyState === WebSocket.OPEN;
const acked = client.lastAckSequence;
client.close();

report('ack flow: transition acknowledgements coalesce', [
    { label: 'all inputs acknowledged', actual: `${acked}/${sent}`, pass: acked === sent },
    { label: 'connection survived', actual: stillOpen ? 'open' : 'closed', pass: stillOpen },
], { transitions: sent });
