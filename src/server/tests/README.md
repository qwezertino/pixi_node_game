# External Go tests

Black-box tests that exercise only the exported API of an `internal/` package, living
as `package <pkg>_test` importing `pixi_game_server/internal/<pkg>` from outside it.

Only one package currently qualifies: `internal/types`'s movement-input queue is fully
testable through `Player.EnqueueMovementInput`/`DequeueMovementInput` and the exported
`InputResult` values, with no need to reach into unexported fields.

## Why most tests are NOT here

Go compiles a package per directory, and a `_test.go` file only gets access to
unexported identifiers when it lives in the *same* directory as the code it tests —
there is no way to relocate a white-box test and keep that access. Most of this
project's Go tests are intentionally white-box: they pin small unexported helpers
directly (`classifyDelta`, `replicationIntervalNs`, `appendUvarint`,
`validClientHeader`, `selectRecipients`, ...) rather than driving them indirectly
through a public entry point. That is why they stay next to the source in
`internal/*/*_test.go` — moving them would require exporting implementation details
whose whole point is to stay private, or rewriting them as slower, less precise
integration tests. Run them the normal way:

```bash
go test ./...
```

`utils/testing/protocol/` holds the project's actual black-box suite: end-to-end
probes that run a live server and speak the real wire protocol to it.
