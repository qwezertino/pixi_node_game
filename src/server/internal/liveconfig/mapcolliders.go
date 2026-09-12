package liveconfig

import (
	"context"

	"pixi_game_server/internal/collision"
)

// CampaignMapSnapshot is the loaded, not-yet-validated map metadata and
// collider rows for one campaign.
type CampaignMapSnapshot struct {
	CampaignID       uint64
	GeneratorVersion string
	Colliders        []collision.MapAABB
}

func loadCampaignMapSnapshot(ctx context.Context, db dbQuerier, campaignID uint64) (CampaignMapSnapshot, error) {
	snapshot := CampaignMapSnapshot{CampaignID: campaignID}

	err := db.QueryRow(ctx,
		`SELECT generator_version FROM campaigns WHERE campaign_id = $1`,
		campaignID,
	).Scan(&snapshot.GeneratorVersion)
	if err != nil {
		return CampaignMapSnapshot{}, err
	}

	rows, err := db.Query(ctx, `
		SELECT collider_id, min_x, min_y, max_x, max_y, render_kind
		FROM campaign_map_colliders
		WHERE campaign_id = $1 AND enabled = TRUE
		ORDER BY collider_id
	`, campaignID)
	if err != nil {
		return CampaignMapSnapshot{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var c collision.MapAABB
		if err := rows.Scan(&c.ID, &c.MinX, &c.MinY, &c.MaxX, &c.MaxY, &c.RenderKind); err != nil {
			return CampaignMapSnapshot{}, err
		}
		snapshot.Colliders = append(snapshot.Colliders, c)
	}
	if err := rows.Err(); err != nil {
		return CampaignMapSnapshot{}, err
	}

	return snapshot, nil
}

// LoadCampaignMapSnapshot loads the active campaign's generator metadata and
// enabled map colliders, ordered by collider_id, so that Postgres row order
// never affects the resulting MapStaticGrid or canonical map version.
func (s *Store) LoadCampaignMapSnapshot(ctx context.Context, campaignID uint64) (CampaignMapSnapshot, error) {
	return loadCampaignMapSnapshot(ctx, s.pool, campaignID)
}
