package systems

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"pixi_game_server/internal/types"
)

// VisibilityManager управляет видимостью игроков для оптимизации broadcasting
type VisibilityManager struct {
	// Spatial grid для быстрого поиска соседних игроков
	gridSize   uint16
	gridWidth  uint16
	gridHeight uint16
	grid       [][]uint32 // grid[x][y] = list of player IDs
	gridMutex  sync.RWMutex

	// LRU cache для viewport queries
	cacheSize   int
	cache       map[uint64][]uint32 // cache[hash] = visible player IDs
	cacheMutex  sync.RWMutex
	cacheAccess map[uint64]int64 // access timestamp

	// Performance metrics
	cacheHits   uint64
	cacheMisses uint64
	gridUpdates uint64
}

// NewVisibilityManager создает новый менеджер видимости
func NewVisibilityManager(worldWidth, worldHeight, gridSize uint16) *VisibilityManager {
	gridW := (worldWidth + gridSize - 1) / gridSize
	gridH := (worldHeight + gridSize - 1) / gridSize

	// Initialize grid
	grid := make([][]uint32, gridW)
	for i := range grid {
		grid[i] = make([]uint32, gridH)
	}

	vm := &VisibilityManager{
		gridSize:    gridSize,
		gridWidth:   gridW,
		gridHeight:  gridH,
		grid:        grid,
		cacheSize:   1000, // Cache для 1000 viewport queries
		cache:       make(map[uint64][]uint32),
		cacheAccess: make(map[uint64]int64),
	}

	slog.Info("visibility manager initialized", "grid_w", gridW, "grid_h", gridH, "cell_size", gridSize)

	return vm
}

// UpdatePlayerPosition обновляет позицию игрока в spatial grid
func (vm *VisibilityManager) UpdatePlayerPosition(playerID uint32, x, y uint16) {
	gridX := x / vm.gridSize
	gridY := y / vm.gridSize

	if gridX >= vm.gridWidth || gridY >= vm.gridHeight {
		return // Out of bounds
	}

	vm.gridMutex.Lock()
	defer vm.gridMutex.Unlock()

	// Remove from old position (simplified - would need to track old position)
	// Add to new position
	vm.addToGrid(gridX, gridY, playerID)

	atomic.AddUint64(&vm.gridUpdates, 1)
}

// GetVisiblePlayers возвращает список игроков видимых из viewport
func (vm *VisibilityManager) GetVisiblePlayers(viewport types.ViewportBounds) []uint32 {
	// Create cache key from viewport
	cacheKey := vm.hashViewport(viewport)

	// Check cache first
	vm.cacheMutex.RLock()
	if cached, exists := vm.cache[cacheKey]; exists {
		vm.cacheAccess[cacheKey] = time.Now().UnixNano()
		vm.cacheMutex.RUnlock()
		atomic.AddUint64(&vm.cacheHits, 1)
		return cached
	}
	vm.cacheMutex.RUnlock()

	atomic.AddUint64(&vm.cacheMisses, 1)

	// Calculate visible players
	visible := vm.calculateVisiblePlayers(viewport)

	// Cache the result
	vm.cacheResult(cacheKey, visible)

	return visible
}

// calculateVisiblePlayers вычисляет видимых игроков
func (vm *VisibilityManager) calculateVisiblePlayers(viewport types.ViewportBounds) []uint32 {
	var visible []uint32

	// Calculate grid bounds for viewport
	minGridX := viewport.MinX / vm.gridSize
	maxGridX := (viewport.MaxX + vm.gridSize - 1) / vm.gridSize
	minGridY := viewport.MinY / vm.gridSize
	maxGridY := (viewport.MaxY + vm.gridSize - 1) / vm.gridSize

	vm.gridMutex.RLock()
	defer vm.gridMutex.RUnlock()

	// Iterate through relevant grid cells
	for x := minGridX; x <= maxGridX && x < vm.gridWidth; x++ {
		for y := minGridY; y <= maxGridY && y < vm.gridHeight; y++ {
			// Get players from this grid cell
			players := vm.getFromGrid(x, y)
			visible = append(visible, players...)
		}
	}

	return visible
}

// cacheResult кэширует результат с LRU eviction
func (vm *VisibilityManager) cacheResult(key uint64, result []uint32) {
	vm.cacheMutex.Lock()
	defer vm.cacheMutex.Unlock()

	// Check cache size and evict if necessary
	if len(vm.cache) >= vm.cacheSize {
		vm.evictOldest()
	}

	vm.cache[key] = result
	vm.cacheAccess[key] = time.Now().UnixNano()
}

// evictOldest удаляет самый старый элемент из cache
func (vm *VisibilityManager) evictOldest() {
	var oldestKey uint64
	var oldestTime int64 = time.Now().UnixNano()

	for key, accessTime := range vm.cacheAccess {
		if accessTime < oldestTime {
			oldestTime = accessTime
			oldestKey = key
		}
	}

	delete(vm.cache, oldestKey)
	delete(vm.cacheAccess, oldestKey)
}

// hashViewport создает хэш для viewport (простая реализация)
func (vm *VisibilityManager) hashViewport(v types.ViewportBounds) uint64 {
	return uint64(v.MinX)<<48 | uint64(v.MinY)<<32 | uint64(v.MaxX)<<16 | uint64(v.MaxY)
}

// addToGrid добавляет игрока в grid cell (упрощенная реализация)
func (vm *VisibilityManager) addToGrid(x, y uint16, playerID uint32) {
	// В реальной реализации это была бы slice или linked list
	// Для демонстрации используем простое присваивание
	if x < vm.gridWidth && y < vm.gridHeight {
		vm.grid[x][y] = playerID
	}
}

// getFromGrid получает игроков из grid cell (упрощенная реализация)
func (vm *VisibilityManager) getFromGrid(x, y uint16) []uint32 {
	if x < vm.gridWidth && y < vm.gridHeight && vm.grid[x][y] != 0 {
		return []uint32{vm.grid[x][y]}
	}
	return nil
}

// GetStats возвращает статистику производительности
func (vm *VisibilityManager) GetStats() map[string]uint64 {
	return map[string]uint64{
		"cache_hits":   atomic.LoadUint64(&vm.cacheHits),
		"cache_misses": atomic.LoadUint64(&vm.cacheMisses),
		"grid_updates": atomic.LoadUint64(&vm.gridUpdates),
		"cache_size":   uint64(len(vm.cache)),
	}
}
