package metrics

import (
	"regexp"

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

	BytesReceived = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_bytes_received_total",
		Help: "Total bytes received from clients",
	})

	// ── Broadcast ─────────────────────────────────────────────────────────────
	BroadcastsDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_broadcasts_dropped_total",
		Help: "Total broadcast messages dropped (send channel full)",
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

	AdaptiveBatchIntervalMs = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "game_adaptive_batch_interval_ms",
		Help: "Current adaptive batch interval in milliseconds for broadcast pacing",
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
)
