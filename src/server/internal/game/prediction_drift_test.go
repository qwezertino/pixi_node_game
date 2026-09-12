package game

import (
	"testing"
	"time"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/types"
	"pixi_game_server/internal/units"
)

// TestSuppressedDeltasStayExactlySyncedViaRemainder is the 2026-09-10
// follow-up regression: the old naive-rounding client model bounded drift to
// clientPredictionEpsilonUnits but could never eliminate it, causing visible
// jitter on any non-integer per-tick speed (e.g. moveSpeed=7 here gives
// 3.5 units/tick). The fix transmits MoveRemainderMilli on the wire so the
// client can replay the server's exact fixed-point integrator instead of a
// rounded approximation. This drives a real 90-tick suppressed run and
// checks that a client doing that exact replay lands EXACTLY on the
// authoritative position — zero drift, not just bounded drift.
func TestSuppressedDeltasStayExactlySyncedViaRemainder(t *testing.T) {
	const moveSpeed = 7.0
	const unitsPerMeter = 10.0
	const tickRate = 20
	const milliRate = uint64(moveSpeed * unitsPerMeter * 1000 / tickRate) // 3500, exact

	if err := units.LoadDefinitions([]units.Definition{{
		ID: "spearman", TypeID: 1, HP: 100, Stamina: 100,
		MoveSpeed: moveSpeed, RangeType: "melee", ActiveSeconds: 0.1, RecoverySeconds: 0.2,
		SprintSpeedMultiplier: 1.5, AnimationSpeed: 0.1,
	}}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Build(&config.GameSettings{
		TickRate: tickRate, SyncIntervalSec: 3600, UnitsPerMeter: unitsPerMeter,
		WorldWidth: 60000, WorldHeight: 60000,
		SpawnMinX: 100, SpawnMaxX: 200, SpawnMinY: 100, SpawnMaxY: 200,
		PlayerBaseScale: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	live := config.NewLiveNet(config.BuildLiveNetConfig(cfg))
	gw := NewGameWorld(cfg, live, newTestCollisionWorld(t, 60000, 60000))
	t.Cleanup(gw.Stop)
	gw.SetTickInterval(time.Hour)

	p, err := gw.AddPlayer("spearman")
	if err != nil {
		t.Fatalf("AddPlayer: %v", err)
	}

	var lastAll, lastChanged []types.PlayerState
	var lastFullSync bool
	var lastWorldTick uint32
	gw.SetTickBroadcaster(func(all []types.PlayerState, changed []types.PlayerState, fullSync bool, worldTick uint32, computeDur time.Duration) bool {
		lastAll, lastChanged, lastFullSync, lastWorldTick = all, changed, fullSync, worldTick
		return true
	})

	findSelf := func(states []types.PlayerState) (types.PlayerState, bool) {
		for _, st := range states {
			if st.ID == p.ID {
				return st, true
			}
		}
		return types.PlayerState{}, false
	}

	gw.tick()
	if !lastFullSync {
		t.Fatal("first tick must be a full sync establishing the baseline")
	}
	baseline, ok := findSelf(lastAll)
	if !ok {
		t.Fatal("player missing from initial full sync")
	}
	clientX := int64(baseline.X)
	clientRemainder := uint64(baseline.MoveRemainderMilli)
	clientTick := lastWorldTick

	if res := gw.QueueMovementInput(p.ID, 1, 0, 1, false); res != types.InputAccepted {
		t.Fatalf("QueueMovementInput rejected: %v", res)
	}

	const ticksToRun = 90
	for i := 0; i < ticksToRun; i++ {
		gw.tick()

		var record types.PlayerState
		var received bool
		if lastFullSync {
			record, received = findSelf(lastAll)
		} else {
			record, received = findSelf(lastChanged)
		}
		if received {
			clientX = int64(record.X)
			clientRemainder = uint64(record.MoveRemainderMilli)
			clientTick = lastWorldTick
		}
	}

	elapsed := uint64(lastWorldTick - clientTick)
	totalMilli := clientRemainder + milliRate*elapsed
	predictedX := clientX + int64(totalMilli/1000)

	if realX := int64(p.GetX()); realX != predictedX {
		t.Fatalf("remainder-synced client replay = %d, want exact authoritative X = %d (after %d ticks, %d suppressed since last update)",
			predictedX, realX, ticksToRun, elapsed)
	}
}
