// Replication must land on a steady cadence. The client's interpolation delay is driven
// by the inter-arrival EWMA, so jitter here is paid for twice: fewer updates, and a
// larger delay to hide them. A 100ms interval sampled on a 50ms tick grid used to
// alternate 100/150ms — an effective 8Hz that pinned the delay at its 300ms ceiling.
import { GameClient, loadProtocol, sleep, stats, report } from '../lib/harness.mjs';

const SECONDS = Number(process.env.SECONDS ?? 8);

const protocol = await loadProtocol();
const mover = await GameClient.connect(protocol);
const watcher = await GameClient.connect(protocol);
await sleep(600);

watcher.frameArrivals.length = 0;
for (let i = 0; i < SECONDS * 20; i++) {
    mover.move(1, 0);
    await sleep(1000 / 20);
}
await sleep(600);

const intervals = watcher.arrivalIntervals().map((v) => Math.round(v));
const mean = stats.mean(intervals);
const stdev = stats.stdev(intervals);
const buckets = stats.histogram(intervals.map((v) => Math.round(v / 10) * 10));

mover.close(); watcher.close();
report('pacing: steady replication cadence', [
    { label: 'frames observed', actual: intervals.length, pass: intervals.length >= 10 },
    { label: 'interval stdev under 5ms', actual: `${stdev.toFixed(1)}ms`, pass: stdev < 5 },
    { label: 'no bimodal beat (<=2 buckets)', actual: Object.keys(buckets).length, pass: Object.keys(buckets).length <= 2 },
], { meanMs: +mean.toFixed(1), buckets });
