package server

import (
	"sync/atomic"
	"time"

	"pixi_game_server/internal/metrics"
)

func populationWritePressure(conns []*Connection, now int64) time.Duration {
	var warm, moderate, severe int
	for _, c := range conns {
		var age int64
		if observed := atomic.LoadInt64(&c.lastWriteObservedNs); observed > 0 && now-observed <= int64(250*time.Millisecond) {
			age = atomic.LoadInt64(&c.lastWriteAgeNs)
		}
		if pending := atomic.LoadInt64(&c.pendingStateNs); pending > 0 && now-pending > age {
			age = now - pending
		}
		if age >= int64(10*time.Millisecond) {
			warm++
		}
		if age > int64(30*time.Millisecond) {
			moderate++
		}
		if age > int64(75*time.Millisecond) {
			severe++
		}
	}
	if len(conns) == 0 {
		return 0
	}
	metrics.WritePressureFraction.WithLabelValues("30ms").Set(float64(moderate) / float64(len(conns)))
	metrics.WritePressureFraction.WithLabelValues("75ms").Set(float64(severe) / float64(len(conns)))
	threshold := max(2, (len(conns)+9)/10)
	switch {
	case severe >= threshold:
		return 76 * time.Millisecond
	case moderate >= threshold:
		return 31 * time.Millisecond
	case warm >= threshold:
		return 10 * time.Millisecond
	default:
		return 0
	}
}
