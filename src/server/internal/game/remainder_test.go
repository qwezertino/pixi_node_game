package game

import (
	"testing"
	"time"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/protocol"
	"pixi_game_server/internal/types"
	"pixi_game_server/internal/units"
)

// TestTickPreservesPerPlayerRemainderAcrossSnapshotSort is the P2 regression
// from the 2026-09-10 review: AppendGameState (the encoder) sorts the
// []types.PlayerState slice it is handed in place by ID, but
// scratchRemainders used to be a second slice built in map-iteration order
// and was never sorted along with it. After the encoder returned, the tick
// saved prevMoveRemainder[state.ID] = scratchRemainders[i], which after the
// sort could belong to a completely different player.
//
// This drives a real gw.tick() and a real full-sync encode (so the encoder
// actually reorders the backing slice), over players whose IDs are visited
// in Go's randomized map-iteration order, and checks every player's saved
// baseline remainder is its own — not a neighbor's.
func TestTickPreservesPerPlayerRemainderAcrossSnapshotSort(t *testing.T) {
	if err := units.LoadDefinitions([]units.Definition{{
		ID: "spearman", TypeID: 1, HP: 100, Stamina: 100,
		MoveSpeed: 10, RangeType: "melee", ActiveSeconds: 0.1, RecoverySeconds: 0.2,
		SprintSpeedMultiplier: 1.5, AnimationSpeed: 0.1,
	}}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Build(&config.GameSettings{
		TickRate: 20, SyncIntervalSec: 30, UnitsPerMeter: 10,
		WorldWidth: 1000, WorldHeight: 1000,
		SpawnMinX: 100, SpawnMaxX: 200, SpawnMinY: 100, SpawnMaxY: 200,
		PlayerBaseScale: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	live := config.NewLiveNet(config.BuildLiveNetConfig(cfg))
	gw := NewGameWorld(cfg, live, newTestCollisionWorld(t, 1000, 1000))
	t.Cleanup(gw.Stop)

	gw.SetTickInterval(time.Hour)

	const numPlayers = 8
	wantRemainder := make(map[uint32]uint32, numPlayers)
	players := make([]*types.Player, 0, numPlayers)
	for i := 0; i < numPlayers; i++ {
		p, err := gw.AddPlayer("")
		if err != nil {
			t.Fatalf("AddPlayer: %v", err)
		}

		p.SetVX(0)
		p.SetVY(0)
		remainder := uint32(37 * (i + 1))
		p.SetMoveRemainderMilli(remainder)
		wantRemainder[p.ID] = remainder
		players = append(players, p)
	}

	var encoded []types.PlayerState
	gw.SetTickBroadcaster(func(all []types.PlayerState, changed []types.PlayerState, fullSync bool, worldTick uint32, computeDur time.Duration) bool {
		var proto protocol.BinaryProtocol
		proto.AppendGameState(nil, all, 1, worldTick, 10000)
		encoded = all
		return true
	})

	gw.tick()

	if len(encoded) != numPlayers {
		t.Fatalf("encoded %d players, want %d", len(encoded), numPlayers)
	}
	for i := 1; i < len(encoded); i++ {
		if encoded[i-1].ID > encoded[i].ID {
			t.Fatalf("encoder did not sort by ID as expected, got order %v", encoded)
		}
	}

	for _, p := range players {
		want := wantRemainder[p.ID]
		got := gw.prevMoveRemainder[p.ID]
		if got != want {
			t.Fatalf("player %d: prevMoveRemainder = %d, want %d (remainder association corrupted by snapshot sort)", p.ID, got, want)
		}
	}
}
