package protocol

import (
	"encoding/binary"
	"fmt"

	"pixi_game_server/internal/collision"
)

const (
	MessageStructureCollisionSnapshot      = 22
	MessageStructureCollisionDelta         = 23
	MessageStructureCollisionResyncRequest = 24
)

const maxRenderKindLen = 255

func appendUvarint64(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func readUvarint64(data []byte, offset int) (uint64, int, error) {
	var v uint64
	var shift uint
	for {
		if offset >= len(data) {
			return 0, 0, fmt.Errorf("uvarint truncated")
		}
		b := data[offset]
		offset++
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, offset, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, 0, fmt.Errorf("uvarint overflow")
		}
	}
}

func appendRenderKind(dst []byte, kind string) ([]byte, error) {
	if len(kind) > maxRenderKindLen {
		return nil, fmt.Errorf("render kind %q exceeds %d bytes", kind, maxRenderKindLen)
	}
	dst = append(dst, byte(len(kind)))
	return append(dst, kind...), nil
}

func readRenderKind(data []byte, offset int) (string, int, error) {
	if offset >= len(data) {
		return "", 0, fmt.Errorf("render kind length truncated")
	}
	n := int(data[offset])
	offset++
	if offset+n > len(data) {
		return "", 0, fmt.Errorf("render kind bytes truncated")
	}
	return string(data[offset : offset+n]), offset + n, nil
}

func appendAABB32(dst []byte, minX, minY, maxX, maxY int32) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, uint32(minX))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(minY))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(maxX))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(maxY))
	return dst
}

func readAABB32(data []byte, offset int) (minX, minY, maxX, maxY int32, next int, err error) {
	if offset+16 > len(data) {
		return 0, 0, 0, 0, 0, fmt.Errorf("AABB truncated")
	}
	minX = int32(binary.LittleEndian.Uint32(data[offset:]))
	minY = int32(binary.LittleEndian.Uint32(data[offset+4:]))
	maxX = int32(binary.LittleEndian.Uint32(data[offset+8:]))
	maxY = int32(binary.LittleEndian.Uint32(data[offset+12:]))
	return minX, minY, maxX, maxY, offset + 16, nil
}

// EncodeStructureCollisionSnapshot encodes the full-refresh message sent
// once per connection, after MessageWelcome and before the player is
// allowed to spawn: every currently-solid structure collider of the active
// campaign, plus the mutation revision it reflects. See
// docs/collisions_plan.md, "Клиент и API".
func (bp *BinaryProtocol) EncodeStructureCollisionSnapshot(campaignID, revision uint64, colliders []collision.StructureAABB) ([]byte, error) {
	dst := make([]byte, 0, 17+len(colliders)*24)
	dst = append(dst, MessageStructureCollisionSnapshot)
	dst = binary.LittleEndian.AppendUint64(dst, campaignID)
	dst = binary.LittleEndian.AppendUint64(dst, revision)
	dst = appendUvarint(dst, uint32(len(colliders)))

	prevStructureID := uint64(0)
	for _, c := range colliders {
		dst = appendUvarint64(dst, c.StructureID-prevStructureID)
		prevStructureID = c.StructureID
		dst = appendUvarint(dst, c.PartID)
		dst = appendAABB32(dst, c.MinX, c.MinY, c.MaxX, c.MaxY)
		var err error
		dst, err = appendRenderKind(dst, c.RenderKind)
		if err != nil {
			return nil, err
		}
	}
	return dst, nil
}

// StructureColliderWire is one decoded structure collision snapshot row.
type StructureColliderWire struct {
	StructureID uint64
	PartID      uint32
	MinX, MinY  int32
	MaxX, MaxY  int32
	RenderKind  string
}

// DecodeStructureCollisionSnapshot is the counterpart of
// EncodeStructureCollisionSnapshot, used to verify the wire format in tests
// (the production decoder lives in the TypeScript client).
func (bp *BinaryProtocol) DecodeStructureCollisionSnapshot(data []byte) (campaignID, revision uint64, colliders []StructureColliderWire, err error) {
	if len(data) < 1 || data[0] != MessageStructureCollisionSnapshot {
		return 0, 0, nil, fmt.Errorf("not a structure collision snapshot message")
	}
	if len(data) < 17 {
		return 0, 0, nil, fmt.Errorf("structure collision snapshot header truncated")
	}
	campaignID = binary.LittleEndian.Uint64(data[1:9])
	revision = binary.LittleEndian.Uint64(data[9:17])

	count, offset, err := readUvarint64(data, 17)
	if err != nil {
		return 0, 0, nil, err
	}

	colliders = make([]StructureColliderWire, 0, count)
	structureID := uint64(0)
	for i := uint64(0); i < count; i++ {
		delta, next, err := readUvarint64(data, offset)
		if err != nil {
			return 0, 0, nil, err
		}
		offset = next
		structureID += delta

		partID64, next, err := readUvarint64(data, offset)
		if err != nil {
			return 0, 0, nil, err
		}
		offset = next

		minX, minY, maxX, maxY, next, err := readAABB32(data, offset)
		if err != nil {
			return 0, 0, nil, err
		}
		offset = next

		renderKind, next, err := readRenderKind(data, offset)
		if err != nil {
			return 0, 0, nil, err
		}
		offset = next

		colliders = append(colliders, StructureColliderWire{
			StructureID: structureID,
			PartID:      uint32(partID64),
			MinX:        minX, MinY: minY, MaxX: maxX, MaxY: maxY,
			RenderKind: renderKind,
		})
	}
	return campaignID, revision, colliders, nil
}

const (
	StructureDeltaOpUpsert   uint8 = 0
	StructureDeltaOpRemove   uint8 = 1
	StructureDeltaOpSetSolid uint8 = 2
)

// EncodeStructureCollisionDelta encodes one reliable, ordered
// structure_collision_delta message: an atomically-applied
// StructureMutationBatch, as published by
// (*collision.StructureGrid).ApplyMutationBatch.
func (bp *BinaryProtocol) EncodeStructureCollisionDelta(batch collision.StructureMutationBatch) ([]byte, error) {
	dst := make([]byte, 0, 17+len(batch.Mutations)*32)
	dst = append(dst, MessageStructureCollisionDelta)
	dst = binary.LittleEndian.AppendUint64(dst, batch.Revision)
	dst = binary.LittleEndian.AppendUint64(dst, batch.EffectiveTick)
	dst = appendUvarint(dst, uint32(len(batch.Mutations)))

	for _, m := range batch.Mutations {
		var op uint8
		switch m.Operation {
		case collision.StructureOpUpsert:
			op = StructureDeltaOpUpsert
		case collision.StructureOpRemove:
			op = StructureDeltaOpRemove
		case collision.StructureOpSetSolid:
			op = StructureDeltaOpSetSolid
		default:
			return nil, fmt.Errorf("unknown structure mutation operation %d", m.Operation)
		}
		dst = append(dst, op)
		dst = binary.LittleEndian.AppendUint64(dst, m.Key.StructureID)
		dst = appendUvarint(dst, m.Key.PartID)

		solid := uint8(0)
		if op == StructureDeltaOpUpsert || (op == StructureDeltaOpSetSolid && m.Solid) {
			solid = 1
		}
		dst = append(dst, solid)

		if solid == 1 {
			dst = appendAABB32(dst, m.Collider.MinX, m.Collider.MinY, m.Collider.MaxX, m.Collider.MaxY)
			var err error
			dst, err = appendRenderKind(dst, m.Collider.RenderKind)
			if err != nil {
				return nil, err
			}
		}
	}
	return dst, nil
}

// StructureDeltaMutationWire is one decoded delta mutation entry.
type StructureDeltaMutationWire struct {
	Operation   uint8
	StructureID uint64
	PartID      uint32
	Solid       bool
	MinX, MinY  int32
	MaxX, MaxY  int32
	RenderKind  string
}

// DecodeStructureCollisionDelta is the counterpart of
// EncodeStructureCollisionDelta, used to verify the wire format in tests.
func (bp *BinaryProtocol) DecodeStructureCollisionDelta(data []byte) (revision, effectiveTick uint64, mutations []StructureDeltaMutationWire, err error) {
	if len(data) < 1 || data[0] != MessageStructureCollisionDelta {
		return 0, 0, nil, fmt.Errorf("not a structure collision delta message")
	}
	if len(data) < 17 {
		return 0, 0, nil, fmt.Errorf("structure collision delta header truncated")
	}
	revision = binary.LittleEndian.Uint64(data[1:9])
	effectiveTick = binary.LittleEndian.Uint64(data[9:17])

	count, offset, err := readUvarint64(data, 17)
	if err != nil {
		return 0, 0, nil, err
	}

	mutations = make([]StructureDeltaMutationWire, 0, count)
	for i := uint64(0); i < count; i++ {
		if offset >= len(data) {
			return 0, 0, nil, fmt.Errorf("mutation truncated")
		}
		op := data[offset]
		offset++
		if offset+8 > len(data) {
			return 0, 0, nil, fmt.Errorf("mutation structureId truncated")
		}
		structureID := binary.LittleEndian.Uint64(data[offset:])
		offset += 8

		partID64, next, err := readUvarint64(data, offset)
		if err != nil {
			return 0, 0, nil, err
		}
		offset = next

		if offset >= len(data) {
			return 0, 0, nil, fmt.Errorf("mutation solid flag truncated")
		}
		solid := data[offset] == 1
		offset++

		m := StructureDeltaMutationWire{Operation: op, StructureID: structureID, PartID: uint32(partID64), Solid: solid}
		if solid {
			minX, minY, maxX, maxY, next, err := readAABB32(data, offset)
			if err != nil {
				return 0, 0, nil, err
			}
			offset = next
			renderKind, next, err := readRenderKind(data, offset)
			if err != nil {
				return 0, 0, nil, err
			}
			offset = next
			m.MinX, m.MinY, m.MaxX, m.MaxY, m.RenderKind = minX, minY, maxX, maxY, renderKind
		}
		mutations = append(mutations, m)
	}
	return revision, effectiveTick, mutations, nil
}

// EncodeStructureCollisionResyncRequest encodes the client->server request
// for a fresh full structure_collision_snapshot after a duplicate, gap,
// campaign/mapVersion mismatch, or unknown-entity delta.
func (bp *BinaryProtocol) EncodeStructureCollisionResyncRequest(lastRevision uint64) []byte {
	dst := make([]byte, 9)
	dst[0] = MessageStructureCollisionResyncRequest
	binary.LittleEndian.PutUint64(dst[1:], lastRevision)
	return dst
}
