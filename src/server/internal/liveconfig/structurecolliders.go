package liveconfig

import (
	"context"

	"pixi_game_server/internal/collision"
)

// StructureSnapshot is the restored solid structure geometry and the
// mutation revision it was published at.
type StructureSnapshot struct {
	CampaignID uint64
	Revision   uint64
	Colliders  []collision.StructureAABB
}

func loadStructureSnapshot(ctx context.Context, db dbQuerier, campaignID uint64) (StructureSnapshot, error) {
	snapshot := StructureSnapshot{CampaignID: campaignID}

	var revision int64
	err := db.QueryRow(ctx,
		`SELECT revision FROM structure_collision_state WHERE campaign_id = $1`,
		campaignID,
	).Scan(&revision)
	if err != nil {
		return StructureSnapshot{}, err
	}
	snapshot.Revision = uint64(revision)

	rows, err := db.Query(ctx, `
		SELECT structure_id, part_id, min_x, min_y, max_x, max_y, render_kind
		FROM structure_colliders
		WHERE campaign_id = $1 AND solid = TRUE
		ORDER BY structure_id, part_id
	`, campaignID)
	if err != nil {
		return StructureSnapshot{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var c collision.StructureAABB
		if err := rows.Scan(&c.StructureID, &c.PartID, &c.MinX, &c.MinY, &c.MaxX, &c.MaxY, &c.RenderKind); err != nil {
			return StructureSnapshot{}, err
		}
		snapshot.Colliders = append(snapshot.Colliders, c)
	}
	if err := rows.Err(); err != nil {
		return StructureSnapshot{}, err
	}

	return snapshot, nil
}

// LoadStructureSnapshot restores every solid structure collider of the
// active campaign, ordered by (structure_id, part_id), plus the mutation
// revision it was last published at.
func (s *Store) LoadStructureSnapshot(ctx context.Context, campaignID uint64) (StructureSnapshot, error) {
	return loadStructureSnapshot(ctx, s.pool, campaignID)
}
