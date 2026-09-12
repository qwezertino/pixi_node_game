package collision

import "testing"

func wall(structureID uint64, partID uint32, minX, minY, maxX, maxY int32) StructureAABB {
	return StructureAABB{StructureID: structureID, PartID: partID, MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY, RenderKind: "wood_wall"}
}

func TestBuildStructureGrid_Restore(t *testing.T) {
	colliders := []StructureAABB{
		wall(9001, 1, 3300, 700, 3340, 1800),
		wall(9002, 1, 4200, 600, 4500, 900),
	}
	g, err := BuildStructureGrid(32000, 32000, colliders, 5)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	if g.Revision() != 5 {
		t.Errorf("revision = %d, want 5", g.Revision())
	}
	if _, ok := g.Lookup(StructureColliderKey{9001, 1}); !ok {
		t.Error("expected restored collider to be looked up")
	}
	scratch := &QueryScratch{}
	got := g.QueryAABB(AABB{MinX: 3300, MinY: 700, MaxX: 3300, MaxY: 700}, scratch)
	if len(got) != 1 {
		t.Errorf("expected restored collider queryable, got %v", got)
	}
}

func TestBuildStructureGrid_RejectsDuplicateKey(t *testing.T) {
	colliders := []StructureAABB{
		wall(1, 1, 0, 0, 10, 10),
		wall(1, 1, 20, 20, 30, 30),
	}
	if _, err := BuildStructureGrid(32000, 32000, colliders, 0); err == nil {
		t.Error("expected error for duplicate (structureId, partId)")
	}
}

func TestBuildStructureGrid_RejectsInvalidAABB(t *testing.T) {
	colliders := []StructureAABB{{StructureID: 1, PartID: 1, MinX: 10, MinY: 0, MaxX: 10, MaxY: 10}}
	if _, err := BuildStructureGrid(32000, 32000, colliders, 0); err == nil {
		t.Error("expected error for degenerate AABB")
	}
}

func TestStructureGrid_UpsertNewCollider(t *testing.T) {
	g, err := BuildStructureGrid(32000, 32000, nil, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	key := StructureColliderKey{StructureID: 1, PartID: 1}
	err = g.ApplyMutationBatch(StructureMutationBatch{
		Revision: 1,
		Mutations: []StructureMutation{
			{Operation: StructureOpUpsert, Key: key, Collider: wall(1, 1, 100, 100, 200, 200)},
		},
	})
	if err != nil {
		t.Fatalf("ApplyMutationBatch: %v", err)
	}
	if g.Revision() != 1 {
		t.Errorf("revision = %d, want 1", g.Revision())
	}
	if _, ok := g.Lookup(key); !ok {
		t.Error("expected collider to be present after upsert")
	}
	scratch := &QueryScratch{}
	got := g.QueryAABB(AABB{MinX: 150, MinY: 150, MaxX: 150, MaxY: 150}, scratch)
	if len(got) != 1 {
		t.Errorf("expected new collider queryable, got %v", got)
	}
}

func TestStructureGrid_RemoveOnlyAffectsTargetCells(t *testing.T) {
	colliders := []StructureAABB{
		wall(1, 1, 0, 0, 10, 10),
		wall(2, 1, 1000, 1000, 1010, 1010),
	}
	g, err := BuildStructureGrid(32000, 32000, colliders, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	err = g.ApplyMutationBatch(StructureMutationBatch{
		Revision:  1,
		Mutations: []StructureMutation{{Operation: StructureOpRemove, Key: StructureColliderKey{1, 1}}},
	})
	if err != nil {
		t.Fatalf("ApplyMutationBatch: %v", err)
	}
	if _, ok := g.Lookup(StructureColliderKey{1, 1}); ok {
		t.Error("expected removed collider to be gone")
	}
	if _, ok := g.Lookup(StructureColliderKey{2, 1}); !ok {
		t.Error("expected untouched collider to remain")
	}
	scratch := &QueryScratch{}
	got := g.QueryAABB(AABB{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}, scratch)
	if len(got) != 0 {
		t.Errorf("expected removed cell empty, got %v", got)
	}
	got = g.QueryAABB(AABB{MinX: 1000, MinY: 1000, MaxX: 1010, MaxY: 1010}, scratch)
	if len(got) != 1 {
		t.Errorf("expected untouched collider still queryable, got %v", got)
	}
}

func TestStructureGrid_SetSolidFalseThenTrue(t *testing.T) {
	g, err := BuildStructureGrid(32000, 32000, []StructureAABB{wall(1, 1, 0, 0, 10, 10)}, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	key := StructureColliderKey{1, 1}
	if err := g.ApplyMutationBatch(StructureMutationBatch{
		Revision:  1,
		Mutations: []StructureMutation{{Operation: StructureOpSetSolid, Key: key, Solid: false}},
	}); err != nil {
		t.Fatalf("set solid false: %v", err)
	}
	if _, ok := g.Lookup(key); ok {
		t.Error("expected collider disabled after set_solid false")
	}
	if err := g.ApplyMutationBatch(StructureMutationBatch{
		Revision:  2,
		Mutations: []StructureMutation{{Operation: StructureOpSetSolid, Key: key, Solid: true, Collider: wall(1, 1, 0, 0, 10, 10)}},
	}); err != nil {
		t.Fatalf("set solid true: %v", err)
	}
	if _, ok := g.Lookup(key); !ok {
		t.Error("expected collider active again after set_solid true")
	}
}

func TestStructureGrid_CompositeStructure_MultiplePartsAtomic(t *testing.T) {
	g, err := BuildStructureGrid(32000, 32000, nil, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	batch := StructureMutationBatch{
		Revision: 1,
		Mutations: []StructureMutation{
			{Operation: StructureOpUpsert, Key: StructureColliderKey{1, 1}, Collider: wall(1, 1, 0, 0, 10, 10)},
			{Operation: StructureOpUpsert, Key: StructureColliderKey{1, 2}, Collider: wall(1, 2, 20, 20, 30, 30)},
		},
	}
	if err := g.ApplyMutationBatch(batch); err != nil {
		t.Fatalf("ApplyMutationBatch: %v", err)
	}
	if _, ok := g.Lookup(StructureColliderKey{1, 1}); !ok {
		t.Error("expected part 1 present")
	}
	if _, ok := g.Lookup(StructureColliderKey{1, 2}); !ok {
		t.Error("expected part 2 present")
	}
}

func TestStructureGrid_InvalidBatchLeavesNoPartialChanges(t *testing.T) {
	g, err := BuildStructureGrid(32000, 32000, nil, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	batch := StructureMutationBatch{
		Revision: 1,
		Mutations: []StructureMutation{
			{Operation: StructureOpUpsert, Key: StructureColliderKey{1, 1}, Collider: wall(1, 1, 0, 0, 10, 10)},
			{Operation: StructureOpUpsert, Key: StructureColliderKey{1, 2}, Collider: StructureAABB{StructureID: 1, PartID: 2, MinX: 10, MinY: 0, MaxX: 10, MaxY: 10}},
		},
	}
	if err := g.ApplyMutationBatch(batch); err == nil {
		t.Fatal("expected error for degenerate second part")
	}
	if g.Revision() != 0 {
		t.Errorf("revision changed on rejected batch: %d", g.Revision())
	}
	if _, ok := g.Lookup(StructureColliderKey{1, 1}); ok {
		t.Error("expected no partial application: part 1 must not be present")
	}
}

func TestStructureGrid_RejectsWrongRevision(t *testing.T) {
	g, err := BuildStructureGrid(32000, 32000, nil, 5)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	batch := StructureMutationBatch{
		Revision:  7,
		Mutations: []StructureMutation{{Operation: StructureOpUpsert, Key: StructureColliderKey{1, 1}, Collider: wall(1, 1, 0, 0, 10, 10)}},
	}
	if err := g.ApplyMutationBatch(batch); err == nil {
		t.Error("expected error for revision gap")
	}

	batch.Revision = 5
	if err := g.ApplyMutationBatch(batch); err == nil {
		t.Error("expected error for duplicate/stale revision")
	}
}

func TestStructureGrid_RejectsRemoveOfUnknownKey(t *testing.T) {
	g, err := BuildStructureGrid(32000, 32000, nil, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	batch := StructureMutationBatch{
		Revision:  1,
		Mutations: []StructureMutation{{Operation: StructureOpRemove, Key: StructureColliderKey{99, 1}}},
	}
	if err := g.ApplyMutationBatch(batch); err == nil {
		t.Error("expected error removing unknown collider")
	}
}

func TestStructureGrid_HandleReuseAfterFullRemoval(t *testing.T) {
	g, err := BuildStructureGrid(32000, 32000, []StructureAABB{wall(1, 1, 0, 0, 10, 10)}, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	if err := g.ApplyMutationBatch(StructureMutationBatch{
		Revision:  1,
		Mutations: []StructureMutation{{Operation: StructureOpRemove, Key: StructureColliderKey{1, 1}}},
	}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := g.ApplyMutationBatch(StructureMutationBatch{
		Revision:  2,
		Mutations: []StructureMutation{{Operation: StructureOpUpsert, Key: StructureColliderKey{2, 1}, Collider: wall(2, 1, 500, 500, 510, 510)}},
	}); err != nil {
		t.Fatalf("upsert reusing slot: %v", err)
	}
	scratch := &QueryScratch{}
	got := g.QueryAABB(AABB{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}, scratch)
	if len(got) != 0 {
		t.Errorf("expected old cell empty after handle reuse, got %v", got)
	}
	got = g.QueryAABB(AABB{MinX: 500, MinY: 500, MaxX: 510, MaxY: 510}, scratch)
	if len(got) != 1 {
		t.Errorf("expected reused-slot collider queryable, got %v", got)
	}
}

func TestStructureGrid_MonotonicRevisionAcrossMultipleBatches(t *testing.T) {
	g, err := BuildStructureGrid(32000, 32000, nil, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	for i := uint64(1); i <= 3; i++ {
		if err := g.ApplyMutationBatch(StructureMutationBatch{
			Revision:  i,
			Mutations: []StructureMutation{{Operation: StructureOpUpsert, Key: StructureColliderKey{i, 1}, Collider: wall(i, 1, int32(i)*100, 0, int32(i)*100+10, 10)}},
		}); err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
	}
	if g.Revision() != 3 {
		t.Errorf("revision = %d, want 3", g.Revision())
	}
}

func TestStructureGrid_QueryScratchIsolatedFromMapStaticGrid(t *testing.T) {
	// Structure and map grids must use independent scratch state per the
	// plan; sharing a QueryScratch across layers would corrupt dedup.
	mapGrid, err := BuildMapStaticGrid(32000, 32000, []MapAABB{{ID: 1, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10, RenderKind: "stone_wall"}})
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	structGrid, err := BuildStructureGrid(32000, 32000, []StructureAABB{wall(1, 1, 0, 0, 10, 10)}, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	mapScratch := &QueryScratch{}
	structScratch := &QueryScratch{}
	mapGot := mapGrid.QueryAABB(AABB{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}, mapScratch)
	structGot := structGrid.QueryAABB(AABB{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}, structScratch)
	if len(mapGot) != 1 || len(structGot) != 1 {
		t.Errorf("expected one candidate in each independent layer, got map=%v structure=%v", mapGot, structGot)
	}
}

func TestStructureGrid_Colliders_SortedAndExcludesRemoved(t *testing.T) {
	g, err := BuildStructureGrid(2000, 2000, []StructureAABB{
		wall(2, 1, 0, 0, 10, 10),
		wall(1, 1, 20, 20, 30, 30),
	}, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	if err := g.ApplyMutationBatch(StructureMutationBatch{
		Revision:  1,
		Mutations: []StructureMutation{{Operation: StructureOpRemove, Key: StructureColliderKey{2, 1}}},
	}); err != nil {
		t.Fatalf("ApplyMutationBatch: %v", err)
	}

	got := g.Colliders(nil)
	if len(got) != 1 || got[0].StructureID != 1 {
		t.Fatalf("Colliders() = %+v, want only structure 1", got)
	}
}

func TestStructureGrid_Colliders_SortOrder(t *testing.T) {
	g, err := BuildStructureGrid(2000, 2000, []StructureAABB{
		wall(3, 1, 0, 0, 10, 10),
		wall(1, 2, 20, 20, 30, 30),
		wall(1, 1, 40, 40, 50, 50),
	}, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	got := g.Colliders(nil)
	if len(got) != 3 {
		t.Fatalf("got %d colliders, want 3", len(got))
	}
	want := []StructureColliderKey{{1, 1}, {1, 2}, {3, 1}}
	for i, k := range want {
		if keyOf(got[i]) != k {
			t.Errorf("index %d = %+v, want key %+v", i, got[i], k)
		}
	}
}
