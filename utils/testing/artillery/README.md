# Latency testing

`make load-test` keeps the local scenario and saves its report in `logs/`.
`make load-test-latency` uses the tracked `latency-config.yml`: 1200 arrivals at
10/s, 180-second sessions, at least about 60 seconds at 1200 connections, and
movement decisions every 500 ms. Repeated vectors are suppressed like the client.
The full run takes about 5 minutes. The older local scenario's four think steps
sum to 8 seconds; its MOVE frequency is not 2/s.

The shared processor now reports:

- `game.ping.rtt_ms`: application PING/PONG matched by nonce.
- `game.movement.ack_ms`: send to authoritative ACK matched by input sequence;
  includes waiting for a simulation tick. Coalesced ACKs have a separate counter.
- `game.state.interval_ms`: time between received state frames.
- `game.state.sequence_gaps`, `game.state.resync_requests`: missing deltas and
  throttled full-state recovery requests (`PROBE_RESYNC=0` disables requests).
- `game.probe.timer_lag_ms`: generator timer delay; high values can distort RTT.
- `game.state.dilation_percent`: simulation speed received on the wire.

The probe parses protocol-6 headers, not player records or browser rendering.
RTT and ACK use monotonic time. Unmatched messages cannot become RTT samples.
Listeners/timers are removed on socket close. Every bot pings roughly once per second.

For controlled local A/B, first build both server revisions into separate paths:

```sh
node utils/testing/artillery/compare-latency.mjs /tmp/server-before logs/ab/before
node utils/testing/artillery/compare-latency.mjs /tmp/server-after logs/ab/after
python3 utils/testing/artillery/summarize-latency.py logs/ab/before logs/ab/after
```

Each invocation starts and stops its own server on port 18108. Keep this port free.
Defaults: 1200 clients, 30-second ramp, 45-second measurement, MOVE every 8 seconds.
Use `MOVE_INTERVAL_MS=500` for active movement; `CLIENTS`, `RAMP_SECONDS`,
`HOLD_SECONDS`, `PORT`, and `GOMAXPROCS` are configurable.
The runner deliberately uses explicit common server settings instead of `.env`.
Run variants sequentially on an otherwise quiet host. It requires Node and `ws`
from the installed Artillery dependencies.

The runner saves before/after Prometheus counters, client statistics, and server
logs. After measurement it captures a 10-second CPU profile and a 1-second trace
at the same load (`PROFILE=0` skips profiling). Profiles are outside the measured
window. Server rates use the client's monotonic elapsed time to avoid WSL wall
clock corrections. Histogram quantiles are interpolated estimates; client values
are rounded up to 0.1 ms. Shared-host A/B does not establish production network RTT.

Run `node --test utils/testing/artillery/latency-probe.test.cjs` to check matching,
sequence wrap, bounded resync, and cleanup behavior.
