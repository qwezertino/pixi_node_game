package server

import (
	"testing"
	"time"
)

func TestPopulationPressureIsolatesSlowClient(t *testing.T) {
	now := int64(time.Second)
	conns := make([]*Connection, 1200)
	for i := range conns {
		conns[i] = &Connection{}
	}
	conns[0].pendingStateNs = now - int64(500*time.Millisecond)
	if got := populationWritePressure(conns, now); got != 0 {
		t.Fatalf("one stuck client slowed whole population: %v", got)
	}
	for i := 1; i < 120; i++ {
		conns[i].pendingStateNs = now - int64(80*time.Millisecond)
	}
	if got := populationWritePressure(conns, now); got <= 75*time.Millisecond {
		t.Fatalf("10%% blocked writers must signal severe pressure: %v", got)
	}
}

func TestPopulationPressureExpiresCompletedSamples(t *testing.T) {
	now := int64(time.Second)
	conns := []*Connection{
		{lastWriteAgeNs: int64(40 * time.Millisecond), lastWriteObservedNs: now},
		{lastWriteAgeNs: int64(40 * time.Millisecond), lastWriteObservedNs: now},
	}
	if got := populationWritePressure(conns, now); got <= 30*time.Millisecond || got > 75*time.Millisecond {
		t.Fatalf("expected moderate pressure, got %v", got)
	}
	if got := populationWritePressure(conns, now+int64(time.Second)); got != 0 {
		t.Fatalf("completed samples must expire, got %v", got)
	}
}

func TestPopulationPressureRequiresTwoSlowConnections(t *testing.T) {
	conns := []*Connection{{pendingStateNs: 1}, {}}
	if got := populationWritePressure(conns, int64(time.Second)); got != 0 {
		t.Fatalf("one slow peer must not slow the healthy peer: %v", got)
	}
}
