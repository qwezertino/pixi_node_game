package collision

import "testing"

func TestDynamicGrid_RebuildAndSwap(t *testing.T) {
	g := NewDynamicGrid(2000, 2000)
	g.Rebuild([]PlayerSnapshot{{ID: 1, X: 100, Y: 100}})
	// Query before swap sees an empty front (initial buffer never rebuilt).
	dst := make([]uint32, 0, 4)
	if got := g.QueryCircle(100, 100, 10, dst, &QueryScratch{}); len(got) != 0 {
		t.Errorf("expected empty front before swap, got %v", got)
	}
	g.Swap()
	if got := g.QueryCircle(100, 100, 10, dst, &QueryScratch{}); len(got) != 1 || got[0] != 1 {
		t.Errorf("expected player 1 visible after swap, got %v", got)
	}
}

func TestDynamicGrid_ClearOnlyTouchedCells(t *testing.T) {
	g := NewDynamicGrid(2000, 2000)
	g.Rebuild([]PlayerSnapshot{{ID: 1, X: 100, Y: 100}, {ID: 2, X: 1500, Y: 1500}})
	g.Swap()

	// Rebuild with only player 1 now; player 2's old cell must be cleared,
	// not linger, but only that cell — not a full-grid scan.
	g.Rebuild([]PlayerSnapshot{{ID: 1, X: 100, Y: 100}})
	g.Swap()

	dst := make([]uint32, 0, 4)
	got := g.QueryCircle(1500, 1500, 10, dst, &QueryScratch{})
	if len(got) != 0 {
		t.Errorf("expected player 2's old cell cleared, got %v", got)
	}
	got = g.QueryCircle(100, 100, 10, dst, &QueryScratch{})
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected player 1 still present, got %v", got)
	}
}

func TestDynamicGrid_NoDuplicateTouchedCellIndices(t *testing.T) {
	g := NewDynamicGrid(2000, 2000)
	// Both players share the same cell (cellSize=64), so it should only be
	// recorded once in touchedCells.
	g.Rebuild([]PlayerSnapshot{{ID: 1, X: 100, Y: 100}, {ID: 2, X: 102, Y: 102}})
	back := g.backBuf()
	seen := map[uint32]int{}
	for _, cell := range back.touchedCells {
		seen[cell]++
	}
	for cell, count := range seen {
		if count > 1 {
			t.Errorf("cell %d recorded %d times in touchedCells, want at most once", cell, count)
		}
	}
}

func TestDynamicGrid_PlayerTransitionsBetweenCells(t *testing.T) {
	g := NewDynamicGrid(2000, 2000)
	g.Rebuild([]PlayerSnapshot{{ID: 1, X: 100, Y: 100}})
	g.Swap()

	g.Rebuild([]PlayerSnapshot{{ID: 1, X: 900, Y: 900}})
	g.Swap()

	dst := make([]uint32, 0, 4)
	if got := g.QueryCircle(100, 100, 10, dst, &QueryScratch{}); len(got) != 0 {
		t.Errorf("expected old cell empty after transition, got %v", got)
	}
	if got := g.QueryCircle(900, 900, 10, dst, &QueryScratch{}); len(got) != 1 || got[0] != 1 {
		t.Errorf("expected player present at new cell, got %v", got)
	}
}

func TestDynamicGrid_CircleOnCellBoundaryDeduplicates(t *testing.T) {
	g := NewDynamicGrid(2000, 2000)
	// Player radius 4 straddling a cell boundary at x=64 registers in two
	// cells; a query spanning both must return it exactly once.
	g.Rebuild([]PlayerSnapshot{{ID: 1, X: 64, Y: 100}})
	g.Swap()

	dst := make([]uint32, 0, 4)
	got := g.QueryAABB(AABB{MinX: 0, MinY: 90, MaxX: 128, MaxY: 110}, dst, &QueryScratch{})
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected deduplicated single result, got %v", got)
	}
}

func TestDynamicGrid_QueryCircle_ExcludesFarPlayers(t *testing.T) {
	g := NewDynamicGrid(2000, 2000)
	g.Rebuild([]PlayerSnapshot{{ID: 1, X: 100, Y: 100}, {ID: 2, X: 1900, Y: 1900}})
	g.Swap()

	dst := make([]uint32, 0, 4)
	got := g.QueryCircle(100, 100, 50, dst, &QueryScratch{})
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected only nearby player, got %v", got)
	}
}

func TestDynamicGrid_QueryCircle_ReturnsAscendingIDs(t *testing.T) {
	g := NewDynamicGrid(2000, 2000)
	g.Rebuild([]PlayerSnapshot{{ID: 5, X: 100, Y: 100}, {ID: 2, X: 105, Y: 100}, {ID: 9, X: 102, Y: 103}})
	g.Swap()

	dst := make([]uint32, 0, 4)
	got := g.QueryCircle(100, 100, 20, dst, &QueryScratch{})
	if len(got) != 3 || got[0] != 2 || got[1] != 5 || got[2] != 9 {
		t.Errorf("expected ascending [2,5,9], got %v", got)
	}
}

func TestDynamicGrid_QueryAABB(t *testing.T) {
	g := NewDynamicGrid(2000, 2000)
	g.Rebuild([]PlayerSnapshot{{ID: 1, X: 100, Y: 100}, {ID: 2, X: 500, Y: 500}})
	g.Swap()

	dst := make([]uint32, 0, 4)
	got := g.QueryAABB(AABB{MinX: 90, MinY: 90, MaxX: 110, MaxY: 110}, dst, &QueryScratch{})
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected only player 1, got %v", got)
	}
}

func TestDynamicGrid_QuerySegment_BroadPhase(t *testing.T) {
	g := NewDynamicGrid(2000, 2000)
	g.Rebuild([]PlayerSnapshot{{ID: 1, X: 100, Y: 100}, {ID: 2, X: 900, Y: 900}})
	g.Swap()

	dst := make([]uint32, 0, 4)
	got := g.QuerySegment(0, 0, 200, 200, 10, dst, &QueryScratch{})
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected only player near the segment, got %v", got)
	}
}

func TestDynamicGrid_MultiplayerGhostMovement_BothPresentAfterTick(t *testing.T) {
	g := NewDynamicGrid(2000, 2000)
	g.Rebuild([]PlayerSnapshot{{ID: 1, X: 100, Y: 100}, {ID: 2, X: 101, Y: 100}})
	g.Swap()

	dst := make([]uint32, 0, 4)
	got := g.QueryCircle(100, 100, 10, dst, &QueryScratch{})
	if len(got) != 2 {
		t.Errorf("expected both overlapping players present (ghost movement), got %v", got)
	}
}

func TestDynamicGrid_RebuildDoesNotDependOnInputOrder(t *testing.T) {
	g1 := NewDynamicGrid(2000, 2000)
	g1.Rebuild([]PlayerSnapshot{{ID: 1, X: 100, Y: 100}, {ID: 2, X: 200, Y: 200}, {ID: 3, X: 300, Y: 300}})
	g1.Swap()

	g2 := NewDynamicGrid(2000, 2000)
	g2.Rebuild([]PlayerSnapshot{{ID: 3, X: 300, Y: 300}, {ID: 1, X: 100, Y: 100}, {ID: 2, X: 200, Y: 200}})
	g2.Swap()

	dst1 := make([]uint32, 0, 4)
	dst2 := make([]uint32, 0, 4)
	got1 := g1.QueryAABB(AABB{MinX: 0, MinY: 0, MaxX: 400, MaxY: 400}, dst1, &QueryScratch{})
	got2 := g2.QueryAABB(AABB{MinX: 0, MinY: 0, MaxX: 400, MaxY: 400}, dst2, &QueryScratch{})
	if len(got1) != len(got2) {
		t.Fatalf("result length differs by input order: %v vs %v", got1, got2)
	}
	for i := range got1 {
		if got1[i] != got2[i] {
			t.Errorf("result differs by input order at %d: %v vs %v", i, got1, got2)
		}
	}
}
