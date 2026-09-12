package protocol

import (
	"testing"

	"pixi_game_server/internal/collision"
)

func TestStructureCollisionSnapshot_RoundTrip(t *testing.T) {
	bp := &BinaryProtocol{}
	colliders := []collision.StructureAABB{
		{StructureID: 9001, PartID: 1, MinX: 3300, MinY: 700, MaxX: 3340, MaxY: 1800, RenderKind: "stone_wall"},
		{StructureID: 9002, PartID: 1, MinX: 3300, MinY: 1760, MaxX: 4300, MaxY: 1800, RenderKind: "stone_wall"},
		{StructureID: 9003, PartID: 1, MinX: 4200, MinY: 600, MaxX: 4500, MaxY: 900, RenderKind: "stone_wall"},
	}

	data, err := bp.EncodeStructureCollisionSnapshot(1, 3, colliders)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	campaignID, revision, got, err := bp.DecodeStructureCollisionSnapshot(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if campaignID != 1 || revision != 3 {
		t.Fatalf("campaignID=%d revision=%d, want 1,3", campaignID, revision)
	}
	if len(got) != len(colliders) {
		t.Fatalf("got %d colliders, want %d", len(got), len(colliders))
	}
	for i, c := range colliders {
		g := got[i]
		if g.StructureID != c.StructureID || g.PartID != c.PartID ||
			g.MinX != c.MinX || g.MinY != c.MinY || g.MaxX != c.MaxX || g.MaxY != c.MaxY ||
			g.RenderKind != c.RenderKind {
			t.Errorf("collider %d = %+v, want %+v", i, g, c)
		}
	}
}

func TestStructureCollisionSnapshot_EmptyColliders(t *testing.T) {
	bp := &BinaryProtocol{}
	data, err := bp.EncodeStructureCollisionSnapshot(1, 0, nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	_, _, got, err := bp.DecodeStructureCollisionSnapshot(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no colliders, got %v", got)
	}
}

func TestStructureCollisionDelta_RoundTrip_Upsert(t *testing.T) {
	bp := &BinaryProtocol{}
	batch := collision.StructureMutationBatch{
		Revision:      42,
		EffectiveTick: 120044,
		Mutations: []collision.StructureMutation{
			{
				Operation: collision.StructureOpUpsert,
				Key:       collision.StructureColliderKey{StructureID: 9001, PartID: 1},
				Collider:  collision.StructureAABB{StructureID: 9001, PartID: 1, MinX: 100, MinY: 100, MaxX: 200, MaxY: 200, RenderKind: "wood_wall"},
			},
		},
	}

	data, err := bp.EncodeStructureCollisionDelta(batch)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	revision, effectiveTick, mutations, err := bp.DecodeStructureCollisionDelta(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if revision != 42 || effectiveTick != 120044 {
		t.Fatalf("revision=%d effectiveTick=%d, want 42,120044", revision, effectiveTick)
	}
	if len(mutations) != 1 {
		t.Fatalf("got %d mutations, want 1", len(mutations))
	}
	m := mutations[0]
	if m.Operation != StructureDeltaOpUpsert || m.StructureID != 9001 || m.PartID != 1 || !m.Solid {
		t.Errorf("mutation = %+v", m)
	}
	if m.MinX != 100 || m.MinY != 100 || m.MaxX != 200 || m.MaxY != 200 || m.RenderKind != "wood_wall" {
		t.Errorf("mutation geometry = %+v", m)
	}
}

func TestStructureCollisionDelta_RoundTrip_Remove(t *testing.T) {
	bp := &BinaryProtocol{}
	batch := collision.StructureMutationBatch{
		Revision: 5,
		Mutations: []collision.StructureMutation{
			{Operation: collision.StructureOpRemove, Key: collision.StructureColliderKey{StructureID: 42, PartID: 2}},
		},
	}
	data, err := bp.EncodeStructureCollisionDelta(batch)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	_, _, mutations, err := bp.DecodeStructureCollisionDelta(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(mutations) != 1 {
		t.Fatalf("got %d mutations, want 1", len(mutations))
	}
	m := mutations[0]
	if m.Operation != StructureDeltaOpRemove || m.StructureID != 42 || m.PartID != 2 || m.Solid {
		t.Errorf("mutation = %+v", m)
	}
}

func TestStructureCollisionDelta_RoundTrip_SetSolidFalse(t *testing.T) {
	bp := &BinaryProtocol{}
	batch := collision.StructureMutationBatch{
		Revision: 6,
		Mutations: []collision.StructureMutation{
			{Operation: collision.StructureOpSetSolid, Key: collision.StructureColliderKey{StructureID: 1, PartID: 1}, Solid: false},
		},
	}
	data, err := bp.EncodeStructureCollisionDelta(batch)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	_, _, mutations, err := bp.DecodeStructureCollisionDelta(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if mutations[0].Operation != StructureDeltaOpSetSolid || mutations[0].Solid {
		t.Errorf("mutation = %+v", mutations[0])
	}
}

func TestStructureCollisionDelta_CompositeMultiPart(t *testing.T) {
	bp := &BinaryProtocol{}
	batch := collision.StructureMutationBatch{
		Revision: 7,
		Mutations: []collision.StructureMutation{
			{Operation: collision.StructureOpUpsert, Key: collision.StructureColliderKey{StructureID: 1, PartID: 1}, Collider: collision.StructureAABB{StructureID: 1, PartID: 1, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10, RenderKind: "wood_wall"}},
			{Operation: collision.StructureOpUpsert, Key: collision.StructureColliderKey{StructureID: 1, PartID: 2}, Collider: collision.StructureAABB{StructureID: 1, PartID: 2, MinX: 20, MinY: 20, MaxX: 30, MaxY: 30, RenderKind: "wood_wall"}},
		},
	}
	data, err := bp.EncodeStructureCollisionDelta(batch)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	_, _, mutations, err := bp.DecodeStructureCollisionDelta(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(mutations) != 2 {
		t.Fatalf("got %d mutations, want 2", len(mutations))
	}
}

func TestStructureCollisionResyncRequest_RoundTrip(t *testing.T) {
	bp := &BinaryProtocol{}
	data := bp.EncodeStructureCollisionResyncRequest(815)
	msg, err := bp.DecodeClientMessage(data)
	if err != nil {
		t.Fatalf("DecodeClientMessage: %v", err)
	}
	if msg.Type != MessageStructureCollisionResyncRequest || msg.LastRevision != 815 {
		t.Errorf("msg = %+v, want type=%d lastRevision=815", msg, MessageStructureCollisionResyncRequest)
	}
}

func TestStructureCollisionResyncRequest_RejectsWrongLength(t *testing.T) {
	bp := &BinaryProtocol{}
	if _, err := bp.DecodeClientMessage([]byte{MessageStructureCollisionResyncRequest, 1, 2, 3}); err == nil {
		t.Error("expected error for wrong-length resync request")
	}
}

func TestDecodeStructureCollisionSnapshot_RejectsTruncated(t *testing.T) {
	bp := &BinaryProtocol{}
	if _, _, _, err := bp.DecodeStructureCollisionSnapshot([]byte{MessageStructureCollisionSnapshot, 1, 2}); err == nil {
		t.Error("expected error for truncated snapshot")
	}
}

func TestDecodeStructureCollisionDelta_RejectsTruncated(t *testing.T) {
	bp := &BinaryProtocol{}
	if _, _, _, err := bp.DecodeStructureCollisionDelta([]byte{MessageStructureCollisionDelta, 1, 2}); err == nil {
		t.Error("expected error for truncated delta")
	}
}
