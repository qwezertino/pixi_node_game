package collision

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
)

// KnownRenderKinds is the set of render kinds the client registry can
// display. An unknown kind is a startup error: physics must never load
// without a matching client-side representation.
var KnownRenderKinds = map[string]bool{
	"stone_wall": true,
}

// ValidateMapColliders checks bounds, render kind and world containment for
// a campaign map snapshot before it is built into a MapStaticGrid.
func ValidateMapColliders(worldWidth, worldHeight int32, colliders []MapAABB) error {
	for _, c := range colliders {
		box := AABB{MinX: c.MinX, MinY: c.MinY, MaxX: c.MaxX, MaxY: c.MaxY}
		if !box.Valid() {
			return errInvalidCollider(c.ID)
		}
		if c.MinX < 0 || c.MinY < 0 || c.MaxX > worldWidth || c.MaxY > worldHeight {
			return fmt.Errorf("collision: map collider %d out of world bounds", c.ID)
		}
		if c.RenderKind == "" || !KnownRenderKinds[c.RenderKind] {
			return fmt.Errorf("collision: map collider %d has unknown render_kind %q", c.ID, c.RenderKind)
		}
	}
	return nil
}

// ComputeMapVersion hashes the canonical binary sequence
// campaignId,generatorVersion,colliderId,minX,minY,maxX,maxY,renderKind for
// every collider, sorted by collider ID, so that Postgres row order never
// affects the resulting version.
func ComputeMapVersion(campaignID uint64, generatorVersion string, colliders []MapAABB) string {
	sorted := make([]MapAABB, len(colliders))
	copy(sorted, colliders)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], campaignID)
	h.Write(buf[:])
	h.Write([]byte(generatorVersion))

	for _, c := range sorted {
		binary.BigEndian.PutUint64(buf[:], c.ID)
		h.Write(buf[:])
		writeInt32(h, c.MinX)
		writeInt32(h, c.MinY)
		writeInt32(h, c.MaxX)
		writeInt32(h, c.MaxY)
		h.Write([]byte(c.RenderKind))
	}

	return "sha256-" + hex.EncodeToString(h.Sum(nil))
}

func writeInt32(h interface{ Write([]byte) (int, error) }, v int32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(v))
	h.Write(buf[:])
}
