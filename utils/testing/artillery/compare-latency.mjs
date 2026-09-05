// Controlled local A/B: same binary configuration and deterministic bot movement.
// Usage: node compare-latency.mjs /path/to/server /path/to/output
// CLIENTS=1200 RAMP_SECONDS=30 HOLD_SECONDS=45 MOVE_INTERVAL_MS=8000 PORT=18108
// This shares the host with the server: timer lag is reported, not hidden.
import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { performance } from 'node:perf_hooks';
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const WebSocket = require('ws');
const { attachLatencyProbe } = require('./latency-probe.cjs');
const [binary, directory] = process.argv.slice(2);
if (!binary || !directory) throw new Error('Provide server binary and output directory');
fs.mkdirSync(directory, { recursive: true });
const clients = Number(process.env.CLIENTS || 1200);
const ramp = Number(process.env.RAMP_SECONDS || 30)*1000;
const hold = Number(process.env.HOLD_SECONDS || 45)*1000;
const movementInterval = Number(process.env.MOVE_INTERVAL_MS || 8000);
const port = Number(process.env.PORT || 18108);
const base = process.env.SERVER_URL || `http://127.0.0.1:${port}`;
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const histograms = new Map(), counters = new Map();
let measuring = false;
const events = { emit(kind, name, value = 1) {
  if (!measuring) return;
  if (kind === 'counter') counters.set(name, (counters.get(name) || 0)+value);
  if (kind === 'histogram') {
    let h = histograms.get(name);
    if (!h) { h = { count: 0, sum: 0, max: 0, buckets: new Uint32Array(100001) }; histograms.set(name, h); }
    h.count++; h.sum += value; h.max = Math.max(h.max, value);
    h.buckets[Math.min(100000, Math.max(0, Math.ceil(value*10)))]++;
  }
} };
const log = fs.openSync(path.join(directory, 'server.log'), 'w');
// Explicit common settings, independent of secret-bearing application .env.
const child = process.env.SERVER_URL ? null : spawn(binary, [], { env: { ...process.env, PORT: String(port),
  GOGC: '400', GOMEMLIMIT: '2GiB', IP_CONN_RATE: '200', IP_CONN_BURST: '200',
  VELOCITY_REPLICATION: 'true', KEYFRAME_DIVISOR: '100', WRITE_BATCH_SIZE: '8',
}, stdio: ['ignore', log, log] });
let childError;
child?.on('error', err => { childError = err; });
const sockets = [], timers = [];
let closing = false, errors = 0, prematureCloses = 0;
function cleanup() {
  closing = true;
  for (const timer of timers) clearInterval(timer);
  for (const ws of sockets) ws.terminate();
  child?.kill('SIGTERM');
}
process.once('SIGINT', () => { cleanup(); process.exitCode = 130; });
process.once('SIGTERM', () => { cleanup(); process.exitCode = 143; });
async function save(endpoint, filename) {
  const res = await fetch(base+endpoint, { signal: AbortSignal.timeout(30000) });
  if (!res.ok) throw new Error(`${endpoint}: ${res.status}`);
  fs.writeFileSync(path.join(directory, filename), Buffer.from(await res.arrayBuffer()));
}
try {
  let ready = false;
  for (let i = 0; i < 80; i++) {
    if (childError) throw childError;
    if (child && child.exitCode !== null) throw new Error('Server exited during startup');
    try { const r = await fetch(base+'/health'); if (r.ok) { ready = true; break; } } catch {}
    await delay(100);
  }
  if (!ready) throw new Error('Server not ready');
  const start = performance.now();
  const connected = [];
  const vectors = [[1,0],[0,1],[-1,0],[0,-1],[1,1],[-1,-1],[0,0]];
  for (let i = 0; i < clients; i++) {
    const wait = start+i*ramp/clients-performance.now();
    if (wait > 0) await delay(wait);
    if (closing) throw new Error('Interrupted');
    const ws = new WebSocket(base.replace(/^http/, 'ws')+'/ws'); sockets.push(ws);
    ws.on('error', () => { errors++; });
    ws.on('close', () => { if (!closing) prematureCloses++; });
    connected.push(new Promise((resolve, reject) => {
      ws.once('error', reject);
      ws.once('open', () => {
        ws.off('error', reject);
        const probe = attachLatencyProbe(ws, events, { initialDelay: (i*37)%1000 });
        let sequence = 0, turn = i%vectors.length;
        function move() {
          if (ws.readyState !== 1) return;
          const [dx,dy] = vectors[turn++%vectors.length];
          const packet = Buffer.allocUnsafe(6); packet[0] = 3;
          packet[1] = (dx+1) | ((dy+1)<<2); packet.writeUInt32LE(++sequence, 2);
          probe.trackMove(sequence); ws.send(packet);
        }
        move(); timers.push(setInterval(move, movementInterval)); resolve();
      });
    }));
    // Attach a handler immediately so failed handshakes cannot become unhandled rejections during ramp.
    connected.at(-1).catch(() => {});
  }
  await Promise.all(connected);
  await delay(3000);
  await save('/metrics', 'before.prom');
  measuring = true;
  const measuredStart = performance.now();
  console.log(`Plateau: ${clients} clients for ${hold/1000}s, MOVE every ${movementInterval}ms`);
  await delay(hold);
  measuring = false;
  const seconds = (performance.now()-measuredStart)/1000;
  await save('/metrics', 'after.prom');
  const summary = { clients, seconds, movementInterval, errors, prematureCloses,
    counters: Object.fromEntries(counters), histograms: {} };
  for (const [name, h] of histograms) {
    const quantiles = {};
    for (const q of [0.5, 0.95, 0.99]) {
      let count = 0;
      for (let i = 0; i < h.buckets.length; i++) {
        count += h.buckets[i];
        if (count >= q*h.count) { quantiles[q] = i/10; break; }
      }
    }
    summary.histograms[name] = { count: h.count, mean: h.sum/h.count, max: h.max, quantiles };
  }
  fs.writeFileSync(path.join(directory, 'clients.json'), JSON.stringify(summary, null, 2));
  console.log(JSON.stringify(summary, null, 2));
  // Profile after the measured window so tracing overhead cannot distort A/B metrics.
  if (process.env.PROFILE !== '0') {
    console.log('Capturing CPU profile (10s) and runtime trace (1s) under the same load');
    await save('/debug/pprof/profile?seconds=10', 'cpu.pprof');
    await save('/debug/pprof/trace?seconds=1', 'runtime.trace');
  }
  if (errors || prematureCloses) process.exitCode = 1;
} finally {
  cleanup(); fs.closeSync(log);
}
