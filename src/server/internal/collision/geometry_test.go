package collision

import (
	"math"
	"testing"
)

func TestAABBValid(t *testing.T) {
	cases := []struct {
		name string
		box  AABB
		want bool
	}{
		{"valid", AABB{0, 0, 10, 10}, true},
		{"minX_eq_maxX", AABB{10, 0, 10, 10}, false},
		{"minY_eq_maxY", AABB{0, 10, 10, 10}, false},
		{"minX_gt_maxX", AABB{20, 0, 10, 10}, false},
		{"minimal_thickness", AABB{0, 0, 1, 1}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.box.Valid(); got != c.want {
				t.Errorf("Valid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCircleOverlapsAABB(t *testing.T) {
	box := AABB{MinX: 100, MinY: 100, MaxX: 200, MaxY: 200}
	radius := int32(4)

	cases := []struct {
		name    string
		x, y    int32
		overlap bool
	}{
		{"far_away", 0, 0, false},
		{"center_inside", 150, 150, true},
		{"touching_side_exactly_at_radius", 96, 150, false},
		{"one_unit_penetration", 97, 150, true},
		{"touching_corner_exactly_at_radius", 100 - int32(math.Round(4/math.Sqrt2)), 100 - int32(math.Round(4/math.Sqrt2)), false},
		{"just_outside_corner", 95, 95, false},
		{"penetrating_corner", 98, 98, true},
		{"on_boundary_edge", 100, 150, true},
		{"on_boundary_corner", 100, 100, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CircleOverlapsAABB(c.x, c.y, radius, box); got != c.overlap {
				t.Errorf("CircleOverlapsAABB(%d,%d) = %v, want %v", c.x, c.y, got, c.overlap)
			}
		})
	}
}

func TestCircleOverlapsAABB_NoOverflowAtWorldExtent(t *testing.T) {
	box := AABB{MinX: 2147483000, MinY: 2147483000, MaxX: 2147483600, MaxY: 2147483600}
	if !CircleOverlapsAABB(2147483300, 2147483300, 4, box) {
		t.Errorf("expected overlap for center inside large-coordinate box")
	}
}

func TestClampInt32(t *testing.T) {
	if got := clampInt32(5, 10, 20); got != 10 {
		t.Errorf("clamp below range = %d, want 10", got)
	}
	if got := clampInt32(25, 10, 20); got != 20 {
		t.Errorf("clamp above range = %d, want 20", got)
	}
	if got := clampInt32(15, 10, 20); got != 15 {
		t.Errorf("clamp inside range = %d, want 15", got)
	}
}
