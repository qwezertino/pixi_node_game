const { test } = require('node:test');
const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const { attachLatencyProbe } = require('./latency-probe.cjs');

test('RTT matches nonce; ACK matches sequence; timers and listeners clean up', () => {
  const ws = new EventEmitter(); ws.readyState = 1;
  const sent = []; ws.send = packet => sent.push(packet);
  const values = []; const events = { emit: (...args) => values.push(args) };
  let time = 100;
  const probe = attachLatencyProbe(ws, events, { now: () => time, interval: 0 });
  probe.sendPing(); time = 125;
  const pong = Buffer.from(sent[0]); pong[0] = 18;
  ws.emit('message', pong); ws.emit('message', pong);
  assert.deepEqual(values.filter(v => v[1] === 'game.ping.rtt_ms'), [['histogram', 'game.ping.rtt_ms', 25]]);
  probe.trackMove(1); probe.trackMove(2); time = 175;
  const ack = Buffer.alloc(13); ack[0] = 8; ack.writeUInt32LE(2, 9);
  ws.emit('message', ack);
  assert.deepEqual(values.filter(v => v[1] === 'game.movement.ack_ms'), [['histogram', 'game.movement.ack_ms', 50]]);
  assert.equal(values.filter(v => v[1] === 'game.ack.coalesced').length, 1);
  ws.emit('close'); assert.equal(ws.listenerCount('message'), 0);
});

test('sequence gaps request bounded resync and tolerate sequence wrap', () => {
  const ws = new EventEmitter(); ws.readyState = 1;
  const sent = []; ws.send = packet => sent.push(packet);
  let time = 100;
  const probe = attachLatencyProbe(ws, { emit() {} }, { now: () => time, interval: 0 });
  function frame(seq, type = 14) {
    const b = Buffer.alloc(15); b[0] = type; b.writeUInt32LE(seq, 1); ws.emit('message', b);
  }
  frame(0xffffffff, 7); frame(0); assert.equal(sent.length, 0);
  frame(2); frame(4); assert.equal(sent.length, 1);
  time += 1001; frame(6); assert.equal(sent.length, 2);
  probe.stop();
});
