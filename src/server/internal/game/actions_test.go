package game

import (
	"sync/atomic"
	"testing"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/systems"
	"pixi_game_server/internal/types"
)

func TestActionsExecuteOnlyDuringTickAndPreserveOrder(t *testing.T) {
	p := &types.Player{ID: 1}
	p.SetX(100)
	p.SetY(100)
	p.SetStaminaCenti(1000)
	gw := &GameWorld{cfg: &config.Config{World: config.WorldConfig{MaxX: 1000, MaxY: 1000}}, playersMap: map[uint32]*types.Player{1: p}, visibilityManager: systems.NewVisibilityManager(1000, 1000, 100)}
	gw.unitTablesPtr.Store(&unitTables{attackDurationTicks: map[uint8]uint32{0: 2}, comboSteps: map[uint8]uint8{0: 2}, staminaStats: map[uint8]staminaStat{0: {maxCenti: 1000, canBlock: true, attackStaminaCostCenti: 100}}})
	ch := make(chan tickWorkerInput)
	done := make(chan struct{})
	go func() { gw.runTickWorker(ch); close(done) }()
	defer func() { close(ch); <-done }()
	step := func(tick uint32) {
		atomic.StoreUint32(&gw.tickCount, tick)
		gw.tickWorkerWg.Add(1)
		ch <- tickWorkerInput{ptrs: []*types.Player{p}, worldTick: tick, nowNano: 1}
		gw.tickWorkerWg.Wait()
	}
	for _, kind := range []types.ActionType{types.ActionBlockStart, types.ActionBlockEnd, types.ActionAttack} {
		if !gw.QueueAction(1, types.PlayerAction{Type: kind}) {
			t.Fatal("enqueue failed")
		}
	}
	if p.GetState() != types.StateIdle || p.GetStaminaCenti() != 1000 {
		t.Fatal("network path mutated gameplay")
	}
	step(10)
	if p.GetState() != types.StateAttacking || p.GetStaminaCenti() != 900 || p.GetAttackStartTick() != 10 {
		t.Fatal("action order or stamina incorrect")
	}
	gw.QueueAction(1, types.PlayerAction{Type: types.ActionAttack})
	step(11)
	if p.GetStaminaCenti() != 900 {
		t.Fatal("buffered combo charged early")
	}
	gw.QueueAction(1, types.PlayerAction{Type: types.ActionAttack})
	step(12)
	if p.GetStaminaCenti() != 800 || p.GetAttackStartTick() != 12 || p.GetState() != types.StateAttacking {
		t.Fatal("boundary attack lost or double charged")
	}
	for i := 0; i < types.MaxPendingActions; i++ {
		if !gw.QueueAction(1, types.PlayerAction{Type: types.ActionFace}) {
			t.Fatal("queue filled early")
		}
	}
	if gw.QueueAction(1, types.PlayerAction{Type: types.ActionAttack}) {
		t.Fatal("unbounded action queue")
	}
}
