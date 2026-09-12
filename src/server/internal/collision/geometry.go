// Package collision implements server-authoritative movement collision
// against static map geometry and event-driven structures, using an
// integer-only, fixed-tick spatial grid model.
package collision

const (
	PlayerRadius            int32 = 4
	GridCellSize            int32 = 64
	MaxMoveSteps            int32 = 64
	MaxMapColliders               = 100_000
	MaxMapMemberships             = 2_000_000
	MaxStructureColliders         = 100_000
	MaxStructureMemberships       = 2_000_000
)

// AABB is an axis-aligned bounding box in integer world units.
// The interior is solid; a point on the boundary is inside.
type AABB struct {
	MinX, MinY int32
	MaxX, MaxY int32
}

func (b AABB) Valid() bool {
	return b.MinX < b.MaxX && b.MinY < b.MaxY
}

// CircleOverlapsAABB reports whether a circle centered at (x, y) with the
// given radius overlaps box. Touching the boundary counts as contact
// (overlap), matching the plan's "distanceSquared < radiusSquared is a
// penetration; equality is contact" rule inverted for the free/blocked
// check used by the solver: callers block movement when this returns true.
func CircleOverlapsAABB(x, y, radius int32, box AABB) bool {
	closestX := clampInt32(x, box.MinX, box.MaxX)
	closestY := clampInt32(y, box.MinY, box.MaxY)
	dx := int64(x - closestX)
	dy := int64(y - closestY)
	r := int64(radius)
	return dx*dx+dy*dy < r*r
}

func clampInt32(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
