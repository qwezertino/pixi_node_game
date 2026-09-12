package collision

import "testing"

func TestGridDims(t *testing.T) {
	cases := []struct {
		name                string
		width, height, cell int32
		wantCols, wantRows  int32
	}{
		{"divides_evenly", 32000, 32000, 64, 500, 500},
		{"non_divisible_rounds_up", 100, 100, 64, 2, 2},
		{"exact_single_cell", 64, 64, 64, 1, 1},
		{"one_unit_over", 65, 64, 64, 2, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cols, rows := gridDims(c.width, c.height, c.cell)
			if cols != c.wantCols || rows != c.wantRows {
				t.Errorf("gridDims(%d,%d,%d) = (%d,%d), want (%d,%d)",
					c.width, c.height, c.cell, cols, rows, c.wantCols, c.wantRows)
			}
		})
	}
}

func TestFlatIndex(t *testing.T) {
	if got := flatIndex(0, 0, 500); got != 0 {
		t.Errorf("flatIndex(0,0,500) = %d, want 0", got)
	}
	if got := flatIndex(3, 2, 500); got != 1003 {
		t.Errorf("flatIndex(3,2,500) = %d, want 1003", got)
	}
}

func TestCellRangeClampsToWorldBounds(t *testing.T) {
	minCell, maxCell := cellRange(-100, 10000, 64, 10)
	if minCell != 0 || maxCell != 9 {
		t.Errorf("cellRange out-of-bounds = (%d,%d), want (0,9)", minCell, maxCell)
	}
}

func TestBuildMapStaticGrid_SingleCellObject(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: 10, MinY: 10, MaxX: 20, MaxY: 20}}
	g, err := BuildMapStaticGrid(640, 640, colliders)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	if g.Columns() != 10 || g.Rows() != 10 {
		t.Fatalf("dims = (%d,%d), want (10,10)", g.Columns(), g.Rows())
	}
	scratch := &QueryScratch{}
	got := g.QueryAABB(AABB{MinX: 0, MinY: 0, MaxX: 63, MaxY: 63}, scratch)
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("QueryAABB = %v, want [0]", got)
	}
}

func TestBuildMapStaticGrid_MultiCellObject(t *testing.T) {
	// Spans cell (0,0) through (2,1) given cellSize=64.
	colliders := []MapAABB{{ID: 1, MinX: 10, MinY: 10, MaxX: 150, MaxY: 100}}
	g, err := BuildMapStaticGrid(640, 640, colliders)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	scratch := &QueryScratch{}
	// query far cell not touching the collider
	got := g.QueryAABB(AABB{MinX: 600, MinY: 600, MaxX: 639, MaxY: 639}, scratch)
	if len(got) != 0 {
		t.Errorf("expected no candidates far from collider, got %v", got)
	}
	got = g.QueryAABB(AABB{MinX: 128, MinY: 64, MaxX: 130, MaxY: 66}, scratch)
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("expected collider present in far corner cell of its span, got %v", got)
	}
}

func TestBuildMapStaticGrid_BoundaryTouchingCell(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: 64, MinY: 64, MaxX: 128, MaxY: 128}}
	g, err := BuildMapStaticGrid(640, 640, colliders)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	scratch := &QueryScratch{}
	got := g.QueryAABB(AABB{MinX: 60, MinY: 60, MaxX: 64, MaxY: 64}, scratch)
	if len(got) != 1 {
		t.Errorf("expected query spanning the cell boundary at 64 to include the collider, got %v", got)
	}
	got = g.QueryAABB(AABB{MinX: 0, MinY: 0, MaxX: 63, MaxY: 63}, scratch)
	if len(got) != 0 {
		t.Errorf("expected query strictly before the boundary to exclude the collider, got %v", got)
	}
}

func TestBuildMapStaticGrid_DeterministicOrdering(t *testing.T) {
	colliders := []MapAABB{
		{ID: 5, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10},
		{ID: 2, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10},
	}
	g, err := BuildMapStaticGrid(640, 640, colliders)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	if g.Collider(0).ID != 5 || g.Collider(1).ID != 2 {
		t.Errorf("expected dense indices to preserve input order, got %+v", g)
	}
}

func TestBuildMapStaticGrid_RejectsInvalidAABB(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: 10, MinY: 10, MaxX: 10, MaxY: 20}}
	if _, err := BuildMapStaticGrid(640, 640, colliders); err == nil {
		t.Error("expected error for degenerate AABB")
	}
}

func TestBuildMapStaticGrid_RejectsTooManyColliders(t *testing.T) {
	colliders := make([]MapAABB, MaxMapColliders+1)
	for i := range colliders {
		colliders[i] = MapAABB{ID: uint64(i + 1), MinX: 0, MinY: 0, MaxX: 1, MaxY: 1}
	}
	if _, err := BuildMapStaticGrid(640, 640, colliders); err == nil {
		t.Error("expected error for exceeding MaxMapColliders")
	}
}

func TestQueryAABB_OutOfWorldBoundsClamps(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}}
	g, err := BuildMapStaticGrid(64, 64, colliders)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	scratch := &QueryScratch{}
	got := g.QueryAABB(AABB{MinX: -1000, MinY: -1000, MaxX: 1000, MaxY: 1000}, scratch)
	if len(got) != 1 {
		t.Errorf("expected out-of-bounds query to clamp and still find collider, got %v", got)
	}
}

func TestQueryAABB_NoDuplicateResults(t *testing.T) {
	// Large collider spanning many cells must appear once per query.
	colliders := []MapAABB{{ID: 1, MinX: 0, MinY: 0, MaxX: 500, MaxY: 500}}
	g, err := BuildMapStaticGrid(640, 640, colliders)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	scratch := &QueryScratch{}
	got := g.QueryAABB(AABB{MinX: 0, MinY: 0, MaxX: 500, MaxY: 500}, scratch)
	if len(got) != 1 {
		t.Errorf("expected exactly one deduplicated result, got %v", got)
	}
}

func TestQueryScratch_ReusedAcrossCalls(t *testing.T) {
	colliders := []MapAABB{
		{ID: 1, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10},
		{ID: 2, MinX: 600, MinY: 600, MaxX: 610, MaxY: 610},
	}
	g, err := BuildMapStaticGrid(640, 640, colliders)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	scratch := &QueryScratch{}
	first := g.QueryAABB(AABB{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}, scratch)
	if len(first) != 1 || first[0] != 0 {
		t.Fatalf("first query = %v, want [0]", first)
	}
	second := g.QueryAABB(AABB{MinX: 600, MinY: 600, MaxX: 610, MaxY: 610}, scratch)
	if len(second) != 1 || second[0] != 1 {
		t.Fatalf("second query = %v, want [1]", second)
	}
}

func TestQueryScratch_GenerationWraparound(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}}
	g, err := BuildMapStaticGrid(640, 640, colliders)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	scratch := &QueryScratch{generation: ^uint32(0)}
	got := g.QueryAABB(AABB{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}, scratch)
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("query after generation wraparound = %v, want [0]", got)
	}
	if scratch.generation != 1 {
		t.Errorf("generation after wraparound = %d, want 1", scratch.generation)
	}
}
