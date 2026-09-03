// Shared harness for the protocol probes.
//
// The probes decode server frames with the REAL client decoder, bundled straight out
// of src/client by run.sh. That is deliberate: a hand-written decoder in the test would
// drift from the shipped one, and a world-state frame carries no per-record framing —
// a mismatched decoder does not fail, it silently yields wrong player IDs. Bundling the
// real one is what makes a wire-format change verifiable at all.

import { MOVEMENT, NETWORK } from './config.mjs';

const DEFAULT_URL = process.env.GAME_WS_URL ?? 'ws://127.0.0.1:8108/ws';
const METRICS_URL = process.env.GAME_METRICS_URL ?? 'http://127.0.0.1:8108/metrics';

export const SPEED = MOVEMENT.playerSpeedPerTick;
export const TICK_RATE = NETWORK.tickRate;

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// The eight movement vectors a client can express.
export const DIRECTIONS = [
    [1, 0], [0, 1], [1, 1], [-1, 0],
    [0, -1], [-1, 1], [1, -1], [-1, -1],
];

let BinaryProtocol = null;

/** run.sh bundles src/client/network/protocol/binaryProtocol.ts to lib/proto.mjs. */
export async function loadProtocol() {
    if (!BinaryProtocol) {
        ({ BinaryProtocol } = await import('./proto.mjs'));
    }
    return BinaryProtocol;
}

/**
 * One connected client. Tracks everything the probes assert on: its own identity and
 * ACKs, plus a dead-reckoned view of every other player built the same way
 * playerManager.ts builds it.
 */
export class GameClient {
    constructor(protocol) {
        this.protocol = protocol;
        this.id = null;
        this.protocolVersion = null;
        this.sequence = 0;
        this.vector = { dx: 0, dy: 0 };

        this.acks = new Map();          // inputSequence -> authoritative position
        this.lastAckSequence = null;
        this.stateSequences = [];
        this.frames = 0;
        this.records = 0;
        this.bytes = 0;
        this.frameArrivals = [];

        this.lastWorldTick = null;
        this.tracked = new Map();       // playerId -> { x, y, vx, vy }
        this.deadReckoned = 0;
        this.serverRecords = 0;
        this.decodeFailures = [];
    }

    static async connect(protocol, url = DEFAULT_URL) {
        const client = new GameClient(protocol);
        const ws = new WebSocket(url);
        ws.binaryType = 'arraybuffer';
        client.ws = ws;
        ws.onmessage = (e) => client.#onMessage(e.data);
        await new Promise((resolve, reject) => {
            ws.onopen = resolve;
            ws.onerror = () => reject(new Error(`cannot reach ${url}`));
        });
        return client;
    }

    #onMessage(buffer) {
        const bytes = new Uint8Array(buffer);
        let msg;
        try {
            msg = this.protocol.decodeMessage(bytes);
        } catch (err) {
            this.decodeFailures.push({ code: bytes[0], length: bytes.length, error: String(err) });
            return;
        }
        if (!msg) {
            this.decodeFailures.push({ code: bytes[0], length: bytes.length, error: 'decoder returned null' });
            return;
        }

        if (msg.type === 'welcome') {
            this.id = msg.playerId;
            this.protocolVersion = msg.protocolVersion;
            return;
        }
        if (msg.type === 'movementAck') {
            this.acks.set(msg.inputSequence, { ...msg.position });
            this.lastAckSequence = msg.inputSequence;
            return;
        }
        if (msg.type !== 'gameState' && msg.type !== 'deltaGameState') return;

        this.frames++;
        this.bytes += bytes.length;
        this.frameArrivals.push(performance.now());
        this.stateSequences.push(msg.stateSequence);
        this.records += Object.keys(msg.players).length;

        const elapsed = this.lastWorldTick === null
            ? 0
            : (msg.worldTick - this.lastWorldTick) >>> 0;
        this.lastWorldTick = msg.worldTick;

        // Mirror of playerManager: advance everyone the frame omitted, then apply the
        // records. Under velocity replication the server withholds a player precisely
        // when this integration reproduces what it would have sent.
        if (msg.type === 'deltaGameState' && elapsed > 0) {
            for (const [id, p] of this.tracked) {
                if (msg.players[id]) continue;
                p.x += p.vx * SPEED * elapsed;
                p.y += p.vy * SPEED * elapsed;
                this.deadReckoned++;
            }
        }
        for (const [id, record] of Object.entries(msg.players)) {
            if (this.tracked.has(id)) this.serverRecords++;
            this.tracked.set(id, {
                x: record.position.x,
                y: record.position.y,
                vx: record.vx,
                vy: record.vy,
            });
        }
    }

    /** Advances one local simulation tick and sends only an input-state transition. */
    move(dx, dy) {
        if (dx === this.vector.dx && dy === this.vector.dy) return this.sequence;
        this.vector = { dx, dy };
        this.ws.send(this.protocol.encodeMove({
            type: 'move',
            movementVector: { dx, dy },
            inputSequence: ++this.sequence,
        }));
        return this.sequence;
    }

    belief(playerId) {
        return this.tracked.get(playerId) ?? null;
    }

    /** Frame sequence numbers must advance by exactly one; anything else is a gap. */
    sequenceGaps() {
        const gaps = [];
        for (let i = 1; i < this.stateSequences.length; i++) {
            const distance = (this.stateSequences[i] - this.stateSequences[i - 1]) >>> 0;
            if (distance !== 1) gaps.push([this.stateSequences[i - 1], this.stateSequences[i]]);
        }
        return gaps;
    }

    arrivalIntervals() {
        const gaps = [];
        for (let i = 1; i < this.frameArrivals.length; i++) {
            gaps.push(this.frameArrivals[i] - this.frameArrivals[i - 1]);
        }
        return gaps;
    }

    close() {
        this.ws.close();
    }
}

export async function connectAll(count, protocol, url) {
    const clients = [];
    for (let i = 0; i < count; i++) clients.push(await GameClient.connect(protocol, url));
    return clients;
}

/** Reads a histogram or counter out of the server's Prometheus endpoint. */
export async function readMetrics(names, url = METRICS_URL) {
    const text = await (await fetch(url)).text();
    const read = (name, suffix) => {
        const m = text.match(new RegExp(`^${name}${suffix} (\\S+)`, 'm'));
        return m ? Number(m[1]) : 0;
    };
    const out = {};
    for (const name of names) {
        out[name] = { sum: read(name, '_sum'), count: read(name, '_count'), value: read(name, '') };
    }
    return out;
}

export const stats = {
    mean: (a) => (a.length ? a.reduce((s, v) => s + v, 0) / a.length : 0),
    stdev(a) {
        if (a.length < 2) return 0;
        const m = this.mean(a);
        return Math.sqrt(this.mean(a.map((v) => (v - m) ** 2)));
    },
    percentile(a, p) {
        if (!a.length) return null;
        const sorted = [...a].sort((x, y) => x - y);
        return sorted[Math.min(sorted.length - 1, Math.floor(p * sorted.length))];
    },
    histogram: (a) => a.reduce((h, v) => ((h[v] = (h[v] ?? 0) + 1), h), {}),
};

/** Probe result reporting. Any failure makes the process exit non-zero for run.sh. */
export function report(name, checks, detail = {}) {
    const failed = checks.filter((c) => !c.pass);
    console.log(`\n${failed.length ? '✗' : '✓'} ${name}`);
    for (const c of checks) {
        console.log(`   ${c.pass ? '✓' : '✗'} ${c.label}: ${c.actual}`);
    }
    if (Object.keys(detail).length) {
        console.log(`   · ${JSON.stringify(detail)}`);
    }
    process.exit(failed.length ? 1 : 0);
}
