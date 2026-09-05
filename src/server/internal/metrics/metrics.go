package metrics

import (
	"regexp"

	"pixi_game_server/internal/clock"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

func init() {
	// Дефолтный GoCollector вызывает runtime.ReadMemStats() — это Stop-The-World.
	// При scrape_interval=5s это даёт спайки p99 10-50ms в game loop.
	// Заменяем на STW-free коллектор через runtime/metrics API (Go 1.17+).
	prometheus.Unregister(collectors.NewGoCollector())
	prometheus.MustRegister(collectors.NewGoCollector(
		collectors.WithGoCollectorRuntimeMetrics(
			collectors.GoRuntimeMetricsRule{Matcher: regexp.MustCompile(`.*`)},
		),
	))
}

var (
	WallClockOffset = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "game_wall_clock_offset_seconds",
		Help: "Wall-clock drift/corrections relative to monotonic elapsed time since process start",
	}, func() float64 { return clock.WallOffset().Seconds() })
	TimeDilationChanges = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_time_dilation_changes_total",
		Help: "Simulation speed changes, including transitions shorter than a scrape interval",
	}, []string{"direction"})
	WritePressureFraction = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "game_write_pressure_fraction",
		Help: "Fraction of connected recipients with recent or in-flight state age above threshold",
	}, []string{"threshold"})
	TickStartDelay = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_tick_start_delay_seconds",
		Help:    "Monotonic delay between scheduled tick and game-loop execution",
		Buckets: prometheus.ExponentialBucketsRange(0.00001, 1, 20),
	})
	// ── Players ──────────────────────────────────────────────────────────────
	PlayersConnected = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "game_players_connected",
		Help: "Current number of connected players",
	})

	ConnectionsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_connections_total",
		Help: "Total number of WebSocket connections ever established",
	})

	DisconnectionsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_disconnections_total",
		Help: "Total number of WebSocket disconnections",
	})

	SessionDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_session_duration_seconds",
		Help:    "Player session duration in seconds",
		Buckets: []float64{5, 30, 60, 300, 600, 1800, 3600},
	})

	// ── Game loop ─────────────────────────────────────────────────────────────
	TickDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_tick_duration_seconds",
		Help:    "Time spent processing a single game tick",
		Buckets: prometheus.ExponentialBucketsRange(0.0001, 0.5, 14),
	})

	TicksTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_ticks_total",
		Help: "Total number of game ticks processed",
	})

	// ── Events ───────────────────────────────────────────────────────────────
	EventsProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_events_processed_total",
		Help: "Total game events processed, by type",
	}, []string{"type"})

	// ── Messages ─────────────────────────────────────────────────────────────
	MessagesReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_messages_received_total",
		Help: "Total messages received from clients, by type",
	}, []string{"type"})

	MessagesRateLimited = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_messages_rate_limited_total",
		Help: "Total messages dropped due to per-connection rate limiting",
	})

	MovementInputsRejected = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_movement_inputs_rejected_total",
		Help: "Movement transitions rejected due to stale/gapped sequence, invalid client tick, or a full ring",
	})

	MovementTransitionsLate = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_movement_transitions_late_total",
		Help: "Movement transitions applied after their scheduled server tick",
	})

	MovementTransitionLatenessTicks = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_movement_transition_lateness_ticks",
		Help:    "How many server ticks late a movement transition was applied",
		Buckets: []float64{0, 1, 2, 3, 4, 6, 8, 12, 20},
	})

	MovementCorrectionDistance = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_movement_correction_distance_units",
		Help:    "Authoritative distance corrected when a late transition closes a segment",
		Buckets: []float64{0, 1, 2, 4, 8, 12, 16, 24, 32, 48, 64, 128},
	})

	MovementTimelineRebases = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_movement_timeline_rebases_total",
		Help: "Excessively late movement timelines rebased without historical position rollback",
	})

	BytesReceived = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_bytes_received_total",
		Help: "Total bytes received from clients",
	})

	// ── Broadcast ─────────────────────────────────────────────────────────────
	BroadcastsDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_broadcasts_dropped_total",
		Help: "Total broadcast messages dropped (send channel full)",
	})

	BroadcastsShed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_broadcasts_shed_total",
		Help: "Total world-state broadcasts skipped by queue-aware fanout shedding",
	})

	BytesSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_bytes_sent_total",
		Help: "Total bytes sent to clients",
	})

	BroadcastPayloadBytes = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_broadcast_payload_bytes",
		Help:    "Encoded payload size (without WebSocket header) for each broadcast tick",
		Buckets: []float64{64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536, 131072},
	})

	BroadcastTargets = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_broadcast_targets",
		Help:    "Number of active connections fanned out to in each broadcast tick",
		Buckets: []float64{1, 10, 50, 100, 250, 500, 1000, 2000, 5000, 10000},
	})

	BroadcastRecipients = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_broadcast_recipients",
		Help:    "Number of recipients selected for world-state broadcast in each tick",
		Buckets: []float64{1, 10, 50, 100, 250, 500, 1000, 2000, 5000, 10000},
	})

	BroadcastOverdueRecipients = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_broadcast_overdue_recipients",
		Help:    "Number of recipients selected due to staleness deadline in each tick",
		Buckets: []float64{0, 1, 10, 50, 100, 250, 500, 1000, 2000, 5000},
	})

	BroadcastDeferred = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_broadcast_deferred_total",
		Help: "Total number of connections deferred to future ticks by recipient scheduler",
	})

	BroadcastBudgetTrimmed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_broadcast_budget_trimmed_total",
		Help: "Total number of recipients deferred due to fanout byte budget limits",
	})

	BroadcastBudgetHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_broadcast_budget_hits_total",
		Help: "Total number of ticks where fanout byte budget limited recipient count",
	})

	BroadcastBudgetRecipients = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_broadcast_budget_recipients",
		Help:    "Effective recipient cap imposed by fanout byte budget in each tick",
		Buckets: []float64{1, 10, 50, 100, 250, 500, 1000, 2000, 5000, 10000},
	})

	FanoutRecipientLimit = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "game_fanout_recipient_limit",
		Help: "Current adaptive recipient limit for world-state fanout per tick (0 means unlimited)",
	})

	// ── WebSocket errors ──────────────────────────────────────────────────────
	WSUpgradeErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_ws_upgrade_errors_total",
		Help: "Total WebSocket upgrade failures",
	})

	WSReadErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_ws_read_errors_total",
		Help: "Total unexpected WebSocket read errors",
	})

	WSWriteErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_ws_write_errors_total",
		Help: "Total WebSocket write errors",
	})

	// ── Connection rate limiting ───────────────────────────────────────────────
	IPRateLimited = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_ip_rate_limited_total",
		Help: "Total connection attempts rejected by IP rate limiter",
	})

	// ── Tick phase breakdown ──────────────────────────────────────────────────
	// Labels: "world_step" (snapshot + movement update + state build),
	//         "range" (legacy alias), "delta" (prevStates diff),
	//         "encode" (binary state encoding), "fanout_send" (broadcast enqueue).
	// Sum of all four ≈ total tick duration.
	TickPhaseDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "game_tick_phase_seconds",
		Help:    "Time spent in each phase of the game tick",
		Buckets: prometheus.ExponentialBucketsRange(0.00005, 0.25, 14),
	}, []string{"phase"})

	TickWorldStepDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_tick_world_step_seconds",
		Help:    "Time spent in world step phase (snapshot + movement + state collection)",
		Buckets: prometheus.ExponentialBucketsRange(0.00005, 0.25, 14),
	})

	TickFanoutDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_tick_fanout_send_seconds",
		Help:    "Time spent enqueueing broadcast jobs to per-connection write queues",
		Buckets: prometheus.ExponentialBucketsRange(0.00005, 0.25, 14),
	})

	TickFanoutSelectDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_tick_fanout_select_seconds",
		Help:    "Time spent selecting broadcast recipients for the current tick",
		Buckets: prometheus.ExponentialBucketsRange(0.00001, 0.1, 14),
	})

	TickFanoutEnqueueDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_tick_fanout_enqueue_seconds",
		Help:    "Time spent enqueueing selected recipients in fanout workers/write queues",
		Buckets: prometheus.ExponentialBucketsRange(0.00001, 0.1, 14),
	})

	WSWriteBatchDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_ws_write_batch_seconds",
		Help:    "Duration of one batched socket write in the per-connection write loop",
		Buckets: prometheus.ExponentialBucketsRange(0.00001, 0.25, 14),
	})

	WSWriteBatchJobs = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_ws_write_batch_jobs",
		Help:    "Number of queued write jobs coalesced into one socket write call",
		Buckets: []float64{1, 2, 4, 8, 16, 32, 64},
	})

	WSWriteQueueDepth = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_ws_write_queue_depth",
		Help:    "Observed per-connection write queue depth during world-state enqueue",
		Buckets: []float64{0, 1, 2, 4, 6, 8, 12, 16, 24, 32},
	})

	WorldStateQueueDelay = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_world_state_queue_delay_seconds",
		Help:    "Time a world-state frame waits in a per-connection queue before writing",
		Buckets: prometheus.ExponentialBucketsRange(0.00001, 1, 16),
	})

	WorldStateAgeAtWriteStart = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_world_state_age_at_write_start_seconds",
		Help:    "World-state age when the socket write starts",
		Buckets: prometheus.ExponentialBucketsRange(0.00001, 1, 16),
	})

	WorldStateAgeAtWriteEnd = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_world_state_age_at_write_end_seconds",
		Help:    "World-state age when the socket write completes",
		Buckets: prometheus.ExponentialBucketsRange(0.00001, 1, 16),
	})

	TimeDilationPercent = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "game_time_dilation_percent",
		Help: "Current simulation time scale (100 = nominal tick rate, EVE-style TiDi)",
	})

	TickIntervalMs = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "game_tick_interval_ms",
		Help: "Current (possibly dilated) simulation tick period in milliseconds",
	})

	// ── Delta tracking ────────────────────────────────────────────────────────
	// How many players actually had state changes this tick.
	// If this equals PlayersConnected every tick — delta optimisation does nothing.
	DeltaPlayersCount = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_delta_players_count",
		Help:    "Number of players with changed state per tick",
		Buckets: []float64{0, 10, 50, 100, 250, 500, 1000, 2000, 5000},
	})

	// Fraction of players that changed state (0.0–1.0).
	// 1.0 on a fullSync tick or when everyone is moving.
	DeltaRatio = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "game_delta_ratio",
		Help: "Fraction of players with changed state in the last tick (0.0–1.0)",
	})

	// The three metrics below size the payoff of velocity replication before it is
	// built. Position is a deterministic function of velocity on both sides, so a
	// client could dead-reckon every record counted by DeltaPositionOnly instead of
	// receiving it. DeltaVectorChanges plus DeltaClampedPlayers is the traffic that
	// would remain: input the client cannot predict, and boundary clamps that break
	// the prediction.
	DeltaVectorChanges = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_delta_vector_changes",
		Help:    "Broadcast records a dead-reckoning client could not predict (velocity/state/facing changed)",
		Buckets: []float64{0, 10, 50, 100, 250, 500, 1000, 2000, 5000},
	})

	DeltaPositionOnly = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_delta_position_only",
		Help:    "Broadcast records present only because position advanced predictably — removable by velocity replication",
		Buckets: []float64{0, 10, 50, 100, 250, 500, 1000, 2000, 5000},
	})

	DeltaClampedPlayers = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_delta_clamped_players",
		Help:    "Broadcast records where a moving player did not advance (world-boundary clamp), which dead reckoning would mispredict",
		Buckets: []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 256},
	})

	BroadcastRecords = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_broadcast_records",
		Help:    "Player records actually placed on the wire per broadcast frame",
		Buckets: []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 5000},
	})

	DeltaKeyframes = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "game_delta_keyframes",
		Help:    "Records added purely by keyframe rotation so clients converge after a missed record",
		Buckets: []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 256},
	})

	DeltaPredictableRatio = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "game_delta_predictable_ratio",
		Help: "Fraction of the last broadcast delta a dead-reckoning client could have predicted (0.0–1.0)",
	})
)
