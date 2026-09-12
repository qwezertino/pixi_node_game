package collision

import (
	"fmt"
	"sort"
)

// StructureAABB is one solid collider part of a runtime structure (wall
// section, gate, tower, mine...). Composite key (StructureID, PartID)
// stably identifies the part across its lifetime.
type StructureAABB struct {
	StructureID uint64
	PartID      uint32
	MinX, MinY  int32
	MaxX, MaxY  int32
	RenderKind  string
}

func (c StructureAABB) box() AABB {
	return AABB{MinX: c.MinX, MinY: c.MinY, MaxX: c.MaxX, MaxY: c.MaxY}
}

// StructureColliderKey is the stable identity of one structure collider
// part, independent of its dense grid handle.
type StructureColliderKey struct {
	StructureID uint64
	PartID      uint32
}

func keyOf(c StructureAABB) StructureColliderKey {
	return StructureColliderKey{StructureID: c.StructureID, PartID: c.PartID}
}

type StructureMutationOperation int

const (
	StructureOpUpsert StructureMutationOperation = iota
	StructureOpRemove
	StructureOpSetSolid
)

// StructureMutation is one change to apply to a StructureGrid. Collider is
// required (and must match Key) for StructureOpUpsert and for
// StructureOpSetSolid with Solid=true; it is ignored otherwise.
type StructureMutation struct {
	Operation StructureMutationOperation
	Key       StructureColliderKey
	Collider  StructureAABB
	Solid     bool
}

// StructureMutationBatch is one atomically-applied set of structure changes,
// gated by a monotonically increasing revision.
type StructureMutationBatch struct {
	Revision      uint64
	EffectiveTick uint64
	Mutations     []StructureMutation
}

// StructureGrid indexes the solid, runtime-mutable geometry of structures
// (walls, gates, towers, mines...). It is only mutated by the game-loop
// goroutine in a dedicated mutation phase while no movement workers are
// running, so QueryAABB needs no mutex/atomic.
type StructureGrid struct {
	cellSize         int32
	columns          int32
	rows             int32
	revision         uint64
	buckets          [][]uint32
	colliders        []StructureAABB
	lookup           map[StructureColliderKey]uint32
	freeList         []uint32
	totalMemberships int
}

func BuildStructureGrid(worldWidth, worldHeight int32, colliders []StructureAABB, revision uint64) (*StructureGrid, error) {
	if len(colliders) > MaxStructureColliders {
		return nil, errTooManyStructureColliders(len(colliders), MaxStructureColliders)
	}

	columns, rows := gridDims(worldWidth, worldHeight, GridCellSize)
	g := &StructureGrid{
		cellSize:  GridCellSize,
		columns:   columns,
		rows:      rows,
		revision:  revision,
		buckets:   make([][]uint32, columns*rows),
		colliders: make([]StructureAABB, 0, len(colliders)),
		lookup:    make(map[StructureColliderKey]uint32, len(colliders)),
	}

	memberships := 0
	for _, c := range colliders {
		if !c.box().Valid() {
			return nil, errInvalidStructureCollider(c.StructureID, c.PartID)
		}
		key := keyOf(c)
		if _, exists := g.lookup[key]; exists {
			return nil, fmt.Errorf("collision: duplicate structure collider key (%d,%d)", c.StructureID, c.PartID)
		}
		handle := uint32(len(g.colliders))
		g.colliders = append(g.colliders, c)
		g.lookup[key] = handle

		n, err := g.insertIntoBuckets(handle, c)
		if err != nil {
			return nil, err
		}
		memberships += n
		if memberships > MaxStructureMemberships {
			return nil, errTooManyStructureMemberships(memberships, MaxStructureMemberships)
		}
	}
	g.totalMemberships = memberships

	return g, nil
}

// spanCount returns the number of cells c's AABB touches, i.e. the number
// of collider-cell memberships it contributes.
func (g *StructureGrid) spanCount(c StructureAABB) int {
	minCX, maxCX, minCY, maxCY := g.cellsOf(c)
	return int(maxCX-minCX+1) * int(maxCY-minCY+1)
}

func (g *StructureGrid) Revision() uint64 { return g.revision }
func (g *StructureGrid) Columns() int32   { return g.columns }
func (g *StructureGrid) Rows() int32      { return g.rows }

func (g *StructureGrid) Collider(handle uint32) StructureAABB { return g.colliders[handle] }

// Lookup returns the current handle for key, if it is presently solid and
// indexed in the grid.
func (g *StructureGrid) Lookup(key StructureColliderKey) (uint32, bool) {
	h, ok := g.lookup[key]
	return h, ok
}

// Colliders appends every currently active (solid) collider into dst,
// sorted by (StructureID, PartID), e.g. for building a full
// structure_collision_snapshot.
func (g *StructureGrid) Colliders(dst []StructureAABB) []StructureAABB {
	for _, handle := range g.lookup {
		dst = append(dst, g.colliders[handle])
	}
	sort.Slice(dst, func(i, j int) bool { return keyLess(keyOf(dst[i]), keyOf(dst[j])) })
	return dst
}

func (g *StructureGrid) cellsOf(c StructureAABB) (minCX, maxCX, minCY, maxCY int32) {
	minCX, maxCX = cellRange(c.MinX, c.MaxX, g.cellSize, g.columns)
	minCY, maxCY = cellRange(c.MinY, c.MaxY, g.cellSize, g.rows)
	return
}

// insertIntoBuckets adds handle to every cell its AABB touches, keeping each
// bucket sorted by (structureId, partId) for a deterministic, repeatable
// traversal order. It returns the number of memberships added.
func (g *StructureGrid) insertIntoBuckets(handle uint32, c StructureAABB) (int, error) {
	minCX, maxCX, minCY, maxCY := g.cellsOf(c)
	key := keyOf(c)
	n := 0
	for cy := minCY; cy <= maxCY; cy++ {
		for cx := minCX; cx <= maxCX; cx++ {
			fi := flatIndex(cx, cy, g.columns)
			g.buckets[fi] = insertSorted(g.buckets[fi], handle, key, g.colliders)
			n++
		}
	}
	return n, nil
}

func (g *StructureGrid) removeFromBuckets(handle uint32, c StructureAABB) {
	minCX, maxCX, minCY, maxCY := g.cellsOf(c)
	key := keyOf(c)
	for cy := minCY; cy <= maxCY; cy++ {
		for cx := minCX; cx <= maxCX; cx++ {
			fi := flatIndex(cx, cy, g.columns)
			g.buckets[fi] = removeSorted(g.buckets[fi], key, g.colliders)
		}
	}
}

func insertSorted(bucket []uint32, handle uint32, key StructureColliderKey, colliders []StructureAABB) []uint32 {
	i := sort.Search(len(bucket), func(i int) bool { return !keyLess(keyOf(colliders[bucket[i]]), key) })
	bucket = append(bucket, 0)
	copy(bucket[i+1:], bucket[i:])
	bucket[i] = handle
	return bucket
}

func removeSorted(bucket []uint32, key StructureColliderKey, colliders []StructureAABB) []uint32 {
	i := sort.Search(len(bucket), func(i int) bool { return !keyLess(keyOf(colliders[bucket[i]]), key) })
	if i < len(bucket) && keyOf(colliders[bucket[i]]) == key {
		bucket = append(bucket[:i], bucket[i+1:]...)
	}
	return bucket
}

func keyLess(a, b StructureColliderKey) bool {
	if a.StructureID != b.StructureID {
		return a.StructureID < b.StructureID
	}
	return a.PartID < b.PartID
}

// QueryAABB returns the deduplicated, dense handles of solid structure
// colliders whose cell membership intersects bounds, appended into
// scratch's reusable result buffer. The returned slice is only valid until
// the next query on the same scratch.
func (g *StructureGrid) QueryAABB(bounds AABB, scratch *QueryScratch) []uint32 {
	scratch.ensureGenerationCap(cap(g.colliders))
	gen := scratch.nextGeneration()
	scratch.result = scratch.result[:0]

	minCX, maxCX := cellRange(bounds.MinX, bounds.MaxX, g.cellSize, g.columns)
	minCY, maxCY := cellRange(bounds.MinY, bounds.MaxY, g.cellSize, g.rows)

	for cy := minCY; cy <= maxCY; cy++ {
		for cx := minCX; cx <= maxCX; cx++ {
			fi := flatIndex(cx, cy, g.columns)
			for _, handle := range g.buckets[fi] {
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

// ApplyMutationBatch validates the whole batch (bounds, limits, and that
// Revision is exactly the next one) before applying any operation, so a
// rejected batch never leaves partially-applied state. It must only be
// called from the game-loop goroutine, outside any concurrent movement
// query.
func (g *StructureGrid) ApplyMutationBatch(batch StructureMutationBatch) error {
	if batch.Revision != g.revision+1 {
		return fmt.Errorf("collision: structure mutation batch revision %d is not the expected next revision %d",
			batch.Revision, g.revision+1)
	}

	ordered := make([]StructureMutation, len(batch.Mutations))
	copy(ordered, batch.Mutations)
	sort.SliceStable(ordered, func(i, j int) bool { return keyLess(ordered[i].Key, ordered[j].Key) })

	finalMemberships, err := g.validateBatch(ordered)
	if err != nil {
		return err
	}

	for _, m := range ordered {
		g.applyMutation(m)
	}
	g.revision = batch.Revision
	g.totalMemberships = finalMemberships
	return nil
}

// validateBatch simulates the whole ordered batch against a snapshot of
// current keys (without mutating the grid) so that bounds, unknown-key and
// limit errors are all caught before anything is applied. It returns the
// resulting total collider-cell membership count on success.
func (g *StructureGrid) validateBatch(ordered []StructureMutation) (int, error) {
	sim := make(map[StructureColliderKey]*StructureAABB, len(ordered))
	activeCount := len(g.lookup)
	total := g.totalMemberships

	current := func(key StructureColliderKey) (StructureAABB, bool) {
		if c, touched := sim[key]; touched {
			if c == nil {
				return StructureAABB{}, false
			}
			return *c, true
		}
		if handle, exists := g.lookup[key]; exists {
			return g.colliders[handle], true
		}
		return StructureAABB{}, false
	}

	activate := func(m StructureMutation) error {
		if keyOf(m.Collider) != m.Key {
			return fmt.Errorf("collision: mutation collider (%d,%d) does not match key (%d,%d)",
				m.Collider.StructureID, m.Collider.PartID, m.Key.StructureID, m.Key.PartID)
		}
		if !m.Collider.box().Valid() {
			return errInvalidStructureCollider(m.Key.StructureID, m.Key.PartID)
		}
		if old, existed := current(m.Key); existed {
			total -= g.spanCount(old)
		} else {
			activeCount++
		}
		total += g.spanCount(m.Collider)
		collider := m.Collider
		sim[m.Key] = &collider
		return nil
	}

	deactivate := func(key StructureColliderKey) error {
		old, existed := current(key)
		if !existed {
			return errUnknownStructureCollider(key)
		}
		total -= g.spanCount(old)
		activeCount--
		sim[key] = nil
		return nil
	}

	for _, m := range ordered {
		var err error
		switch m.Operation {
		case StructureOpUpsert:
			err = activate(m)
		case StructureOpSetSolid:
			if m.Solid {
				err = activate(m)
			} else {
				err = deactivate(m.Key)
			}
		case StructureOpRemove:
			err = deactivate(m.Key)
		default:
			err = fmt.Errorf("collision: unknown structure mutation operation %d", m.Operation)
		}
		if err != nil {
			return 0, err
		}
	}

	if activeCount > MaxStructureColliders {
		return 0, errTooManyStructureColliders(activeCount, MaxStructureColliders)
	}
	if total > MaxStructureMemberships {
		return 0, errTooManyStructureMemberships(total, MaxStructureMemberships)
	}
	return total, nil
}

func (g *StructureGrid) applyMutation(m StructureMutation) {
	switch m.Operation {
	case StructureOpUpsert:
		g.upsert(m.Key, m.Collider)
	case StructureOpSetSolid:
		if m.Solid {
			g.upsert(m.Key, m.Collider)
		} else {
			g.remove(m.Key)
		}
	case StructureOpRemove:
		g.remove(m.Key)
	}
}

func (g *StructureGrid) upsert(key StructureColliderKey, c StructureAABB) {
	if handle, exists := g.lookup[key]; exists {
		old := g.colliders[handle]
		g.removeFromBuckets(handle, old)
		g.colliders[handle] = c
		g.insertIntoBuckets(handle, c)
		return
	}

	var handle uint32
	if n := len(g.freeList); n > 0 {
		handle = g.freeList[n-1]
		g.freeList = g.freeList[:n-1]
		g.colliders[handle] = c
	} else {
		handle = uint32(len(g.colliders))
		g.colliders = append(g.colliders, c)
	}
	g.lookup[key] = handle
	g.insertIntoBuckets(handle, c)
}

func (g *StructureGrid) remove(key StructureColliderKey) {
	handle, exists := g.lookup[key]
	if !exists {
		return
	}
	g.removeFromBuckets(handle, g.colliders[handle])
	delete(g.lookup, key)
	g.freeList = append(g.freeList, handle)
}
