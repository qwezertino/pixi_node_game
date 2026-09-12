package collision

import "testing"

func TestValidateMapColliders_RejectsOutOfWorldBounds(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: 0, MinY: 0, MaxX: 100, MaxY: 100, RenderKind: "stone_wall"}}
	if err := ValidateMapColliders(50, 50, colliders); err == nil {
		t.Error("expected error for collider exceeding world bounds")
	}
}

func TestValidateMapColliders_RejectsNegativeCoordinates(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: -10, MinY: 0, MaxX: 100, MaxY: 100, RenderKind: "stone_wall"}}
	if err := ValidateMapColliders(1000, 1000, colliders); err == nil {
		t.Error("expected error for negative coordinate")
	}
}

func TestValidateMapColliders_RejectsUnknownRenderKind(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10, RenderKind: "nonexistent_kind"}}
	if err := ValidateMapColliders(1000, 1000, colliders); err == nil {
		t.Error("expected error for unknown render kind")
	}
}

func TestValidateMapColliders_RejectsEmptyRenderKind(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10, RenderKind: ""}}
	if err := ValidateMapColliders(1000, 1000, colliders); err == nil {
		t.Error("expected error for empty render kind")
	}
}

func TestValidateMapColliders_RejectsDegenerateAABB(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: 10, MinY: 0, MaxX: 10, MaxY: 10, RenderKind: "stone_wall"}}
	if err := ValidateMapColliders(1000, 1000, colliders); err == nil {
		t.Error("expected error for degenerate AABB")
	}
}

func TestValidateMapColliders_AcceptsValidSnapshot(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10, RenderKind: "stone_wall"}}
	if err := ValidateMapColliders(1000, 1000, colliders); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestComputeMapVersion_StableAcrossInputOrder(t *testing.T) {
	a := []MapAABB{
		{ID: 1, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10, RenderKind: "stone_wall"},
		{ID: 2, MinX: 20, MinY: 20, MaxX: 30, MaxY: 30, RenderKind: "stone_wall"},
	}
	b := []MapAABB{a[1], a[0]}

	va := ComputeMapVersion(1, "mapgen-v1", a)
	vb := ComputeMapVersion(1, "mapgen-v1", b)
	if va != vb {
		t.Errorf("map version differs by input order: %s vs %s", va, vb)
	}
}

func TestComputeMapVersion_ChangesWithGeometry(t *testing.T) {
	a := []MapAABB{{ID: 1, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10, RenderKind: "stone_wall"}}
	b := []MapAABB{{ID: 1, MinX: 0, MinY: 0, MaxX: 11, MaxY: 10, RenderKind: "stone_wall"}}

	if ComputeMapVersion(1, "mapgen-v1", a) == ComputeMapVersion(1, "mapgen-v1", b) {
		t.Error("expected different map version for different geometry")
	}
}

func TestComputeMapVersion_ChangesWithCampaignID(t *testing.T) {
	colliders := []MapAABB{{ID: 1, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10, RenderKind: "stone_wall"}}
	if ComputeMapVersion(1, "mapgen-v1", colliders) == ComputeMapVersion(2, "mapgen-v1", colliders) {
		t.Error("expected different map version for different campaign ID")
	}
}

func TestComputeMapVersion_HasSha256Prefix(t *testing.T) {
	v := ComputeMapVersion(1, "mapgen-v1", nil)
	if len(v) < 7 || v[:7] != "sha256-" {
		t.Errorf("expected sha256- prefix, got %s", v)
	}
}
