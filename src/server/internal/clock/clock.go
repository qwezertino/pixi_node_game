// Package clock supplies a shared monotonic time domain for in-process deadlines.
package clock

import "time"

var epoch = time.Now()

// Now returns nanoseconds since process initialization. Zero is reserved for
// unset timestamps. These values must never be serialized as wall-clock dates.
func Now() int64 { return SinceEpoch(time.Now()) }

func SinceEpoch(t time.Time) int64 { return t.Sub(epoch).Nanoseconds() + 1 }

// WallOffset exposes wall-clock corrections for diagnostics only. Control loops
// must use Now instead, so a clock synchronization cannot masquerade as load.
func WallOffset() time.Duration {
	now := time.Now()
	return time.Duration(now.UnixNano()-epoch.UnixNano()) - now.Sub(epoch)
}
