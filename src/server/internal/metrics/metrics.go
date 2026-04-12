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
		Buckets: prometheus.ExponentialBucketsRange(0.0001, 0.1, 12),
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

	EventsDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_events_dropped_total",
		Help: "Total events dropped due to full event channel",
	})

	EventChannelLen = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "game_event_channel_len",
		Help: "Current number of events queued in the event channel",
	})

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
	BroadcastsSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_broadcasts_sent_total",
		Help: "Total broadcast messages delivered to individual connections",
	})

	BroadcastsDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_broadcasts_dropped_total",
		Help: "Total broadcast messages dropped (send channel full)",
	})

	BroadcastChannelLen = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "game_broadcast_channel_len",
		Help: "Current number of pending broadcast jobs",
	})

	BytesSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_bytes_sent_total",
		Help: "Total bytes sent to clients",
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

	SendChannelDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_send_channel_dropped_total",
		Help: "Total messages dropped due to full per-player send channel",
	})

	// ── Connection rate limiting ───────────────────────────────────────────────
	IPRateLimited = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_ip_rate_limited_total",
		Help: "Total connection attempts rejected by IP rate limiter",
	})
)
