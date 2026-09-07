package systems

import (
	"log/slog"
	"sync"
)

type gridCell struct {
	mu      sync.RWMutex
	players []uint32
}

type playerCell struct {
	gridX, gridY uint16
}

type VisibilityManager struct {
	gridSize   uint16
	gridWidth  uint16
	gridHeight uint16
	cells      []gridCell

	playerCells sync.Map
}

func NewVisibilityManager(worldWidth, worldHeight, gridSize uint16) *VisibilityManager {
	gridW := (worldWidth + gridSize - 1) / gridSize
	gridH := (worldHeight + gridSize - 1) / gridSize

	vm := &VisibilityManager{
		gridSize:   gridSize,
		gridWidth:  gridW,
		gridHeight: gridH,
		cells:      make([]gridCell, int(gridW)*int(gridH)),
	}

	slog.Info("visibility manager initialized", "grid_w", gridW, "grid_h", gridH, "cell_size", gridSize)
	return vm
}

func (vm *VisibilityManager) worldToGrid(x, y uint16) (uint16, uint16) {
	gx := x / vm.gridSize
	gy := y / vm.gridSize
	if gx >= vm.gridWidth {
		gx = vm.gridWidth - 1
	}
	if gy >= vm.gridHeight {
		gy = vm.gridHeight - 1
	}
	return gx, gy
}

func (vm *VisibilityManager) cellIndex(gx, gy uint16) int {
	return int(gy)*int(vm.gridWidth) + int(gx)
}

func (vm *VisibilityManager) AddPlayer(playerID uint32, x, y uint16) {
	gx, gy := vm.worldToGrid(x, y)
	vm.addToCell(gx, gy, playerID)
	vm.playerCells.Store(playerID, playerCell{gx, gy})
}

func (vm *VisibilityManager) RemovePlayer(playerID uint32) {
	if val, ok := vm.playerCells.LoadAndDelete(playerID); ok {
		pc := val.(playerCell)
		vm.removeFromCell(pc.gridX, pc.gridY, playerID)
	}
}

func (vm *VisibilityManager) MovePlayer(playerID uint32, newX, newY uint16) {
	newGX, newGY := vm.worldToGrid(newX, newY)

	val, ok := vm.playerCells.Load(playerID)
	if !ok {
		vm.addToCell(newGX, newGY, playerID)
		vm.playerCells.Store(playerID, playerCell{newGX, newGY})
		return
	}

	pc := val.(playerCell)
	if pc.gridX == newGX && pc.gridY == newGY {
		return
	}

	vm.removeFromCell(pc.gridX, pc.gridY, playerID)
	vm.addToCell(newGX, newGY, playerID)
	vm.playerCells.Store(playerID, playerCell{newGX, newGY})
}

func (vm *VisibilityManager) addToCell(gx, gy uint16, playerID uint32) {
	cell := &vm.cells[vm.cellIndex(gx, gy)]
	cell.mu.Lock()
	cell.players = append(cell.players, playerID)
	cell.mu.Unlock()
}

func (vm *VisibilityManager) removeFromCell(gx, gy uint16, playerID uint32) {
	cell := &vm.cells[vm.cellIndex(gx, gy)]
	cell.mu.Lock()
	players := cell.players
	for i, id := range players {
		if id == playerID {

			players[i] = players[len(players)-1]
			cell.players = players[:len(players)-1]
			break
		}
	}
	cell.mu.Unlock()
}
