package game

import (
	"testing"

	"pixi_game_server/internal/collision"
)

func newTestGameWorldForStructureMutation(t *testing.T, width, height int32) *GameWorld {
	t.Helper()
	mapGrid, err := collision.BuildMapStaticGrid(width, height, nil)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	structGrid, err := collision.BuildStructureGrid(width, height, nil, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	world := collision.NewCollisionWorld(width, height, mapGrid, structGrid)
	dynamicGrid := collision.NewDynamicGrid(width, height)
	world.SetDynamic(dynamicGrid)
	return &GameWorld{collisionWorld: world, dynamicGrid: dynamicGrid}
}

func TestApplyPendingStructureMutations_AppliesClearBatch(t *testing.T) {
	gw := newTestGameWorldForStructureMutation(t, 2000, 2000)
	key := collision.StructureColliderKey{StructureID: 1, PartID: 1}
	wall := collision.StructureAABB{StructureID: 1, PartID: 1, MinX: 100, MinY: 100, MaxX: 200, MaxY: 200, RenderKind: "wood_wall"}

	gw.QueueStructureMutation([]collision.StructureMutation{
		{Operation: collision.StructureOpUpsert, Key: key, Collider: wall},
	})
	gw.applyPendingStructureMutations(1)

	if _, ok := gw.collisionWorld.Structures().Lookup(key); !ok {
		t.Fatal("expected wall to be applied when footprint is clear")
	}
	if gw.collisionWorld.Structures().Revision() != 1 {
		t.Fatalf("revision = %d, want 1", gw.collisionWorld.Structures().Revision())
	}
}

func TestApplyPendingStructureMutations_DefersWhenPlayerOccupiesFootprint(t *testing.T) {
	gw := newTestGameWorldForStructureMutation(t, 2000, 2000)
	gw.dynamicGrid.Rebuild([]collision.PlayerSnapshot{{ID: 1, X: 150, Y: 150}})
	gw.dynamicGrid.Swap()

	key := collision.StructureColliderKey{StructureID: 1, PartID: 1}
	wall := collision.StructureAABB{StructureID: 1, PartID: 1, MinX: 100, MinY: 100, MaxX: 200, MaxY: 200, RenderKind: "wood_wall"}

	gw.QueueStructureMutation([]collision.StructureMutation{
		{Operation: collision.StructureOpUpsert, Key: key, Collider: wall},
	})
	gw.applyPendingStructureMutations(1)

	if _, ok := gw.collisionWorld.Structures().Lookup(key); ok {
		t.Fatal("expected wall activation deferred while a player occupies the footprint")
	}
	if gw.collisionWorld.Structures().Revision() != 0 {
		t.Fatalf("revision changed on deferred batch: %d", gw.collisionWorld.Structures().Revision())
	}

	// Player leaves; the deferred batch must retry and succeed on a later tick.
	gw.dynamicGrid.Rebuild([]collision.PlayerSnapshot{{ID: 1, X: 1800, Y: 1800}})
	gw.dynamicGrid.Swap()
	gw.applyPendingStructureMutations(2)

	if _, ok := gw.collisionWorld.Structures().Lookup(key); !ok {
		t.Fatal("expected deferred wall to activate once the footprint clears")
	}
}

func TestApplyPendingStructureMutations_CompositeBatchAllOrNothing(t *testing.T) {
	gw := newTestGameWorldForStructureMutation(t, 2000, 2000)
	// Player occupies only the second part's footprint.
	gw.dynamicGrid.Rebuild([]collision.PlayerSnapshot{{ID: 1, X: 550, Y: 150}})
	gw.dynamicGrid.Swap()

	keyA := collision.StructureColliderKey{StructureID: 1, PartID: 1}
	keyB := collision.StructureColliderKey{StructureID: 1, PartID: 2}
	gw.QueueStructureMutation([]collision.StructureMutation{
		{Operation: collision.StructureOpUpsert, Key: keyA, Collider: collision.StructureAABB{StructureID: 1, PartID: 1, MinX: 100, MinY: 100, MaxX: 200, MaxY: 200, RenderKind: "wood_wall"}},
		{Operation: collision.StructureOpUpsert, Key: keyB, Collider: collision.StructureAABB{StructureID: 1, PartID: 2, MinX: 500, MinY: 100, MaxX: 600, MaxY: 200, RenderKind: "wood_wall"}},
	})
	gw.applyPendingStructureMutations(1)

	if _, ok := gw.collisionWorld.Structures().Lookup(keyA); ok {
		t.Error("expected the free half of the composite structure to NOT activate alone")
	}
	if _, ok := gw.collisionWorld.Structures().Lookup(keyB); ok {
		t.Error("expected the occupied half to not activate either")
	}
}

func TestApplyPendingStructureMutations_RemovalNeedsNoClearance(t *testing.T) {
	structGrid, err := collision.BuildStructureGrid(2000, 2000, []collision.StructureAABB{
		{StructureID: 1, PartID: 1, MinX: 100, MinY: 100, MaxX: 200, MaxY: 200, RenderKind: "wood_wall"},
	}, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	mapGrid, err := collision.BuildMapStaticGrid(2000, 2000, nil)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	world := collision.NewCollisionWorld(2000, 2000, mapGrid, structGrid)
	dynamicGrid := collision.NewDynamicGrid(2000, 2000)
	dynamicGrid.Rebuild([]collision.PlayerSnapshot{{ID: 1, X: 150, Y: 150}})
	dynamicGrid.Swap()
	world.SetDynamic(dynamicGrid)
	gw := &GameWorld{collisionWorld: world, dynamicGrid: dynamicGrid}

	key := collision.StructureColliderKey{StructureID: 1, PartID: 1}
	gw.QueueStructureMutation([]collision.StructureMutation{
		{Operation: collision.StructureOpRemove, Key: key},
	})
	gw.applyPendingStructureMutations(1)

	if _, ok := gw.collisionWorld.Structures().Lookup(key); ok {
		t.Error("expected removal to apply immediately even though a player occupies the footprint")
	}
}
