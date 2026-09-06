package game

import (
	"testing"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/protocol"
	"pixi_game_server/internal/systems"
	"pixi_game_server/internal/types"
)

func TestUpdatePlayerPositionAppliesInputAndAcks(t *testing.T) {
	gw := &GameWorld{
		cfg: &config.Config{
			World: config.WorldConfig{Width: 1000, Height: 1000, MaxX: 1000, MaxY: 1000},
		},
		// Unit type 0 (types.Player's zero value) at 4 whole units/tick, no remainder
		// — matches the flat test speed these tests were written against before
		// movement became per-unit (GDD §60 World Coordinate Resolution).
		moveStats:         map[uint8]moveStat{0: {milliUnitsPerTick: 4000}},
		visibilityManager: systems.NewVisibilityManager(1000, 1000, 100),
	}
	player := &types.Player{ID: 1, X: 100, Y: 100}
	gw.visibilityManager.AddPlayer(player.ID, 100, 100)

	if got := player.OfferMovementInput(types.MovementInput{Sequence: 1, DX: 1}); got != types.InputAccepted {
		t.Fatalf("offer start = %s", got)
	}
	gw.updatePlayerPosition(player, 1)
	if player.GetX() != 104 || player.GetVX() != 1 {
		t.Fatalf("after start x=%d vx=%d", player.GetX(), player.GetVX())
	}
	ackX, ackY := player.GetMovementAckPosition()
	if ackX != 104 || ackY != 100 || player.GetAppliedInputSequence() != 1 {
		t.Fatalf("ACK after start x=%d y=%d seq=%d", ackX, ackY, player.GetAppliedInputSequence())
	}
}

// Once a sample sets a non-zero vector, the server keeps integrating it on every
// subsequent tick without requiring a fresh sample — a client resending the same
// vector every fixed tick is a fail-safe choice, not a correctness requirement.
func TestUpdatePlayerPositionKeepsMovingWithoutNewInput(t *testing.T) {
	gw := &GameWorld{
		cfg: &config.Config{
			World: config.WorldConfig{Width: 1000, Height: 1000, MaxX: 1000, MaxY: 1000},
		},
		// Unit type 0 (types.Player's zero value) at 4 whole units/tick, no remainder
		// — matches the flat test speed these tests were written against before
		// movement became per-unit (GDD §60 World Coordinate Resolution).
		moveStats:         map[uint8]moveStat{0: {milliUnitsPerTick: 4000}},
		visibilityManager: systems.NewVisibilityManager(1000, 1000, 100),
	}
	player := &types.Player{ID: 1, X: 100, Y: 100}
	gw.visibilityManager.AddPlayer(player.ID, 100, 100)

	if got := player.OfferMovementInput(types.MovementInput{Sequence: 1, DX: 1}); got != types.InputAccepted {
		t.Fatalf("offer start = %s", got)
	}
	gw.updatePlayerPosition(player, 1)
	gw.updatePlayerPosition(player, 2)
	gw.updatePlayerPosition(player, 3)
	if player.GetX() != 112 {
		t.Fatalf("persisted velocity did not keep integrating: x=%d", player.GetX())
	}
	// No new sample was consumed on ticks 2/3, so the ACK boundary stays at tick 1.
	ackX, _ := player.GetMovementAckPosition()
	if ackX != 104 || player.GetAppliedInputSequence() != 1 {
		t.Fatalf("ACK boundary moved without a new sample: x=%d seq=%d", ackX, player.GetAppliedInputSequence())
	}
}

func TestUpdatePlayerPositionStopsAndHoldsPosition(t *testing.T) {
	gw := &GameWorld{
		cfg: &config.Config{
			World: config.WorldConfig{Width: 1000, Height: 1000, MaxX: 1000, MaxY: 1000},
		},
		// Unit type 0 (types.Player's zero value) at 4 whole units/tick, no remainder
		// — matches the flat test speed these tests were written against before
		// movement became per-unit (GDD §60 World Coordinate Resolution).
		moveStats:         map[uint8]moveStat{0: {milliUnitsPerTick: 4000}},
		visibilityManager: systems.NewVisibilityManager(1000, 1000, 100),
	}
	player := &types.Player{ID: 1, X: 100, Y: 100}
	gw.visibilityManager.AddPlayer(player.ID, 100, 100)

	if got := player.OfferMovementInput(types.MovementInput{Sequence: 1, DX: 1}); got != types.InputAccepted {
		t.Fatalf("offer start = %s", got)
	}
	gw.updatePlayerPosition(player, 1)
	gw.updatePlayerPosition(player, 2)

	if got := player.OfferMovementInput(types.MovementInput{Sequence: 2}); got != types.InputAccepted {
		t.Fatalf("offer stop = %s", got)
	}
	gw.updatePlayerPosition(player, 3)
	stopped := player.GetX()
	if player.GetVX() != 0 {
		t.Fatalf("velocity did not clear on stop: vx=%d", player.GetVX())
	}
	gw.updatePlayerPosition(player, 4)
	if player.GetX() != stopped {
		t.Fatalf("player drifted after stop: before=%d after=%d", stopped, player.GetX())
	}
	ackX, _ := player.GetMovementAckPosition()
	if ackX != stopped || player.GetAppliedInputSequence() != 2 {
		t.Fatalf("ACK after stop x=%d seq=%d", ackX, player.GetAppliedInputSequence())
	}
}

// A held vector continues to integrate at a boundary; its replicated state stays
// constant (clamped) and contributes nothing to the delta, but the ACK still tracks
// the latest applied sequence.
func TestUpdatePlayerPositionClampsAtWorldBoundary(t *testing.T) {
	gw := &GameWorld{
		cfg: &config.Config{
			World: config.WorldConfig{Width: 1000, Height: 1000, MaxX: 1000, MaxY: 1000},
		},
		// Unit type 0 (types.Player's zero value) at 4 whole units/tick, no remainder
		// — matches the flat test speed these tests were written against before
		// movement became per-unit (GDD §60 World Coordinate Resolution).
		moveStats:         map[uint8]moveStat{0: {milliUnitsPerTick: 4000}},
		visibilityManager: systems.NewVisibilityManager(1000, 1000, 100),
	}
	player := &types.Player{ID: 1, X: 1000, Y: 100}
	gw.visibilityManager.AddPlayer(player.ID, 1000, 100)

	if got := player.OfferMovementInput(types.MovementInput{Sequence: 1, DX: 1}); got != types.InputAccepted {
		t.Fatalf("offer start = %s", got)
	}
	gw.updatePlayerPosition(player, 1)
	baseline := player.ToState()

	for tick := int64(2); tick <= 3; tick++ {
		gw.updatePlayerPosition(player, tick)
		st := player.ToState()
		if st.X != baseline.X || st.Y != baseline.Y || st.VX != baseline.VX || st.VY != baseline.VY {
			t.Fatalf("clamped player changed a replicated field at tick %d: %+v vs %+v", tick, st, baseline)
		}
	}
}

func TestOfferMovementInputKeepsOnlyLatestSampleBeforeConsume(t *testing.T) {
	player := &types.Player{ID: 1}

	if got := player.OfferMovementInput(types.MovementInput{Sequence: 1, DX: 1}); got != types.InputAccepted {
		t.Fatalf("offer 1 = %s", got)
	}
	if got := player.OfferMovementInput(types.MovementInput{Sequence: 2, DX: -1}); got != types.InputAccepted {
		t.Fatalf("offer 2 = %s", got)
	}

	input, ok := player.ConsumeLatestMovementInput()
	if !ok || input.Sequence != 2 || input.DX != -1 {
		t.Fatalf("consumed stale sample instead of latest: %+v ok=%v", input, ok)
	}
	if _, ok := player.ConsumeLatestMovementInput(); ok {
		t.Fatal("second consume should find nothing pending")
	}
}

func TestOfferMovementInputRejectsStaleAndGap(t *testing.T) {
	player := &types.Player{ID: 1}

	if got := player.OfferMovementInput(types.MovementInput{Sequence: 5}); got != types.InputAccepted {
		t.Fatalf("offer 5 = %s", got)
	}
	if got := player.OfferMovementInput(types.MovementInput{Sequence: 5}); got != types.InputStale {
		t.Fatalf("duplicate sequence = %s, want InputStale", got)
	}
	if got := player.OfferMovementInput(types.MovementInput{Sequence: 4}); got != types.InputStale {
		t.Fatalf("older sequence = %s, want InputStale", got)
	}
	if got := player.OfferMovementInput(types.MovementInput{Sequence: 8}); got != types.InputGap {
		t.Fatalf("skipped sequence = %s, want InputGap", got)
	}
}

// Attack cooldown is measured in ticks, not wall-clock time, so it dilates along
// with movement under time dilation instead of ticking at a fixed real-world rate.
func TestTryAttackCooldownIsTickBasedNotWallClock(t *testing.T) {
	gw := &GameWorld{attackDurationTicks: map[uint8]uint32{0: 20}}
	player := &types.Player{ID: 1}
	gw.playersMap = map[uint32]*types.Player{1: player}

	gw.tickCount = 100
	if _, _, accepted := gw.TryAttack(1); !accepted {
		t.Fatal("first attack should be accepted")
	}
	if start := player.GetAttackStartTick(); start != 100 {
		t.Fatalf("attack start tick = %d, want 100", start)
	}

	// Still inside the cooldown window in ticks, no matter how much real time an
	// external caller might imagine has passed — this function only looks at
	// gw.tickCount, which nothing here has advanced.
	gw.tickCount = 119
	if _, _, accepted := gw.TryAttack(1); accepted {
		t.Fatal("attack inside the tick-based cooldown must be rejected")
	}

	// Exactly attackDurationTicks later, cooldown has elapsed.
	gw.tickCount = 120
	if _, _, accepted := gw.TryAttack(1); !accepted {
		t.Fatal("attack should be accepted once attackDurationTicks have elapsed")
	}
	if start := player.GetAttackStartTick(); start != 120 {
		t.Fatalf("second attack start tick = %d, want 120", start)
	}
}

const (
	testSpeed    = int32(4)
	testElapsed  = int32(2) // two simulation ticks per broadcast at the shipped defaults
	velocityRepl = true
	legacyRepl   = false
)

func TestClassifyDelta(t *testing.T) {
	// Baseline: moving right at 4 px/tick. After 2 ticks a client predicts X+8.
	prev := types.PlayerState{ID: 1, X: 100, Y: 100, VX: 1, VY: 0, Direction: protocol.DirectionRight}
	moved := func(x, y uint16, mut ...func(*types.PlayerState)) types.PlayerState {
		st := prev
		st.X, st.Y = x, y
		for _, m := range mut {
			m(&st)
		}
		return st
	}

	cases := []struct {
		name   string
		st     types.PlayerState
		exists bool
		want   deltaReason
	}{
		{
			name: "new player is always sent",
			st:   prev, exists: false,
			want: deltaReason{include: true, unpredictable: true},
		},
		{
			name: "steady movement matches prediction exactly",
			st:   moved(108, 100), exists: true,
			want: deltaReason{positionOnly: true},
		},
		{
			name: "velocity change cannot be predicted",
			st:   moved(108, 100, func(s *types.PlayerState) { s.VX = 0 }), exists: true,
			want: deltaReason{include: true, unpredictable: true},
		},
		{
			name: "facing change cannot be predicted",
			st:   moved(108, 100, func(s *types.PlayerState) { s.Direction = protocol.DirectionLeft }), exists: true,
			want: deltaReason{include: true, unpredictable: true},
		},
		{
			name: "fully pinned at a boundary diverges",
			st:   moved(100, 100), exists: true,
			want: deltaReason{include: true, diverged: true},
		},
		{
			// The case a "did the position change at all" test misses: X is clamped
			// while Y keeps moving, so the record looks like ordinary movement.
			name:   "diagonal corner clamps one axis only",
			st:     moved(100, 108, func(s *types.PlayerState) { s.VY = 1 }),
			exists: true,
			want:   deltaReason{include: true, unpredictable: true, diverged: true},
		},
		{
			name:   "idle player contributes nothing",
			st:     types.PlayerState{ID: 1, X: 100, Y: 100, Direction: protocol.DirectionRight, VX: 0},
			exists: true,
			want:   deltaReason{include: true, unpredictable: true, diverged: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyDelta(tc.st, prev, tc.exists, testElapsed, testSpeed, velocityRepl)
			if got != tc.want {
				t.Fatalf("classifyDelta = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A diagonal clamp is the regression that motivated predicting per axis instead of
// asking whether the position changed at all.
func TestClassifyDeltaCatchesSingleAxisClamp(t *testing.T) {
	// Predicted X is 5996 + 8 = 6004, past the 6000 world edge, so the server clamps
	// to 6000 while Y advances the full 8 px. Velocity is unchanged on both axes, so
	// only a per-axis position check can catch this.
	prev := types.PlayerState{ID: 1, X: 5996, Y: 100, VX: 1, VY: 1}
	st := types.PlayerState{ID: 1, X: 6000, Y: 108, VX: 1, VY: 1}

	got := classifyDelta(st, prev, true, testElapsed, testSpeed, velocityRepl)
	if !got.diverged || !got.include {
		t.Fatalf("single-axis clamp must be sent, got %+v", got)
	}
	if got.positionOnly {
		t.Fatal("a clamped record must never be counted as predictable")
	}
}

// Legacy mode must keep shipping every positional change, so the flag is a true
// kill switch rather than a partial rollback.
func TestClassifyDeltaLegacyModeShipsPredictableMovement(t *testing.T) {
	prev := types.PlayerState{ID: 1, X: 100, Y: 100, VX: 1}
	st := types.PlayerState{ID: 1, X: 108, Y: 100, VX: 1}

	legacy := classifyDelta(st, prev, true, testElapsed, testSpeed, legacyRepl)
	if !legacy.include {
		t.Fatal("legacy mode dropped a positional change")
	}
	velocity := classifyDelta(st, prev, true, testElapsed, testSpeed, velocityRepl)
	if velocity.include {
		t.Fatal("velocity mode shipped a record the client can dead-reckon")
	}
	if !legacy.positionOnly || !velocity.positionOnly {
		t.Fatal("the composition metric must not depend on the mode")
	}
}

func TestClassifyDeltaBucketsAreExclusive(t *testing.T) {
	prev := types.PlayerState{ID: 1, X: 10, Y: 10, VX: 1}
	for _, st := range []types.PlayerState{
		{ID: 1, X: 10, Y: 10, VX: 1},
		{ID: 1, X: 18, Y: 10, VX: 1},
		{ID: 1, X: 14, Y: 10, VX: 1},
		{ID: 1, X: 18, Y: 10, VX: 0},
		{ID: 1, X: 10, Y: 10, VX: 0, Direction: protocol.DirectionRight},
	} {
		for _, exists := range []bool{true, false} {
			for _, mode := range []bool{velocityRepl, legacyRepl} {
				r := classifyDelta(st, prev, exists, testElapsed, testSpeed, mode)
				if r.unpredictable && r.positionOnly {
					t.Fatalf("st=%+v counted in both composition buckets", st)
				}
				if r.positionOnly && r.diverged {
					t.Fatalf("st=%+v cannot be both predictable and diverged", st)
				}
				if mode == velocityRepl && r.include != (r.unpredictable || r.diverged) {
					t.Fatalf("st=%+v velocity-mode include disagrees with reasons: %+v", st, r)
				}
			}
		}
	}
}
