package collision

// CollisionWorld ties together the map, structure and (eventually) dynamic
// layers that share one spatial addressing scheme. MoveCircle intentionally
// reads only the map and structure layers: players are ghost-mode with
// respect to movement and never block each other.
type CollisionWorld struct {
	mapStatic   *MapStaticGrid
	structures  *StructureGrid
	dynamic     *DynamicGrid
	worldWidth  int32
	worldHeight int32
}

func NewCollisionWorld(worldWidth, worldHeight int32, mapStatic *MapStaticGrid, structures *StructureGrid) *CollisionWorld {
	return &CollisionWorld{
		mapStatic:   mapStatic,
		structures:  structures,
		worldWidth:  worldWidth,
		worldHeight: worldHeight,
	}
}

func (w *CollisionWorld) Structures() *StructureGrid { return w.structures }
func (w *CollisionWorld) MapStatic() *MapStaticGrid  { return w.mapStatic }
func (w *CollisionWorld) Dynamic() *DynamicGrid      { return w.dynamic }

// SetDynamic attaches the DynamicGrid layer. It exists as a setter, rather
// than a constructor argument, because DynamicGrid has no map/structure
// dependency and many CollisionWorld construction sites (tests especially)
// only need MoveCircle, which never reads this layer.
func (w *CollisionWorld) SetDynamic(dynamic *DynamicGrid) {
	w.dynamic = dynamic
}

// MoveScratch is a reusable, per-tick-worker buffer for MoveCircle and
// FindNearestFree. It must not be shared across goroutines.
type MoveScratch struct {
	mapScratch       QueryScratch
	structureScratch QueryScratch
}

type MoveResult struct {
	X, Y                    uint16
	EffectiveDX             int8
	EffectiveDY             int8
	BlockedX                bool
	BlockedY                bool
	MapCandidateCount       uint32
	StructureCandidateCount uint32
	NarrowPhaseChecks       uint32
}

func (b AABB) expand(r int32) AABB {
	return AABB{MinX: b.MinX - r, MinY: b.MinY - r, MaxX: b.MaxX + r, MaxY: b.MaxY + r}
}

// MoveCircle resolves movement of a PlayerRadius circle from (x, y) along
// (dx, dy) for distance world units, using discrete swept movement:
// one broad-phase query per call against the full path, then a fixed-cost
// integer narrow phase repeated for each 1-world-unit micro-step. See
// docs/collisions_plan.md, "Anti-tunneling и sliding algorithm".
//
// distance must be in [0, MaxMoveSteps]; distance > MaxMoveSteps is a
// caller-side invariant violation (bad unit/config validation) and is
// treated as "no movement this call" rather than clamped, per the plan.
// distance < 0 is a programmer error and panics.
func (w *CollisionWorld) MoveCircle(x, y uint16, dx, dy int8, distance int32, scratch *MoveScratch) MoveResult {
	if distance < 0 {
		panic("collision: MoveCircle distance must be >= 0")
	}
	if distance > MaxMoveSteps {
		return MoveResult{X: x, Y: y, BlockedX: dx != 0, BlockedY: dy != 0}
	}

	startX, startY := int32(x), int32(y)
	intendedX := startX + int32(dx)*distance
	intendedY := startY + int32(dy)*distance

	bounds := AABB{
		MinX: min32(startX, intendedX),
		MinY: min32(startY, intendedY),
		MaxX: max32(startX, intendedX),
		MaxY: max32(startY, intendedY),
	}.expand(PlayerRadius)

	mapCandidates := w.mapStatic.QueryAABB(bounds, &scratch.mapScratch)
	structureCandidates := w.structures.QueryAABB(bounds, &scratch.structureScratch)

	minCenterX, maxCenterX := PlayerRadius, w.worldWidth-PlayerRadius
	minCenterY, maxCenterY := PlayerRadius, w.worldHeight-PlayerRadius

	narrowChecks := uint32(0)
	free := func(cx, cy int32) bool {
		if cx < minCenterX || cx > maxCenterX || cy < minCenterY || cy > maxCenterY {
			return false
		}
		for _, idx := range mapCandidates {
			narrowChecks++
			if CircleOverlapsAABB(cx, cy, PlayerRadius, w.mapStatic.Collider(idx).box()) {
				return false
			}
		}
		for _, idx := range structureCandidates {
			narrowChecks++
			if CircleOverlapsAABB(cx, cy, PlayerRadius, w.structures.Collider(idx).box()) {
				return false
			}
		}
		return true
	}

	curX, curY := startX, startY
	lastMovedX, lastMovedY := false, false

	for step := int32(0); step < distance; step++ {
		movedX, movedY := false, false

		if dx != 0 {
			candidateX := curX + int32(dx)
			if free(candidateX, curY) {
				curX = candidateX
				movedX = true
			}
		}
		if dy != 0 {
			candidateY := curY + int32(dy)
			if free(curX, candidateY) {
				curY = candidateY
				movedY = true
			}
		}

		lastMovedX, lastMovedY = movedX, movedY
		if !movedX && !movedY {
			break
		}
	}

	effectiveDX, effectiveDY := int8(0), int8(0)
	if lastMovedX {
		effectiveDX = dx
	}
	if lastMovedY {
		effectiveDY = dy
	}

	return MoveResult{
		X:                       uint16(curX),
		Y:                       uint16(curY),
		EffectiveDX:             effectiveDX,
		EffectiveDY:             effectiveDY,
		BlockedX:                dx != 0 && effectiveDX == 0,
		BlockedY:                dy != 0 && effectiveDY == 0,
		MapCandidateCount:       uint32(len(mapCandidates)),
		StructureCandidateCount: uint32(len(structureCandidates)),
		NarrowPhaseChecks:       narrowChecks,
	}
}

// FindNearestFree performs a deterministic expanding-ring search for the
// nearest integer position (starting at the origin) where a circle of the
// given radius is free of map/structure colliders and inside world bounds.
// It searches radius 1..searchLimit world units around (x, y), walking each
// square ring clockwise starting at its top-left corner.
func (w *CollisionWorld) FindNearestFree(x, y uint16, radius, searchLimit int32, scratch *MoveScratch) (uint16, uint16, bool) {
	startX, startY := int32(x), int32(y)

	bounds := AABB{
		MinX: startX - searchLimit,
		MinY: startY - searchLimit,
		MaxX: startX + searchLimit,
		MaxY: startY + searchLimit,
	}.expand(radius)

	mapCandidates := w.mapStatic.QueryAABB(bounds, &scratch.mapScratch)
	structureCandidates := w.structures.QueryAABB(bounds, &scratch.structureScratch)

	minCenterX, maxCenterX := radius, w.worldWidth-radius
	minCenterY, maxCenterY := radius, w.worldHeight-radius

	free := func(cx, cy int32) bool {
		if cx < minCenterX || cx > maxCenterX || cy < minCenterY || cy > maxCenterY {
			return false
		}
		for _, idx := range mapCandidates {
			if CircleOverlapsAABB(cx, cy, radius, w.mapStatic.Collider(idx).box()) {
				return false
			}
		}
		for _, idx := range structureCandidates {
			if CircleOverlapsAABB(cx, cy, radius, w.structures.Collider(idx).box()) {
				return false
			}
		}
		return true
	}

	if free(startX, startY) {
		return x, y, true
	}

	for r := int32(1); r <= searchLimit; r++ {
		for cx := startX - r; cx <= startX+r; cx++ {
			if free(cx, startY-r) {
				return uint16(cx), uint16(startY - r), true
			}
		}
		for cy := startY - r + 1; cy <= startY+r; cy++ {
			if free(startX+r, cy) {
				return uint16(startX + r), uint16(cy), true
			}
		}
		for cx := startX + r - 1; cx >= startX-r; cx-- {
			if free(cx, startY+r) {
				return uint16(cx), uint16(startY + r), true
			}
		}
		for cy := startY + r - 1; cy >= startY-r+1; cy-- {
			if free(startX-r, cy) {
				return uint16(startX - r), uint16(cy), true
			}
		}
	}

	return 0, 0, false
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
