package collision

import "fmt"

func errTooManyColliders(got, max int) error {
	return fmt.Errorf("collision: %d colliders exceeds limit of %d", got, max)
}

func errTooManyMemberships(got, max int) error {
	return fmt.Errorf("collision: %d collider-cell memberships exceeds limit of %d", got, max)
}

func errInvalidCollider(id uint64) error {
	return fmt.Errorf("collision: invalid AABB for collider %d: minX must be < maxX and minY must be < maxY with minimum thickness of 1 world unit", id)
}

func errTooManyStructureColliders(got, max int) error {
	return fmt.Errorf("collision: %d structure colliders exceeds limit of %d", got, max)
}

func errTooManyStructureMemberships(got, max int) error {
	return fmt.Errorf("collision: %d structure collider-cell memberships exceeds limit of %d", got, max)
}

func errInvalidStructureCollider(structureID uint64, partID uint32) error {
	return fmt.Errorf("collision: invalid AABB for structure collider (%d,%d): minX must be < maxX and minY must be < maxY with minimum thickness of 1 world unit", structureID, partID)
}

func errUnknownStructureCollider(key StructureColliderKey) error {
	return fmt.Errorf("collision: structure mutation references unknown collider (%d,%d)", key.StructureID, key.PartID)
}
