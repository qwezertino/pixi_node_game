package collision

// gridDims computes column/row counts for a world of the given size using
// integer ceiling division, per the plan's addressing rules.
func gridDims(worldWidth, worldHeight, cellSize int32) (columns, rows int32) {
	columns = (worldWidth + cellSize - 1) / cellSize
	rows = (worldHeight + cellSize - 1) / cellSize
	return
}

func cellCoord(v, cellSize, count int32) int32 {
	return clampInt32(v/cellSize, 0, count-1)
}

// cellRange returns the inclusive [minCell, maxCell] range covering bounds
// on one axis, clamped to [0, count-1].
func cellRange(minV, maxV, cellSize, count int32) (minCell, maxCell int32) {
	minCell = cellCoord(minV, cellSize, count)
	maxCell = cellCoord(maxV, cellSize, count)
	return
}

func flatIndex(cellX, cellY, columns int32) uint32 {
	return uint32(cellY*columns + cellX)
}

// QueryScratch is a reusable, per-worker buffer for MapStaticGrid and
// StructureGrid queries. It must not be shared across goroutines and its
// returned slices are only valid until the next call using the same
// scratch.
type QueryScratch struct {
	result         []uint32
	seenGeneration []uint32
	generation     uint32
}

func (s *QueryScratch) ensureGenerationCap(n int) {
	if len(s.seenGeneration) >= n {
		return
	}
	grown := make([]uint32, n)
	copy(grown, s.seenGeneration)
	s.seenGeneration = grown
}

func (s *QueryScratch) nextGeneration() uint32 {
	s.generation++
	if s.generation == 0 {
		for i := range s.seenGeneration {
			s.seenGeneration[i] = 0
		}
		s.generation = 1
	}
	return s.generation
}

// MapAABB is a static map collider loaded once at campaign start.
type MapAABB struct {
	ID         uint64
	MinX, MinY int32
	MaxX, MaxY int32
	RenderKind string
}

func (c MapAABB) box() AABB {
	return AABB{MinX: c.MinX, MinY: c.MinY, MaxX: c.MaxX, MaxY: c.MaxY}
}

// MapStaticGrid is an immutable spatial index over campaign map geometry.
// It is safe for concurrent reads from any number of tick workers.
type MapStaticGrid struct {
	cellSize  int32
	columns   int32
	rows      int32
	buckets   [][]uint32
	colliders []MapAABB
}

func BuildMapStaticGrid(worldWidth, worldHeight int32, colliders []MapAABB) (*MapStaticGrid, error) {
	if len(colliders) > MaxMapColliders {
		return nil, errTooManyColliders(len(colliders), MaxMapColliders)
	}

	columns, rows := gridDims(worldWidth, worldHeight, GridCellSize)
	g := &MapStaticGrid{
		cellSize:  GridCellSize,
		columns:   columns,
		rows:      rows,
		buckets:   make([][]uint32, columns*rows),
		colliders: colliders,
	}

	memberships := 0
	for idx, c := range colliders {
		box := AABB{MinX: c.MinX, MinY: c.MinY, MaxX: c.MaxX, MaxY: c.MaxY}
		if !box.Valid() {
			return nil, errInvalidCollider(c.ID)
		}
		minCX, maxCX := cellRange(c.MinX, c.MaxX, g.cellSize, columns)
		minCY, maxCY := cellRange(c.MinY, c.MaxY, g.cellSize, rows)
		for cy := minCY; cy <= maxCY; cy++ {
			for cx := minCX; cx <= maxCX; cx++ {
				fi := flatIndex(cx, cy, columns)
				g.buckets[fi] = append(g.buckets[fi], uint32(idx))
				memberships++
				if memberships > MaxMapMemberships {
					return nil, errTooManyMemberships(memberships, MaxMapMemberships)
				}
			}
		}
	}

	return g, nil
}

func (g *MapStaticGrid) Columns() int32              { return g.columns }
func (g *MapStaticGrid) Rows() int32                 { return g.rows }
func (g *MapStaticGrid) Collider(idx uint32) MapAABB { return g.colliders[idx] }
func (g *MapStaticGrid) ColliderCount() int          { return len(g.colliders) }

// QueryAABB returns the deduplicated, dense indices of colliders whose cell
// membership intersects bounds, appended into scratch's reusable result
// buffer. The returned slice is only valid until the next query on the same
// scratch.
func (g *MapStaticGrid) QueryAABB(bounds AABB, scratch *QueryScratch) []uint32 {
	scratch.ensureGenerationCap(len(g.colliders))
	gen := scratch.nextGeneration()
	scratch.result = scratch.result[:0]

	minCX, maxCX := cellRange(bounds.MinX, bounds.MaxX, g.cellSize, g.columns)
	minCY, maxCY := cellRange(bounds.MinY, bounds.MaxY, g.cellSize, g.rows)

	for cy := minCY; cy <= maxCY; cy++ {
		for cx := minCX; cx <= maxCX; cx++ {
			fi := flatIndex(cx, cy, g.columns)
			for _, idx := range g.buckets[fi] {
				if scratch.seenGeneration[idx] == gen {
					continue
				}
				scratch.seenGeneration[idx] = gen
				scratch.result = append(scratch.result, idx)
			}
		}
	}
	return scratch.result
}
