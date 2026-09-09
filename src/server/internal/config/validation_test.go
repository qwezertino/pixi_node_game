package config

import (
	"math"
	"sync"
	"testing"
)

func validSettings() *GameSettings {
	return &GameSettings{TickRate: 20, SyncIntervalSec: 30, UnitsPerMeter: 10, WorldWidth: 1000, WorldHeight: 1000, SpawnMinX: 100, SpawnMaxX: 200, SpawnMinY: 100, SpawnMaxY: 200, PlayerBaseScale: 1}
}

func TestBuildRejectsUnsafeSettings(t *testing.T) {
	cases := []func(*GameSettings){
		func(c *GameSettings) { c.TickRate = 0 }, func(c *GameSettings) { c.TickRate = -1 },
		func(c *GameSettings) { c.WorldWidth = 65536 }, func(c *GameSettings) { c.WorldHeight = -1 },
		func(c *GameSettings) { c.SpawnMaxX = c.SpawnMinX }, func(c *GameSettings) { c.SpawnMaxY = 1001 },
		func(c *GameSettings) { c.UnitsPerMeter = math.NaN() }, func(c *GameSettings) { c.UnitsPerMeter = math.Inf(1) },
	}
	for _, mutate := range cases {
		c := validSettings()
		mutate(c)
		if _, _, err := Build(c); err == nil {
			t.Fatalf("accepted unsafe settings: %+v", c)
		}
	}
}

func TestBuildRejectsMalformedEnvAndConversionOverflow(t *testing.T) {
	for _, tc := range []struct{ key, value string }{{"TICK_RATE", "oops"}, {"VELOCITY_REPLICATION", "maybe"}, {"UNITS_PER_METER", "NaN"}, {"SPAWN_MIN_X", "65535"}, {"FANOUT_DROP_STREAK", "4294967297"}, {"FANOUT_CRITICAL_WINDOW_MS", "9223372036854775807"}} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, _, err := Build(validSettings()); err == nil {
				t.Fatal("accepted malformed environment")
			}
		})
	}
}

func TestLiveUpdateRejectsWholeInvalidSnapshotAndSerializesWriters(t *testing.T) {
	cfg, _, err := Build(validSettings())
	if err != nil {
		t.Fatal(err)
	}
	live := NewLiveNet(BuildLiveNetConfig(cfg))
	old := live.Load()
	if err := live.Update(func(c *LiveNetConfig) error { c.SpawnMinX = 65535; return nil }); err == nil {
		t.Fatal("accepted overflow spawn")
	}
	if live.Load() != old {
		t.Fatal("invalid snapshot was published")
	}
	if err := live.Update(func(c *LiveNetConfig) error { c.WriteBatchSize = 0; return nil }); err == nil {
		t.Fatal("accepted invalid batch")
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := live.Update(func(c *LiveNetConfig) error { c.MaxConnections++; return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if live.Load().MaxConnections != old.MaxConnections+32 {
		t.Fatal("lost concurrent update")
	}
	for _, key := range []string{"fanout_critical_window_ms", "fanout_target_ms", "world_state_active_staleness_ms", "world_state_idle_staleness_ms", "world_state_active_window_ms"} {
		if err := live.Update(func(c *LiveNetConfig) error { return c.ApplyKey(key, "9223372036854775807") }); err == nil {
			t.Fatal("accepted duration overflow", key)
		}
	}
}
