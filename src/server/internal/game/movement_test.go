package game

import (
	"testing"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/systems"
	"pixi_game_server/internal/types"
)

func TestDelayedMoveAndStopAreAppliedOnceInOrder(t *testing.T) {
	gw := &GameWorld{
		cfg: &config.Config{
			Game:  config.GameConfig{PlayerSpeedPerTick: 4},
			World: config.WorldConfig{Width: 1000, Height: 1000, MaxX: 1000, MaxY: 1000},
		},
		visibilityManager: systems.NewVisibilityManager(1000, 1000, 100),
	}
	player := &types.Player{ID: 1, X: 100, Y: 100}
	gw.visibilityManager.AddPlayer(player.ID, 100, 100)

	for _, input := range []types.MovementInput{
		{Sequence: 1, DX: 1},
		{Sequence: 2, DX: 1},
		{Sequence: 3}, // delayed STOP
	} {
		if got := player.EnqueueMovementInput(input); got != types.InputAccepted {
			t.Fatalf("failed to enqueue %+v: %d", input, got)
		}
	}

	gw.updatePlayerPosition(player, 1)
	if player.GetX() != 104 || player.GetAppliedClientTick() != 1 {
		t.Fatalf("after first tick x=%d seq=%d", player.GetX(), player.GetAppliedClientTick())
	}
	gw.updatePlayerPosition(player, 2)
	if player.GetX() != 108 || player.GetAppliedClientTick() != 2 {
		t.Fatalf("after second tick x=%d seq=%d", player.GetX(), player.GetAppliedClientTick())
	}
	gw.updatePlayerPosition(player, 3)
	if player.GetX() != 108 || player.GetVX() != 0 || player.GetAppliedClientTick() != 3 {
		t.Fatalf("after stop x=%d vx=%d seq=%d", player.GetX(), player.GetVX(), player.GetAppliedClientTick())
	}
	gw.updatePlayerPosition(player, 4)
	if player.GetX() != 108 {
		t.Fatalf("server drifted without another input: x=%d", player.GetX())
	}
}

// A player held against a world boundary keeps consuming inputs while every
// replicated field stays constant, so it contributes nothing to the delta. This is
// why broadcastTick emits movement ACKs independently of the delta payload: if the
// ACK rode on the delta, this client would never prune its pending-input ring.
func TestClampedPlayerAdvancesInputSequenceWithoutChangingState(t *testing.T) {
	gw := &GameWorld{
		cfg: &config.Config{
			Game:  config.GameConfig{PlayerSpeedPerTick: 4},
			World: config.WorldConfig{Width: 1000, Height: 1000, MaxX: 1000, MaxY: 1000},
		},
		visibilityManager: systems.NewVisibilityManager(1000, 1000, 100),
	}
	player := &types.Player{ID: 1, X: 1000, Y: 100}
	gw.visibilityManager.AddPlayer(player.ID, 1000, 100)

	for sequence := uint32(1); sequence <= 3; sequence++ {
		if got := player.EnqueueMovementInput(types.MovementInput{Sequence: sequence, DX: 1}); got != types.InputAccepted {
			t.Fatalf("enqueue %d = %d, want InputAccepted", sequence, got)
		}
	}

	// First tick establishes VX=1, which does change the replicated record.
	gw.updatePlayerPosition(player, 1)
	baseline := player.ToState()

	for sequence := uint32(2); sequence <= 3; sequence++ {
		gw.updatePlayerPosition(player, int64(sequence))
		st := player.ToState()

		if st.X != baseline.X || st.Y != baseline.Y || st.VX != baseline.VX ||
			st.VY != baseline.VY || st.State != baseline.State || st.FacingRight != baseline.FacingRight {
			t.Fatalf("clamped player changed a replicated field at sequence %d: %+v vs %+v", sequence, st, baseline)
		}
		if player.GetAppliedClientTick() != sequence {
			t.Fatalf("applied sequence = %d, want %d", player.GetAppliedClientTick(), sequence)
		}
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
	prev := types.PlayerState{ID: 1, X: 100, Y: 100, VX: 1, VY: 0, FacingRight: true}
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
			st:   moved(108, 100, func(s *types.PlayerState) { s.FacingRight = false }), exists: true,
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
			st:     types.PlayerState{ID: 1, X: 100, Y: 100, FacingRight: true, VX: 0},
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
		{ID: 1, X: 10, Y: 10, VX: 0, FacingRight: true},
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
