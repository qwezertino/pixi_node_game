package collision

import "testing"

func emptyStructureGrid(t *testing.T, worldWidth, worldHeight int32) *StructureGrid {
	t.Helper()
	g, err := BuildStructureGrid(worldWidth, worldHeight, nil, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	return g
}

func newTestWorld(t *testing.T, colliders []MapAABB) *CollisionWorld {
	t.Helper()
	mapGrid, err := BuildMapStaticGrid(2000, 2000, colliders)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	return NewCollisionWorld(2000, 2000, mapGrid, emptyStructureGrid(t, 2000, 2000))
}

func TestMoveCircle_UnobstructedMovement(t *testing.T) {
	w := newTestWorld(t, nil)
	res := w.MoveCircle(100, 100, 1, 0, 10, &MoveScratch{})
	if res.X != 110 || res.Y != 100 {
		t.Errorf("got (%d,%d), want (110,100)", res.X, res.Y)
	}
	if res.EffectiveDX != 1 || res.EffectiveDY != 0 {
		t.Errorf("effective velocity = (%d,%d), want (1,0)", res.EffectiveDX, res.EffectiveDY)
	}
	if res.BlockedX || res.BlockedY {
		t.Error("expected no blocking for unobstructed movement")
	}
}

func TestMoveCircle_DirectCollisionFromAllFourSides(t *testing.T) {
	// Wall occupying a 20x20 box centered at (200,200).
	wall := MapAABB{ID: 1, MinX: 190, MinY: 190, MaxX: 210, MaxY: 210, RenderKind: "stone_wall"}

	cases := []struct {
		name   string
		startX uint16
		startY uint16
		dx, dy int8
		wantX  uint16
		wantY  uint16
	}{
		{"from_left", 150, 200, 1, 0, 186, 200},
		{"from_right", 250, 200, -1, 0, 214, 200},
		{"from_top", 200, 150, 0, 1, 200, 186},
		{"from_bottom", 200, 250, 0, -1, 200, 214},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newTestWorld(t, []MapAABB{wall})
			res := w.MoveCircle(c.startX, c.startY, c.dx, c.dy, 64, &MoveScratch{})
			if res.X != c.wantX || res.Y != c.wantY {
				t.Errorf("stopped at (%d,%d), want (%d,%d)", res.X, res.Y, c.wantX, c.wantY)
			}
		})
	}
}

func TestMoveCircle_DiagonalSliding(t *testing.T) {
	// Vertical wall to the right; moving diagonally down-right should slide
	// down along the wall once X is blocked.
	wall := MapAABB{ID: 1, MinX: 210, MinY: 0, MaxX: 250, MaxY: 2000, RenderKind: "stone_wall"}
	w := newTestWorld(t, []MapAABB{wall})

	res := w.MoveCircle(190, 100, 1, 1, 30, &MoveScratch{})
	if int32(res.X) >= 210-PlayerRadius+1 {
		t.Errorf("expected X blocked near wall boundary, got X=%d", res.X)
	}
	if res.Y != 130 {
		t.Errorf("expected Y to continue moving freely (sliding), got Y=%d", res.Y)
	}
	if res.EffectiveDX != 0 || res.EffectiveDY != 1 {
		t.Errorf("effective velocity = (%d,%d), want (0,1) for sliding", res.EffectiveDX, res.EffectiveDY)
	}
	if !res.BlockedX || res.BlockedY {
		t.Errorf("BlockedX=%v BlockedY=%v, want BlockedX=true BlockedY=false", res.BlockedX, res.BlockedY)
	}
}

func TestMoveCircle_FullStopWhenBothAxesBlocked(t *testing.T) {
	// Inner corner: walls both to the right and below.
	wallRight := MapAABB{ID: 1, MinX: 210, MinY: 0, MaxX: 250, MaxY: 2000, RenderKind: "stone_wall"}
	wallBelow := MapAABB{ID: 2, MinX: 0, MinY: 210, MaxX: 2000, MaxY: 250, RenderKind: "stone_wall"}
	w := newTestWorld(t, []MapAABB{wallRight, wallBelow})

	res := w.MoveCircle(200, 200, 1, 1, 20, &MoveScratch{})
	if res.EffectiveDX != 0 || res.EffectiveDY != 0 {
		t.Errorf("expected full stop at inner corner, got effective velocity (%d,%d)", res.EffectiveDX, res.EffectiveDY)
	}
	if !res.BlockedX || !res.BlockedY {
		t.Error("expected both axes blocked at inner corner")
	}
}

func TestMoveCircle_OuterCornerAllowsDiagonalPastEdge(t *testing.T) {
	// A single small block; moving diagonally past its corner should not be
	// blocked once clear of the block's edge.
	block := MapAABB{ID: 1, MinX: 200, MinY: 200, MaxX: 210, MaxY: 210, RenderKind: "stone_wall"}
	w := newTestWorld(t, []MapAABB{block})

	res := w.MoveCircle(150, 150, 1, 1, 40, &MoveScratch{})
	if res.EffectiveDX != 1 || res.EffectiveDY != 1 {
		t.Errorf("expected free diagonal movement clear of the block corner, got (%d,%d) effective, final (%d,%d)",
			res.EffectiveDX, res.EffectiveDY, res.X, res.Y)
	}
}

func TestMoveCircle_ResumesAfterPassingWallEdge(t *testing.T) {
	// Short wall segment; sliding along Y should let X resume once past the
	// wall's vertical extent.
	wall := MapAABB{ID: 1, MinX: 210, MinY: 190, MaxX: 250, MaxY: 210, RenderKind: "stone_wall"}
	w := newTestWorld(t, []MapAABB{wall})

	res := w.MoveCircle(190, 190, 1, 1, 64, &MoveScratch{})
	if res.X <= 210 {
		t.Errorf("expected X to resume past wall's Y extent, got X=%d", res.X)
	}
}

func TestMoveCircle_MultipleWalls_LShapedCorner(t *testing.T) {
	horizontal := MapAABB{ID: 1, MinX: 100, MinY: 300, MaxX: 400, MaxY: 320, RenderKind: "stone_wall"}
	vertical := MapAABB{ID: 2, MinX: 380, MinY: 100, MaxX: 400, MaxY: 320, RenderKind: "stone_wall"}
	w := newTestWorld(t, []MapAABB{horizontal, vertical})

	res := w.MoveCircle(370, 280, 1, 1, 64, &MoveScratch{})
	if res.EffectiveDX != 0 || res.EffectiveDY != 0 {
		t.Errorf("expected both axes blocked at L-shaped corner, got effective (%d,%d) final (%d,%d)",
			res.EffectiveDX, res.EffectiveDY, res.X, res.Y)
	}
}

func TestMoveCircle_MovementAwayFromWallSurfaceIsFree(t *testing.T) {
	wall := MapAABB{ID: 1, MinX: 190, MinY: 190, MaxX: 210, MaxY: 210, RenderKind: "stone_wall"}
	w := newTestWorld(t, []MapAABB{wall})

	// Start touching the wall boundary, then move away from it.
	res := w.MoveCircle(186, 200, -1, 0, 10, &MoveScratch{})
	if res.X != 176 {
		t.Errorf("expected free movement away from wall, got X=%d", res.X)
	}
}

func TestMoveCircle_AntiTunneling_ThinWalls(t *testing.T) {
	for _, thickness := range []int32{1, 2, 40} {
		t.Run("", func(t *testing.T) {
			wallMinX := int32(500)
			wall := MapAABB{ID: 1, MinX: wallMinX, MinY: 0, MaxX: wallMinX + thickness, MaxY: 2000, RenderKind: "stone_wall"}
			w := newTestWorld(t, []MapAABB{wall})

			res := w.MoveCircle(400, 100, 1, 0, MaxMoveSteps, &MoveScratch{})
			if int32(res.X) >= wallMinX {
				t.Errorf("thickness=%d: player tunneled through wall, stopped at X=%d (wall starts at %d)",
					thickness, res.X, wallMinX)
			}
		})
	}
}

func TestMoveCircle_AntiTunneling_SprintDistance(t *testing.T) {
	// Rogue sprint distance from the plan: 26 world units/tick after rounding.
	wallMinX := int32(500)
	wall := MapAABB{ID: 1, MinX: wallMinX, MinY: 0, MaxX: wallMinX + 1, MaxY: 2000, RenderKind: "stone_wall"}
	w := newTestWorld(t, []MapAABB{wall})

	res := w.MoveCircle(480, 100, 1, 0, 26, &MoveScratch{})
	if int32(res.X) >= wallMinX {
		t.Errorf("player tunneled through 1-unit wall at sprint distance, stopped at X=%d", res.X)
	}
}

func TestMoveCircle_HardLimitDistance64DoesNotTunnel(t *testing.T) {
	wallMinX := int32(500)
	wall := MapAABB{ID: 1, MinX: wallMinX, MinY: 0, MaxX: wallMinX + 1, MaxY: 2000, RenderKind: "stone_wall"}
	w := newTestWorld(t, []MapAABB{wall})

	res := w.MoveCircle(440, 100, 1, 0, 64, &MoveScratch{})
	if int32(res.X) >= wallMinX {
		t.Errorf("player tunneled through wall at hard limit distance 64, stopped at X=%d", res.X)
	}
}

func TestMoveCircle_DistanceOne(t *testing.T) {
	w := newTestWorld(t, nil)
	res := w.MoveCircle(100, 100, 1, 0, 1, &MoveScratch{})
	if res.X != 101 {
		t.Errorf("distance=1 should move exactly one unit, got X=%d", res.X)
	}
}

func TestMoveCircle_DistanceAboveHardLimitIsNoOp(t *testing.T) {
	w := newTestWorld(t, nil)
	res := w.MoveCircle(100, 100, 1, 0, 65, &MoveScratch{})
	if res.X != 100 || res.Y != 100 {
		t.Errorf("expected no movement for distance > MaxMoveSteps, got (%d,%d)", res.X, res.Y)
	}
}

func TestMoveCircle_NegativeDistancePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for negative distance")
		}
	}()
	w := newTestWorld(t, nil)
	w.MoveCircle(100, 100, 1, 0, -1, &MoveScratch{})
}

func TestMoveCircle_WorldBounds_FourSides(t *testing.T) {
	w := NewCollisionWorld(1000, 1000, mustBuildEmptyMap(t, 1000, 1000), emptyStructureGrid(t, 1000, 1000))

	if res := w.MoveCircle(10, 500, -1, 0, 64, &MoveScratch{}); res.X != uint16(PlayerRadius) {
		t.Errorf("west bound: X=%d, want %d", res.X, PlayerRadius)
	}
	if res := w.MoveCircle(990, 500, 1, 0, 64, &MoveScratch{}); res.X != uint16(1000-PlayerRadius) {
		t.Errorf("east bound: X=%d, want %d", res.X, 1000-PlayerRadius)
	}
	if res := w.MoveCircle(500, 10, 0, -1, 64, &MoveScratch{}); res.Y != uint16(PlayerRadius) {
		t.Errorf("north bound: Y=%d, want %d", res.Y, PlayerRadius)
	}
	if res := w.MoveCircle(500, 990, 0, 1, 64, &MoveScratch{}); res.Y != uint16(1000-PlayerRadius) {
		t.Errorf("south bound: Y=%d, want %d", res.Y, 1000-PlayerRadius)
	}
}

func TestMoveCircle_WorldCorner_Diagonal(t *testing.T) {
	w := NewCollisionWorld(1000, 1000, mustBuildEmptyMap(t, 1000, 1000), emptyStructureGrid(t, 1000, 1000))
	res := w.MoveCircle(10, 10, -1, -1, 64, &MoveScratch{})
	if res.X != uint16(PlayerRadius) || res.Y != uint16(PlayerRadius) {
		t.Errorf("corner clamp = (%d,%d), want (%d,%d)", res.X, res.Y, PlayerRadius, PlayerRadius)
	}
}

func TestMoveCircle_BoundaryCentersAreValid(t *testing.T) {
	w := NewCollisionWorld(1000, 1000, mustBuildEmptyMap(t, 1000, 1000), emptyStructureGrid(t, 1000, 1000))
	res := w.MoveCircle(uint16(PlayerRadius), uint16(PlayerRadius), -1, -1, 1, &MoveScratch{})
	if res.X != uint16(PlayerRadius) || res.Y != uint16(PlayerRadius) {
		t.Errorf("expected minimal boundary center to stay in place, got (%d,%d)", res.X, res.Y)
	}
	res = w.MoveCircle(uint16(1000-PlayerRadius), uint16(1000-PlayerRadius), 1, 1, 1, &MoveScratch{})
	if res.X != uint16(1000-PlayerRadius) || res.Y != uint16(1000-PlayerRadius) {
		t.Errorf("expected maximal boundary center to stay in place, got (%d,%d)", res.X, res.Y)
	}
}

func TestMoveCircle_StructureLayerBlocksMovement(t *testing.T) {
	mapGrid, err := BuildMapStaticGrid(2000, 2000, nil)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	structGrid, err := BuildStructureGrid(2000, 2000, []StructureAABB{
		{StructureID: 1, PartID: 1, MinX: 190, MinY: 190, MaxX: 210, MaxY: 210, RenderKind: "wood_wall"},
	}, 0)
	if err != nil {
		t.Fatalf("BuildStructureGrid: %v", err)
	}
	w := NewCollisionWorld(2000, 2000, mapGrid, structGrid)

	res := w.MoveCircle(150, 200, 1, 0, 64, &MoveScratch{})
	if res.X != 186 {
		t.Errorf("expected structure collider to block movement, stopped at X=%d, want 186", res.X)
	}
}

func mustBuildEmptyMap(t *testing.T, w, h int32) *MapStaticGrid {
	t.Helper()
	g, err := BuildMapStaticGrid(w, h, nil)
	if err != nil {
		t.Fatalf("BuildMapStaticGrid: %v", err)
	}
	return g
}

func TestFindNearestFree_OriginAlreadyFree(t *testing.T) {
	w := newTestWorld(t, nil)
	x, y, ok := w.FindNearestFree(500, 500, PlayerRadius, 128, &MoveScratch{})
	if !ok || x != 500 || y != 500 {
		t.Errorf("got (%d,%d,%v), want (500,500,true)", x, y, ok)
	}
}

func TestFindNearestFree_RelocatesWhenOriginOccupied(t *testing.T) {
	wall := MapAABB{ID: 1, MinX: 480, MinY: 480, MaxX: 520, MaxY: 520, RenderKind: "stone_wall"}
	w := newTestWorld(t, []MapAABB{wall})

	x, y, ok := w.FindNearestFree(500, 500, PlayerRadius, 128, &MoveScratch{})
	if !ok {
		t.Fatal("expected a free position to be found")
	}
	if CircleOverlapsAABB(int32(x), int32(y), PlayerRadius, wall.box()) {
		t.Errorf("relocated position (%d,%d) still overlaps the wall", x, y)
	}
	// Should be very close to the wall (small ring radius).
	dist := abs32(int32(x)-500) + abs32(int32(y)-500)
	if dist > 60 {
		t.Errorf("relocated too far from origin: (%d,%d), manhattan dist=%d", x, y, dist)
	}
}

func TestFindNearestFree_NoFreePositionWithinLimitFails(t *testing.T) {
	// A wall big enough to cover the whole search radius around the origin.
	wall := MapAABB{ID: 1, MinX: 100, MinY: 100, MaxX: 900, MaxY: 900, RenderKind: "stone_wall"}
	w := newTestWorld(t, []MapAABB{wall})

	_, _, ok := w.FindNearestFree(500, 500, PlayerRadius, 128, &MoveScratch{})
	if ok {
		t.Error("expected no free position to be found inside a huge wall")
	}
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
