# Protocol probes

End-to-end checks that run a real server and speak the real binary protocol to it.
They complement the Go unit tests rather than duplicating them: the unit tests pin
pure functions, these pin the behaviour that only appears once both sides are talking.

```bash
utils/testing/protocol/run.sh                  # every probe
utils/testing/protocol/run.sh pacing           # one probe
utils/testing/protocol/ab-velocity.sh 12 1.5   # velocity replication A/B
```

`run.sh` builds the server, starts it in its own temp directory (a fresh `mktemp -d` per
run, so parallel runs never share a binary/log/decoder), runs the probes and fails if
any probe fails or the server logs an error. No manual setup.

By default it also spins up scratch Postgres/Redis containers scoped to the run — set
`GAME_TEST_ISOLATED_DB=off` to explicitly reuse whatever `POSTGRES_HOST`/`REDIS_HOST`
are already configured to instead. Without Docker and without that explicit opt-in,
`run.sh` fails rather than silently falling back to a real dev/CI database.

## Why they bundle the client decoder and movement formula

Every probe decodes frames with `src/client/network/protocol/binaryProtocol.ts`, and
predicts movement with `src/client/utils/movement.ts`'s `milliRatePerTick`/
`integrateRemainder` — the same fixed-point integrator the server carries
`moveRemainderMilli` for — both bundled by `run.sh` into the run's temp directory
(`$GAME_PROTOCOL_DIR`). Hand-written copies would drift. That matters more than usual
here: a world-state frame carries no per-record framing, so a decoder that disagrees
with the server does not fail — it silently produces wrong player IDs. And a hand-rolled
speed formula can hide, or fake, the exact client-side drift these probes exist to
catch. Bundling the shipped code is what makes a wire-format or prediction change
verifiable at all.

## The probes

| Probe | Pins |
|---|---|
| `determinism` | A held vector uses one START and one STOP, timestamped by the server's own worldTick (not wall-clock sleep counts); every watcher's reconstructed travel matches the mover's own authoritative ACK exactly. |
| `pacing` | Replication lands on a steady cadence. Jitter is paid for twice — fewer updates, and a larger interpolation delay to hide them. |
| `dead-reckoning` | A watcher reconstructing a mover from velocity and the wire `moveRemainderMilli` converges exactly on that mover's authoritative `MOVEMENT_ACK`, both after STOP and, on every authoritative record received while still moving, against the belief carried since the previous one — zero drift, not just bounded drift, since the client now replays the server's exact integrator. |
| `ack-flow` | Several input transitions inside one replication interval coalesce to the latest authoritative ACK. |
| `resilience` | Duplicate transitions are idempotent; a rate-limit burst closes the stream instead of silently losing a command. |
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
