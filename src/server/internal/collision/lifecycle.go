package collision

// StructureLifecycleState mirrors a structure's gameplay construction/combat
// state as far as collision cares: whether its collider should be solid.
// See docs/collisions_plan.md, "Collision lifecycle постройки".
type StructureLifecycleState uint8

const (
	StructureBlueprint StructureLifecycleState = iota
	StructureUnderConstruction
	StructureCompletionPendingClearance
	StructureCompleted
	StructureDamaged
	StructureDestroyedFoundation
	StructureRebuilding
)

// IsSolid reports whether a structure in this lifecycle state should be a
// solid StructureGrid collider. CompletionPendingClearance is intentionally
// not solid: activation is deferred until FootprintClear passes, so a
// defender standing in the footprint is never displaced or clipped.
func (s StructureLifecycleState) IsSolid() bool {
	switch s {
	case StructureCompleted, StructureDamaged:
		return true
	default:
		return false
	}
}

func unionBounds(colliders []StructureAABB) AABB {
	b := AABB{MinX: colliders[0].MinX, MinY: colliders[0].MinY, MaxX: colliders[0].MaxX, MaxY: colliders[0].MaxY}
	for _, c := range colliders[1:] {
		b.MinX = min32(b.MinX, c.MinX)
		b.MinY = min32(b.MinY, c.MinY)
		b.MaxX = max32(b.MaxX, c.MaxX)
		b.MaxY = max32(b.MaxY, c.MaxY)
	}
	return b
}

// FootprintClear reports whether no dynamic body (player) occupies the
// union AABB of colliders, using the DynamicGrid's current front snapshot
// (the completed post-movement positions from the end of the previous
// tick). A structure transitioning to solid must pass this check first;
// composite structures check the union of every new part in one call so
// only the free half of a building is never activated alone.
//
// If no DynamicGrid is attached, the footprint is treated as clear (no
// players exist to occupy it).
func (w *CollisionWorld) FootprintClear(colliders []StructureAABB, scratch *QueryScratch) bool {
	if len(colliders) == 0 || w.dynamic == nil {
		return true
	}
	bounds := unionBounds(colliders)
	var dst [1]uint32
	occupants := w.dynamic.QueryAABB(bounds, dst[:0], scratch)
	return len(occupants) == 0
}
