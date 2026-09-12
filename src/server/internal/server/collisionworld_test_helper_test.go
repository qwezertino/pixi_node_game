package server

import (
	"testing"

	"pixi_game_server/internal/collision"
	"pixi_game_server/internal/config"
	"pixi_game_server/internal/game"
	"pixi_game_server/internal/types"
)

// newEmptyCollisionWorld builds a CollisionWorld with no map/structure
// colliders, sized to cfg's world, for tests that only need a valid
// dependency to satisfy New/NewGameWorld.
func newEmptyCollisionWorld(t *testing.T, cfg *config.Config) *collision.CollisionWorld {
	t.Helper()
	mapGrid, err := collision.BuildMapStaticGrid(int32(cfg.World.Width), int32(cfg.World.Height), nil)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	structGrid, err := collision.BuildStructureGrid(int32(cfg.World.Width), int32(cfg.World.Height), nil, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	return collision.NewCollisionWorld(int32(cfg.World.Width), int32(cfg.World.Height), mapGrid, structGrid)
}

// mustAddPlayer adds a player to gw, failing the test on the (normally
// unreachable in these tests) spawn-rejection error path.
func mustAddPlayer(t *testing.T, gw *game.GameWorld, unitType string) *types.Player {
	t.Helper()
	p, err := gw.AddPlayer(unitType)
	if err != nil {
		t.Fatalf("AddPlayer: %v", err)
	}
	return p
}
