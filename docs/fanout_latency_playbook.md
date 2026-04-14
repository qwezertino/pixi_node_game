# Fanout Tail Latency Playbook

This note captures practical guidance and references for reducing fanout tail latency in the Go WebSocket server.

## Current Symptoms

- `game_tick_fanout_select_seconds` is low (selection is not the main bottleneck).
- `game_tick_fanout_enqueue_seconds` and `game_tick_fanout_send_seconds` dominate p99.
- CPU profiles point to socket write path (`writev`/syscall) under broadcast load.
- Queue-aware shedding can be correct but inactive if per-connection queues are not filling.

## Root Cause Pattern

At current load, the tail is mainly transport write pressure (many concurrent socket writes), not recipient sorting.

## Implemented Mitigations

- Latest-state semantics for world-state (`pendingBroadcast`) to avoid piling stale snapshots.
- Queue-aware enqueue shedding (`FANOUT_QUEUE_SHED_DEPTH`).
- Fanout byte budget support (`FANOUT_MAX_BROADCAST_BYTES_PER_TICK`) to cap per-tick fanout work.
- Additional metrics for budget pressure and shedding.

## Recommended Tuning Flow

1. Keep one stable baseline config and run 3 identical load tests.
2. Compare p95/p99 for:
   - `game_tick_duration_seconds`
   - `game_tick_fanout_send_seconds`
   - `game_tick_fanout_enqueue_seconds`
3. Validate mechanism activation:
   - queue shedding: `game_broadcasts_shed_total`, `game_ws_write_queue_depth`
   - budget trimming: `game_broadcast_budget_hits_total`, `game_broadcast_budget_trimmed_total`, `game_broadcast_budget_recipients`
4. Change one knob at a time.

## Best-Practice References

### WebSocket broadcast backpressure

- https://websockets.readthedocs.io/en/stable/topics/broadcast.html
- Key point: naive serialized broadcast can stall all clients on slow consumers; per-client buffering/shedding/disconnect policies are needed.

### Linux write path and TCP buffers

- https://man7.org/linux/man-pages/man2/writev.2.html
- https://man7.org/linux/man-pages/man7/tcp.7.html
- Key point: write/send costs and TCP send-buffer behavior define real throughput/latency limits under fanout.

### Realtime game state sync and prioritization

- https://gafferongames.com/post/state_synchronization/
- https://gafferongames.com/post/snapshot_interpolation/
- Key point: prioritize important updates, send latest state, and enforce bandwidth budgets.

### Queueing and bottleneck intuition

- https://queue.acm.org/detail.cfm?id=3022184
- Key point: persistent queues at bottlenecks drive delay; controlling in-flight and pacing is central.

## Practical Next Iterations

- Enable byte budget with conservative value and run A/B against disabled budget.
- Add priority tiers (near/active first) when budget trims recipients.
- Consider per-connection health signals (timeouts, repeated write failures) for stronger adaptive shedding.
- Keep direct critical messages outside budget/shedding path.

## Latest Experiment Notes (2026-04-14)

### Queue shedding A/B

- `FANOUT_QUEUE_SHED_DEPTH=0` vs `2` showed very small p99 difference.
- `game_broadcasts_shed_total` stayed near zero in both runs.
- Interpretation: current profile does not build enough per-connection queue depth for this mechanism to activate.

### Byte budget test

- With `FANOUT_MAX_BROADCAST_BYTES_PER_TICK=3000000`:
   - `tick_p99_5m` dropped to about `1.82ms`.
   - `fanout_send_p99_5m` dropped to about `1.95ms`.
   - `fanout_enqueue_p99_5m` dropped to about `1.88ms`.
   - `game_broadcast_budget_hits_total` and `game_broadcast_budget_trimmed_total` became active.
   - `recipients_avg_5m` decreased significantly (around `369`), and deferred rate increased.
- Interpretation: budget capping is effective for tail latency, with a clear quality/freshness tradeoff that must be tuned by gameplay acceptance criteria.
