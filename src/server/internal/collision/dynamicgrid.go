package collision

import "sort"

type DynamicBodyKind uint8

const (
	DynamicBodyPlayer DynamicBodyKind = iota
	DynamicBodyMovableObject
)

// DynamicBody is one indexed body in a DynamicGrid snapshot.
type DynamicBody struct {
	ID     uint32
	Kind   DynamicBodyKind
	X, Y   uint16
	Radius uint16
}

// PlayerSnapshot is the minimal per-player input to DynamicGrid.Rebuild. It
// exists so the collision package does not need to depend on the game/types
// player representation.
type PlayerSnapshot struct {
	ID   uint32
	X, Y uint16
}

type dynamicGridBuffer struct {
	buckets      [][]uint32
	bodies       []DynamicBody
	touchedCells []uint32
	maxRadius    int32
}

func newDynamicGridBuffer(columns, rows int32) *dynamicGridBuffer {
	return &dynamicGridBuffer{buckets: make([][]uint32, columns*rows)}
}

// clearTouched clears only the buckets this buffer actually populated last
// time, never the full world, so rebuild cost tracks player count rather
// than map area.
func (b *dynamicGridBuffer) clearTouched() {
	for _, cell := range b.touchedCells {
		b.buckets[cell] = b.buckets[cell][:0]
	}
	b.touchedCells = b.touchedCells[:0]
	b.bodies = b.bodies[:0]
	b.maxRadius = 0
}

// DynamicGrid is a front/back spatial index of dynamic bodies (players
// today, movable objects in the future), rebuilt once per tick after
// movement resolves. MoveCircle never consults it: players are ghost-mode
// and never block each other.
type DynamicGrid struct {
	cellSize int32
	columns  int32
	rows     int32
	buffers  [2]*dynamicGridBuffer
	frontIdx int
}

func NewDynamicGrid(worldWidth, worldHeight int32) *DynamicGrid {
	columns, rows := gridDims(worldWidth, worldHeight, GridCellSize)
	return &DynamicGrid{
		cellSize: GridCellSize,
		columns:  columns,
		rows:     rows,
		buffers:  [2]*dynamicGridBuffer{newDynamicGridBuffer(columns, rows), newDynamicGridBuffer(columns, rows)},
	}
}

func (g *DynamicGrid) frontBuf() *dynamicGridBuffer { return g.buffers[g.frontIdx] }
func (g *DynamicGrid) backBuf() *dynamicGridBuffer  { return g.buffers[1-g.frontIdx] }

// Rebuild clears the back buffer's previously touched cells and repopulates
// it from players, sorted by ID first so the result never depends on the
// caller's iteration order.
func (g *DynamicGrid) Rebuild(players []PlayerSnapshot) {
	back := g.backBuf()
	back.clearTouched()

	sorted := make([]PlayerSnapshot, len(players))
	copy(sorted, players)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	for _, p := range sorted {
		body := DynamicBody{ID: p.ID, Kind: DynamicBodyPlayer, X: p.X, Y: p.Y, Radius: uint16(PlayerRadius)}
		handle := uint32(len(back.bodies))
		back.bodies = append(back.bodies, body)
		if int32(body.Radius) > back.maxRadius {
			back.maxRadius = int32(body.Radius)
		}
		g.insertBody(back, handle, body)
	}
}

func (g *DynamicGrid) insertBody(buf *dynamicGridBuffer, handle uint32, body DynamicBody) {
	r := int32(body.Radius)
	minCX, maxCX := cellRange(int32(body.X)-r, int32(body.X)+r, g.cellSize, g.columns)
	minCY, maxCY := cellRange(int32(body.Y)-r, int32(body.Y)+r, g.cellSize, g.rows)
	for cy := minCY; cy <= maxCY; cy++ {
		for cx := minCX; cx <= maxCX; cx++ {
			fi := flatIndex(cx, cy, g.columns)
			if len(buf.buckets[fi]) == 0 {
				buf.touchedCells = append(buf.touchedCells, fi)
			}
			buf.buckets[fi] = append(buf.buckets[fi], handle)
		}
	}
}

// Swap publishes the just-rebuilt back buffer as the new front. It must
// only be called by the single game-loop goroutine, after every query
// against the previous front has finished for this tick.
func (g *DynamicGrid) Swap() {
	g.frontIdx = 1 - g.frontIdx
}

func (g *DynamicGrid) queryBuffer(buf *dynamicGridBuffer, bounds AABB, scratch *QueryScratch) []uint32 {
	scratch.ensureGenerationCap(len(buf.bodies))
	gen := scratch.nextGeneration()
	scratch.result = scratch.result[:0]

	minCX, maxCX := cellRange(bounds.MinX, bounds.MaxX, g.cellSize, g.columns)
	minCY, maxCY := cellRange(bounds.MinY, bounds.MaxY, g.cellSize, g.rows)
	for cy := minCY; cy <= maxCY; cy++ {
		for cx := minCX; cx <= maxCX; cx++ {
			fi := flatIndex(cx, cy, g.columns)
			for _, handle := range buf.buckets[fi] {
				if scratch.seenGeneration[handle] == gen {
					continue
				}
				scratch.seenGeneration[handle] = gen
				scratch.result = append(scratch.result, handle)
			}
		}
	}
	return scratch.result
}

func sortIDs(ids []uint32) []uint32 {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// QueryCircle returns, in dst, the ascending-sorted IDs of front-snapshot
// bodies whose circle overlaps a circle of the given radius centered at
// (centerX, centerY). dst and scratch are caller-owned; the returned slice
// aliases dst and is valid until dst or scratch is reused.
func (g *DynamicGrid) QueryCircle(centerX, centerY, radius int32, dst []uint32, scratch *QueryScratch) []uint32 {
	front := g.frontBuf()
	bounds := AABB{MinX: centerX, MinY: centerY, MaxX: centerX, MaxY: centerY}.expand(radius + front.maxRadius)
	candidates := g.queryBuffer(front, bounds, scratch)

	dst = dst[:0]
	for _, handle := range candidates {
		body := front.bodies[handle]
		dx := int64(centerX) - int64(body.X)
		dy := int64(centerY) - int64(body.Y)
		rr := int64(radius) + int64(body.Radius)
		if dx*dx+dy*dy <= rr*rr {
			dst = append(dst, body.ID)
		}
	}
	return sortIDs(dst)
}

// QueryAABB returns, in dst, the ascending-sorted IDs of front-snapshot
// bodies whose circle actually intersects bounds.
func (g *DynamicGrid) QueryAABB(bounds AABB, dst []uint32, scratch *QueryScratch) []uint32 {
	front := g.frontBuf()
	candidates := g.queryBuffer(front, bounds.expand(front.maxRadius), scratch)

	dst = dst[:0]
	for _, handle := range candidates {
		body := front.bodies[handle]
		if CircleOverlapsAABB(int32(body.X), int32(body.Y), int32(body.Radius), bounds) {
			dst = append(dst, body.ID)
		}
	}
	return sortIDs(dst)
}

// QuerySegment is a broad-phase-only query for future projectiles/melee: it
// returns, in dst, the ascending-sorted, deduplicated IDs of front-snapshot
// bodies whose AABB touches the segment's bounding box expanded by radius.
// Exact segment-vs-circle hit testing and target selection belong to the
// future combat phase, not the grid.
func (g *DynamicGrid) QuerySegment(startX, startY, endX, endY, radius int32, dst []uint32, scratch *QueryScratch) []uint32 {
	front := g.frontBuf()
	bounds := AABB{
		MinX: min32(startX, endX),
		MinY: min32(startY, endY),
		MaxX: max32(startX, endX),
		MaxY: max32(startY, endY),
	}.expand(radius + front.maxRadius)
	candidates := g.queryBuffer(front, bounds, scratch)

	dst = dst[:0]
	for _, handle := range candidates {
		dst = append(dst, front.bodies[handle].ID)
	}
	return sortIDs(dst)
}
