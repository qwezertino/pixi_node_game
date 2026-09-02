# Protocol probes

End-to-end checks that run a real server and speak the real binary protocol to it.
They complement the Go unit tests rather than duplicating them: the unit tests pin
pure functions, these pin the behaviour that only appears once both sides are talking.

```bash
utils/testing/protocol/run.sh                  # every probe
utils/testing/protocol/run.sh pacing           # one probe
utils/testing/protocol/ab-velocity.sh 12 1.5   # velocity replication A/B
```

`run.sh` builds the server, starts it on a scratch directory, runs the probes and fails
if any probe fails or the server logs an error. No manual setup.

## Why they bundle the client decoder

Every probe decodes frames with `src/client/network/protocol/binaryProtocol.ts`,
bundled by `run.sh` into `lib/proto.mjs` (git-ignored). A hand-written copy would drift.
That matters more than usual here: a world-state frame carries no per-record framing, so
a decoder that disagrees with the server does not fail — it silently produces wrong
player IDs. Bundling the shipped decoder is what makes a wire-format change verifiable.

## The probes

| Probe | Pins |
|---|---|
| `determinism` | One input produces exactly one simulation step. Travel must equal `steps × playerSpeedPerTick` for every client's view of every other client. |
| `pacing` | Replication lands on a steady cadence. Jitter is paid for twice — fewer updates, and a larger interpolation delay to hide them. |
| `dead-reckoning` | A watcher reconstructing a mover from velocity alone converges exactly on that mover's authoritative `MOVEMENT_ACK`. |
| `ack-flow` | Acknowledgements keep flowing when the delta is empty. Without this a player pinned against a boundary never prunes its pending-input ring and is eventually disconnected. |
| `resilience` | Duplicate sequences and a send burst cost messages, not the session. |
| `bandwidth` | Not pass/fail — reports records and bytes on the wire plus the delta composition. Used by `ab-velocity.sh`. |

## Tuning

Probes read `src/shared/gameConfig.json`, so changing the tick rate or speed does not
silently invalidate an assertion. Per-probe knobs come from the environment:

```bash
CLIENTS=16 STEPS=120 node utils/testing/protocol/probes/determinism.mjs
TURNS_PER_SEC=3 node utils/testing/protocol/probes/bandwidth.mjs
```

`GAME_WS_URL` and `GAME_METRICS_URL` point the probes at an already-running server.

## What they do not cover

Nothing here looks at the screen. The probes prove the stream reaching the client is
correct and evenly paced; they cannot tell you whether the rendered motion looks smooth.
Direction changes under velocity replication produce a correction of roughly 8–23 px
that the interpolation delay is supposed to hide — that one needs two browser tabs.

Browser-side cost (decode on the main thread, per-entity sprites) is likewise out of
scope, and is the other open item in `docs/pixi_node_game_rag.md`.
