package server

import (
	"encoding/json"

	"pixi_game_server/internal/collision"
)

type mapColliderView struct {
	ColliderID uint64 `json:"colliderId"`
	MinX       int32  `json:"minX"`
	MinY       int32  `json:"minY"`
	MaxX       int32  `json:"maxX"`
	MaxY       int32  `json:"maxY"`
	RenderKind string `json:"renderKind"`
}

type mapCollidersView struct {
	CampaignID       uint64            `json:"campaignId"`
	GeneratorVersion string            `json:"generatorVersion"`
	MapVersion       string            `json:"mapVersion"`
	CellSize         int32             `json:"cellSize"`
	PlayerRadius     int32             `json:"playerRadius"`
	Colliders        []mapColliderView `json:"colliders"`
}

// BuildMapCollidersJSON marshals the immutable /api/map-colliders payload
// described in docs/collisions_plan.md. colliders must already be validated
// and ordered by collider ID (the same order the server's MapStaticGrid was
// built from).
func BuildMapCollidersJSON(campaignID uint64, generatorVersion, mapVersion string, colliders []collision.MapAABB) ([]byte, error) {
	view := mapCollidersView{
		CampaignID:       campaignID,
		GeneratorVersion: generatorVersion,
		MapVersion:       mapVersion,
		CellSize:         collision.GridCellSize,
		PlayerRadius:     collision.PlayerRadius,
		Colliders:        make([]mapColliderView, len(colliders)),
	}
	for i, c := range colliders {
		view.Colliders[i] = mapColliderView{
			ColliderID: c.ID,
			MinX:       c.MinX,
			MinY:       c.MinY,
			MaxX:       c.MaxX,
			MaxY:       c.MaxY,
			RenderKind: c.RenderKind,
		}
	}
	return json.Marshal(view)
}
