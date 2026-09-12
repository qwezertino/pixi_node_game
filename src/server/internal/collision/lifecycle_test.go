package collision

import "testing"

func TestStructureLifecycleState_IsSolid(t *testing.T) {
	cases := []struct {
		state StructureLifecycleState
		want  bool
	}{
		{StructureBlueprint, false},
		{StructureUnderConstruction, false},
		{StructureCompletionPendingClearance, false},
		{StructureCompleted, true},
		{StructureDamaged, true},
		{StructureDestroyedFoundation, false},
		{StructureRebuilding, false},
	}
	for _, c := range cases {
		if got := c.state.IsSolid(); got != c.want {
			t.Errorf("state %d IsSolid() = %v, want %v", c.state, got, c.want)
		}
	}
}

func TestFootprintClear_NoDynamicGridAttached(t *testing.T) {
	w := NewCollisionWorld(2000, 2000, mustBuildEmptyMap(t, 2000, 2000), emptyStructureGrid(t, 2000, 2000))
	colliders := []StructureAABB{{StructureID: 1, PartID: 1, MinX: 100, MinY: 100, MaxX: 200, MaxY: 200}}
	if !w.FootprintClear(colliders, &QueryScratch{}) {
		t.Error("expected clear when no DynamicGrid is attached")
	}
}

func TestFootprintClear_EmptyColliderListIsClear(t *testing.T) {
	w := NewCollisionWorld(2000, 2000, mustBuildEmptyMap(t, 2000, 2000), emptyStructureGrid(t, 2000, 2000))
	if !w.FootprintClear(nil, &QueryScratch{}) {
		t.Error("expected clear for an empty collider list")
	}
}

func TestFootprintClear_BlockedByPlayerInFootprint(t *testing.T) {
	w := NewCollisionWorld(2000, 2000, mustBuildEmptyMap(t, 2000, 2000), emptyStructureGrid(t, 2000, 2000))
	dyn := NewDynamicGrid(2000, 2000)
	dyn.Rebuild([]PlayerSnapshot{{ID: 1, X: 150, Y: 150}})
	dyn.Swap()
	w.SetDynamic(dyn)

	colliders := []StructureAABB{{StructureID: 1, PartID: 1, MinX: 100, MinY: 100, MaxX: 200, MaxY: 200}}
	if w.FootprintClear(colliders, &QueryScratch{}) {
		t.Error("expected footprint blocked by a player standing inside it")
	}
}

func TestFootprintClear_FreeWhenNoPlayerNearby(t *testing.T) {
	w := NewCollisionWorld(2000, 2000, mustBuildEmptyMap(t, 2000, 2000), emptyStructureGrid(t, 2000, 2000))
	dyn := NewDynamicGrid(2000, 2000)
	dyn.Rebuild([]PlayerSnapshot{{ID: 1, X: 1800, Y: 1800}})
	dyn.Swap()
	w.SetDynamic(dyn)

	colliders := []StructureAABB{{StructureID: 1, PartID: 1, MinX: 100, MinY: 100, MaxX: 200, MaxY: 200}}
	if !w.FootprintClear(colliders, &QueryScratch{}) {
		t.Error("expected footprint clear when no player is nearby")
	}
}

func TestFootprintClear_CompositeStructureChecksUnionOfAllParts(t *testing.T) {
	w := NewCollisionWorld(2000, 2000, mustBuildEmptyMap(t, 2000, 2000), emptyStructureGrid(t, 2000, 2000))
	dyn := NewDynamicGrid(2000, 2000)
	// Player sits only inside the second part's footprint.
	dyn.Rebuild([]PlayerSnapshot{{ID: 1, X: 550, Y: 150}})
	dyn.Swap()
	w.SetDynamic(dyn)

	colliders := []StructureAABB{
		{StructureID: 1, PartID: 1, MinX: 100, MinY: 100, MaxX: 200, MaxY: 200},
		{StructureID: 1, PartID: 2, MinX: 500, MinY: 100, MaxX: 600, MaxY: 200},
	}
	if w.FootprintClear(colliders, &QueryScratch{}) {
		t.Error("expected composite footprint blocked when any part is occupied")
	}
}
