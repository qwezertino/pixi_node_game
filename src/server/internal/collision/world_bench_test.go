package collision

import "testing"

func BenchmarkMoveCircle(b *testing.B) {
	mapGrid, err := BuildMapStaticGrid(32000, 32000, []MapAABB{
		{ID: 1, MinX: 3300, MinY: 700, MaxX: 3340, MaxY: 1800, RenderKind: "stone_wall"},
	})
	if err != nil {
		b.Fatalf("BuildMapStaticGrid: %v", err)
	}
	structGrid, err := BuildStructureGrid(32000, 32000, []StructureAABB{
		{StructureID: 9002, PartID: 1, MinX: 3300, MinY: 1760, MaxX: 4300, MaxY: 1800, RenderKind: "stone_wall"},
	}, 0)
	if err != nil {
		b.Fatalf("BuildStructureGrid: %v", err)
	}
	w := NewCollisionWorld(32000, 32000, mapGrid, structGrid)
	scratch := &MoveScratch{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.MoveCircle(5000, 5000, 1, 1, 26, scratch)
	}
}
