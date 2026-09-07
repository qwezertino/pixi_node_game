
package clock

import "time"

var epoch = time.Now()

func Now() int64 { return SinceEpoch(time.Now()) }

func SinceEpoch(t time.Time) int64 { return t.Sub(epoch).Nanoseconds() + 1 }

func WallOffset() time.Duration {
	now := time.Now()
	return time.Duration(now.UnixNano()-epoch.UnixNano()) - now.Sub(epoch)
}
