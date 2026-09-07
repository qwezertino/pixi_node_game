package config

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"
)

// LiveNetConfig holds the subset of NetworkConfig (plus spawn bounds) that is
// safe to change while the server is running: it only feeds per-tick fanout
// decisions and per-connection setup, never anything baked into precomputed
// tables (tick rate, world size, unit stats) or already-bound listeners.
type LiveNetConfig struct {
	MaxConnections                 int
	MessageRateLimit               int
	BurstLimit                     int
	IPConnRate                     float64
	IPConnBurst                    int
	FanoutMaxBroadcastBytesPerTick int
	VelocityReplication            bool
	KeyframeDivisor                int
	FanoutQueueShedDepth           int
	FanoutDropStreak               int32
	WriteBatchSize                 int
	FanoutFairDebtMax              int32
	FanoutFairDebtInc              int32
	FanoutFairDebtDec              int32
	FanoutFairDebtWeightNs         int64
	FanoutRoundRobinWeightNs       int64
	FanoutCriticalWindowNs         int64
	FanoutCriticalBoostNs          int64
	FanoutMinRecipientsPerTick     int
	FanoutMaxRecipientsPerTick     int
	FanoutTarget                   time.Duration
	WorldStateActiveStalenessNs    int64
	WorldStateIdleStalenessNs      int64
	WorldStateActiveWindowNs       int64
	SpawnMinX                      uint16
	SpawnMaxX                      uint16
	SpawnMinY                      uint16
	SpawnMaxY                      uint16
}

// BuildLiveNetConfig seeds a LiveNetConfig from the static Config built at
// startup (.env defaults). This is only the initial value: once the live
// config store (Postgres/Redis, see internal/liveconfig) takes over, DB rows
// win.
func BuildLiveNetConfig(cfg *Config) *LiveNetConfig {
	c := &LiveNetConfig{
		MaxConnections:                 cfg.Net.MaxConnections,
		MessageRateLimit:               cfg.Net.MessageRateLimit,
		BurstLimit:                     cfg.Net.BurstLimit,
		IPConnRate:                     cfg.Net.IPConnRate,
		IPConnBurst:                    cfg.Net.IPConnBurst,
		FanoutMaxBroadcastBytesPerTick: cfg.Net.FanoutMaxBroadcastBytesPerTick,
		VelocityReplication:            cfg.Net.VelocityReplication,
		KeyframeDivisor:                cfg.Net.KeyframeDivisor,
		FanoutQueueShedDepth:           cfg.Net.FanoutQueueShedDepth,
		FanoutDropStreak:               int32(cfg.Net.FanoutDropStreak),
		WriteBatchSize:                 cfg.Net.WriteBatchSize,
		FanoutFairDebtMax:              int32(cfg.Net.FanoutFairDebtMax),
		FanoutFairDebtInc:              int32(cfg.Net.FanoutFairDebtInc),
		FanoutFairDebtDec:              int32(cfg.Net.FanoutFairDebtDec),
		FanoutFairDebtWeightNs:         cfg.Net.FanoutFairDebtWeightNs,
		FanoutRoundRobinWeightNs:       cfg.Net.FanoutRoundRobinWeightNs,
		FanoutCriticalWindowNs:         cfg.Net.FanoutCriticalWindow.Nanoseconds(),
		FanoutCriticalBoostNs:          cfg.Net.FanoutCriticalBoostNs,
		FanoutMinRecipientsPerTick:     cfg.Net.FanoutMinRecipientsPerTick,
		FanoutMaxRecipientsPerTick:     cfg.Net.FanoutMaxRecipientsPerTick,
		FanoutTarget:                   time.Duration(cfg.Net.FanoutTargetMs) * time.Millisecond,
		WorldStateActiveStalenessNs:    cfg.Net.WorldStateActiveStaleness.Nanoseconds(),
		WorldStateIdleStalenessNs:      cfg.Net.WorldStateIdleStaleness.Nanoseconds(),
		WorldStateActiveWindowNs:       cfg.Net.WorldStateActiveWindow.Nanoseconds(),
		SpawnMinX:                      cfg.World.SpawnMinX,
		SpawnMaxX:                      cfg.World.SpawnMaxX,
		SpawnMinY:                      cfg.World.SpawnMinY,
		SpawnMaxY:                      cfg.World.SpawnMaxY,
	}
	clampLiveNetConfig(c)
	return c
}

// clampLiveNetConfig applies the same floors/derived rules the old
// server.New() used to bake in once at startup. It must run on every
// update too, since values can now change at runtime.
func clampLiveNetConfig(c *LiveNetConfig) {
	if c.FanoutDropStreak < 1 {
		c.FanoutDropStreak = 1
	}
	if c.WriteBatchSize < 1 {
		c.WriteBatchSize = 1
	}
	if c.FanoutMaxBroadcastBytesPerTick < 0 {
		c.FanoutMaxBroadcastBytesPerTick = 0
	}
	if c.FanoutQueueShedDepth < 1 {
		c.FanoutQueueShedDepth = 0
	}
	if c.FanoutFairDebtMax < 0 {
		c.FanoutFairDebtMax = 0
	}
	if c.FanoutFairDebtInc < 0 {
		c.FanoutFairDebtInc = 0
	}
	if c.FanoutFairDebtDec < 0 {
		c.FanoutFairDebtDec = 0
	}
	if c.FanoutFairDebtWeightNs < 0 {
		c.FanoutFairDebtWeightNs = 0
	}
	if c.FanoutRoundRobinWeightNs < 0 {
		c.FanoutRoundRobinWeightNs = 0
	}
	if c.FanoutCriticalWindowNs < 0 {
		c.FanoutCriticalWindowNs = 0
	}
	if c.FanoutCriticalBoostNs < 0 {
		c.FanoutCriticalBoostNs = 0
	}
	if c.FanoutMinRecipientsPerTick < 1 {
		c.FanoutMinRecipientsPerTick = 1
	}
	if c.FanoutMaxRecipientsPerTick > 0 && c.FanoutMinRecipientsPerTick > c.FanoutMaxRecipientsPerTick {
		c.FanoutMinRecipientsPerTick = c.FanoutMaxRecipientsPerTick
	}
	if c.FanoutTarget <= 0 {
		c.FanoutTarget = 12 * time.Millisecond
	}
	if c.WorldStateActiveStalenessNs <= 0 {
		c.WorldStateActiveStalenessNs = (150 * time.Millisecond).Nanoseconds()
	}
	if c.WorldStateIdleStalenessNs < c.WorldStateActiveStalenessNs {
		c.WorldStateIdleStalenessNs = c.WorldStateActiveStalenessNs
	}
	if c.WorldStateActiveWindowNs <= 0 {
		c.WorldStateActiveWindowNs = (1 * time.Second).Nanoseconds()
	}
	if c.MaxConnections < 1 {
		c.MaxConnections = 1
	}
	if c.SpawnMaxX <= c.SpawnMinX {
		c.SpawnMaxX = c.SpawnMinX + 1
	}
	if c.SpawnMaxY <= c.SpawnMinY {
		c.SpawnMaxY = c.SpawnMinY + 1
	}
}

// LiveNet is a lock-free, hot-swappable holder for LiveNetConfig. Readers on
// the tick/fanout hot path call Load() once per function and read fields off
// the returned snapshot; writers (the DB watcher) call Update to publish a
// brand new, fully-clamped snapshot atomically.
type LiveNet struct {
	ptr atomic.Pointer[LiveNetConfig]
}

func NewLiveNet(initial *LiveNetConfig) *LiveNet {
	ln := &LiveNet{}
	ln.ptr.Store(initial)
	return ln
}

func (l *LiveNet) Load() *LiveNetConfig {
	return l.ptr.Load()
}

// Update clones the current snapshot, applies mutate, clamps the result and
// publishes it as the new snapshot in one atomic swap.
func (l *LiveNet) Update(mutate func(*LiveNetConfig)) *LiveNetConfig {
	curr := l.ptr.Load()
	next := *curr
	mutate(&next)
	clampLiveNetConfig(&next)
	l.ptr.Store(&next)
	return &next
}

// LiveConfigKeys lists every DB-backed key in the order they should be
// seeded, matched 1:1 with ApplyKey/KeyValues below.
var LiveConfigKeys = []string{
	"max_connections",
	"rate_limit_msg_sec",
	"rate_limit_burst",
	"ip_conn_rate",
	"ip_conn_burst",
	"fanout_max_broadcast_bytes_per_tick",
	"velocity_replication",
	"keyframe_divisor",
	"fanout_queue_shed_depth",
	"fanout_drop_streak",
	"write_batch_size",
	"fanout_fair_debt_max",
	"fanout_fair_debt_inc",
	"fanout_fair_debt_dec",
	"fanout_fair_debt_weight_ns",
	"fanout_round_robin_weight_ns",
	"fanout_critical_window_ms",
	"fanout_critical_boost_ns",
	"fanout_min_recipients_per_tick",
	"fanout_max_recipients_per_tick",
	"fanout_target_ms",
	"world_state_active_staleness_ms",
	"world_state_idle_staleness_ms",
	"world_state_active_window_ms",
	"spawn_min_x",
	"spawn_max_x",
	"spawn_min_y",
	"spawn_max_y",
}

// KeyValues renders every live key as its current string value, for seeding
// the DB on first run.
func (c *LiveNetConfig) KeyValues() map[string]string {
	return map[string]string{
		"max_connections":                      strconv.Itoa(c.MaxConnections),
		"rate_limit_msg_sec":                   strconv.Itoa(c.MessageRateLimit),
		"rate_limit_burst":                     strconv.Itoa(c.BurstLimit),
		"ip_conn_rate":                         strconv.FormatFloat(c.IPConnRate, 'f', -1, 64),
		"ip_conn_burst":                        strconv.Itoa(c.IPConnBurst),
		"fanout_max_broadcast_bytes_per_tick":  strconv.Itoa(c.FanoutMaxBroadcastBytesPerTick),
		"velocity_replication":                 strconv.FormatBool(c.VelocityReplication),
		"keyframe_divisor":                     strconv.Itoa(c.KeyframeDivisor),
		"fanout_queue_shed_depth":              strconv.Itoa(c.FanoutQueueShedDepth),
		"fanout_drop_streak":                   strconv.Itoa(int(c.FanoutDropStreak)),
		"write_batch_size":                     strconv.Itoa(c.WriteBatchSize),
		"fanout_fair_debt_max":                 strconv.Itoa(int(c.FanoutFairDebtMax)),
		"fanout_fair_debt_inc":                 strconv.Itoa(int(c.FanoutFairDebtInc)),
		"fanout_fair_debt_dec":                 strconv.Itoa(int(c.FanoutFairDebtDec)),
		"fanout_fair_debt_weight_ns":           strconv.FormatInt(c.FanoutFairDebtWeightNs, 10),
		"fanout_round_robin_weight_ns":         strconv.FormatInt(c.FanoutRoundRobinWeightNs, 10),
		"fanout_critical_window_ms":            strconv.FormatInt(c.FanoutCriticalWindowNs/int64(time.Millisecond), 10),
		"fanout_critical_boost_ns":             strconv.FormatInt(c.FanoutCriticalBoostNs, 10),
		"fanout_min_recipients_per_tick":       strconv.Itoa(c.FanoutMinRecipientsPerTick),
		"fanout_max_recipients_per_tick":       strconv.Itoa(c.FanoutMaxRecipientsPerTick),
		"fanout_target_ms":                     strconv.FormatInt(int64(c.FanoutTarget/time.Millisecond), 10),
		"world_state_active_staleness_ms":      strconv.FormatInt(c.WorldStateActiveStalenessNs/int64(time.Millisecond), 10),
		"world_state_idle_staleness_ms":        strconv.FormatInt(c.WorldStateIdleStalenessNs/int64(time.Millisecond), 10),
		"world_state_active_window_ms":         strconv.FormatInt(c.WorldStateActiveWindowNs/int64(time.Millisecond), 10),
		"spawn_min_x":                          strconv.Itoa(int(c.SpawnMinX)),
		"spawn_max_x":                          strconv.Itoa(int(c.SpawnMaxX)),
		"spawn_min_y":                          strconv.Itoa(int(c.SpawnMinY)),
		"spawn_max_y":                          strconv.Itoa(int(c.SpawnMaxY)),
	}
}

// ApplyKey parses value and writes it onto the matching field. Unknown keys
// are reported but never fatal — a typo in the DB shouldn't take the field
// down, it should just be ignored (and logged by the caller).
func (c *LiveNetConfig) ApplyKey(key, value string) error {
	switch key {
	case "max_connections":
		return applyInt(value, &c.MaxConnections)
	case "rate_limit_msg_sec":
		return applyInt(value, &c.MessageRateLimit)
	case "rate_limit_burst":
		return applyInt(value, &c.BurstLimit)
	case "ip_conn_rate":
		return applyFloat(value, &c.IPConnRate)
	case "ip_conn_burst":
		return applyInt(value, &c.IPConnBurst)
	case "fanout_max_broadcast_bytes_per_tick":
		return applyInt(value, &c.FanoutMaxBroadcastBytesPerTick)
	case "velocity_replication":
		return applyBool(value, &c.VelocityReplication)
	case "keyframe_divisor":
		return applyInt(value, &c.KeyframeDivisor)
	case "fanout_queue_shed_depth":
		return applyInt(value, &c.FanoutQueueShedDepth)
	case "fanout_drop_streak":
		return applyInt32(value, &c.FanoutDropStreak)
	case "write_batch_size":
		return applyInt(value, &c.WriteBatchSize)
	case "fanout_fair_debt_max":
		return applyInt32(value, &c.FanoutFairDebtMax)
	case "fanout_fair_debt_inc":
		return applyInt32(value, &c.FanoutFairDebtInc)
	case "fanout_fair_debt_dec":
		return applyInt32(value, &c.FanoutFairDebtDec)
	case "fanout_fair_debt_weight_ns":
		return applyInt64(value, &c.FanoutFairDebtWeightNs)
	case "fanout_round_robin_weight_ns":
		return applyInt64(value, &c.FanoutRoundRobinWeightNs)
	case "fanout_critical_window_ms":
		ms, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		c.FanoutCriticalWindowNs = ms * int64(time.Millisecond)
		return nil
	case "fanout_critical_boost_ns":
		return applyInt64(value, &c.FanoutCriticalBoostNs)
	case "fanout_min_recipients_per_tick":
		return applyInt(value, &c.FanoutMinRecipientsPerTick)
	case "fanout_max_recipients_per_tick":
		return applyInt(value, &c.FanoutMaxRecipientsPerTick)
	case "fanout_target_ms":
		ms, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		c.FanoutTarget = time.Duration(ms) * time.Millisecond
		return nil
	case "world_state_active_staleness_ms":
		ms, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		c.WorldStateActiveStalenessNs = ms * int64(time.Millisecond)
		return nil
	case "world_state_idle_staleness_ms":
		ms, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		c.WorldStateIdleStalenessNs = ms * int64(time.Millisecond)
		return nil
	case "world_state_active_window_ms":
		ms, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		c.WorldStateActiveWindowNs = ms * int64(time.Millisecond)
		return nil
	case "spawn_min_x":
		return applyUint16(value, &c.SpawnMinX)
	case "spawn_max_x":
		return applyUint16(value, &c.SpawnMaxX)
	case "spawn_min_y":
		return applyUint16(value, &c.SpawnMinY)
	case "spawn_max_y":
		return applyUint16(value, &c.SpawnMaxY)
	default:
		return fmt.Errorf("unknown live config key %q", key)
	}
}

func applyInt(value string, dst *int) error {
	v, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

func applyInt32(value string, dst *int32) error {
	v, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return err
	}
	*dst = int32(v)
	return nil
}

func applyInt64(value string, dst *int64) error {
	v, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

func applyUint16(value string, dst *uint16) error {
	v, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return err
	}
	*dst = uint16(v)
	return nil
}

func applyFloat(value string, dst *float64) error {
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

func applyBool(value string, dst *bool) error {
	v, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}
