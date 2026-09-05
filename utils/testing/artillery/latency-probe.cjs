const { performance } = require('node:perf_hooks');

// Shared by Artillery and the controlled A/B runner. All times are monotonic;
// PONG/ACK must match a request before a latency observation is emitted.
function attachLatencyProbe(ws, events, options = {}) {
  const now = options.now || (() => performance.now());
  const interval = options.interval ?? 1000;
  const resync = options.resync ?? true;
  const pings = new Map();
  const moves = new Map();
  let nonce = 0, lastFrame, lastSequence, lastResync = -Infinity;
  let timer, stopped = false;
  const counter = (name, value = 1) => events.emit('counter', `game.${name}`, value);
  const histogram = (name, value) => events.emit('histogram', `game.${name}`, value);

  function sendPing() {
    if (ws.readyState !== 1) return;
    const stamp = now();
    for (const [id, sent] of pings) {
      if (stamp - sent > 10000) { pings.delete(id); counter('ping.timeout'); }
    }
    for (const [id, sent] of moves) {
      if (stamp - sent > 10000) { moves.delete(id); counter('ack.unconfirmed'); }
    }
    const id = nonce = (nonce + 1) >>> 0;
    const packet = Buffer.allocUnsafe(5);
    packet[0] = 17; packet.writeUInt32LE(id, 1);
    pings.set(id, stamp);
    ws.send(packet);
  }

  function schedule(delay) {
    const expected = now() + delay;
    timer = setTimeout(() => {
      if (stopped) return;
      histogram('probe.timer_lag_ms', Math.max(0, now() - expected));
      sendPing();
      schedule(interval);
    }, delay);
    timer.unref?.();
  }

  function onMessage(data) {
    const bytes = Buffer.isBuffer(data) ? data : Buffer.from(data);
    const stamp = now();
    counter('received.bytes', bytes.length);
    if (bytes[0] === 18 && bytes.length === 5) {
      const id = bytes.readUInt32LE(1), sent = pings.get(id);
      if (sent !== undefined) { histogram('ping.rtt_ms', stamp - sent); pings.delete(id); }
    } else if (bytes[0] === 8 && bytes.length === 13) {
      const seq = bytes.readUInt32LE(9), sent = moves.get(seq);
      if (sent !== undefined) { histogram('movement.ack_ms', stamp - sent); moves.delete(seq); }
      // The server may acknowledge only the newest sample applied in a tick.
      for (const id of moves.keys()) {
        if (((seq - id) >>> 0) < 0x80000000) { moves.delete(id); counter('ack.coalesced'); }
      }
    } else if ((bytes[0] === 7 || bytes[0] === 14) && bytes.length >= 15) {
      const seq = bytes.readUInt32LE(1);
      if (lastFrame !== undefined) histogram('state.interval_ms', stamp - lastFrame);
      lastFrame = stamp;
      counter('state.frames');
      if (lastSequence !== undefined && bytes[0] === 14 && ((seq-lastSequence) >>> 0) > 1 && ((seq-lastSequence) >>> 0) < 0x80000000) {
        counter('state.sequence_gaps');
        if (resync && stamp-lastResync >= 1000 && ws.readyState === 1) {
          ws.send(Buffer.from([16])); lastResync = stamp; counter('state.resync_requests');
        }
      }
      if (lastSequence === undefined || ((seq-lastSequence) >>> 0) < 0x80000000) lastSequence = seq;
      histogram('state.dilation_percent', bytes.readUInt16LE(13)/100);
    }
  }

  function stop() {
    stopped = true; clearTimeout(timer);
    ws.off('message', onMessage); ws.off('close', stop);
    pings.clear(); moves.clear();
  }
  ws.on('message', onMessage);
  ws.once('close', stop);
  if (interval > 0) schedule(options.initialDelay ?? Math.random()*interval);
  return {
    sendPing, stop,
    trackMove(seq) {
      if (moves.size >= 256) { moves.delete(moves.keys().next().value); counter('ack.tracking_overflow'); }
      moves.set(seq, now());
    },
  };
}

module.exports = { attachLatencyProbe };
