// The probes need the same movement/network constants the client and server share. The
// server no longer ships a static gameConfig.json (settings live in Postgres now), so this
// reads the same /api/config endpoint the real client fetches at startup.
const WS_URL = process.env.GAME_WS_URL ?? 'ws://127.0.0.1:8108/ws';
export const HTTP_BASE = process.env.GAME_HTTP_URL ?? WS_URL.replace(/^ws/, 'http').replace(/\/ws$/, '');

const res = await fetch(`${HTTP_BASE}/api/config`);
if (!res.ok) {
    throw new Error(`GET ${HTTP_BASE}/api/config -> ${res.status}: is the game server running?`);
}
const config = await res.json();

export const { movement: MOVEMENT, network: NETWORK, world: WORLD } = config;
