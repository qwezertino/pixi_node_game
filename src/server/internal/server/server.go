package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* handlers on DefaultServeMux
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gobwas/ws"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"

	"pixi_game_server/internal/clock"
	"pixi_game_server/internal/config"
	"pixi_game_server/internal/game"
	"pixi_game_server/internal/metrics"
	"pixi_game_server/internal/protocol"
	"pixi_game_server/internal/types"
)

// Server основной сервер игры
type Server struct {
	cfg       *config.Config
	gameWorld *game.GameWorld
	protocol  *protocol.BinaryProtocol

	// Connection management
	connectionsMu sync.RWMutex
	connections   map[uint32]*Connection // playerID → *Connection
	rh            readHandler            // epoll (Linux) or goroutine-per-conn (other) read strategy

	// Rate limiting
	rateLimiters sync.Map // map[string]*rate.Limiter

	// Server state
	ctx        context.Context
	cancel     context.CancelFunc
	httpServer *http.Server

	// Throttled diagnostics
	lastSlowFanoutLog  int64 // atomic monotonic timestamp
	lastDilationLog    int64 // atomic monotonic timestamp
	lastFrameRejectLog int64 // atomic monotonic timestamp

	// Time dilation (EVE-style TiDi): replaces the old silent replication-interval
	// backoff. dilationBps is basis points, 10000 = 100% (nominal), floor at
	// minDilationBps. When it changes, GameWorld's tick ticker itself is slowed via
	// SetTickInterval — see world.go — so replication cadence follows the simulation
	// rate automatically instead of being paced separately.
	dilationBps            int64 // atomic
	dilationSevereStreak   int64 // atomic consecutive-tick counter, debounces step-downs against single-tick jitter
	dilationModerateStreak int64 // atomic
	worldStateSeq          uint32

	// Fanout/write controls
	fanoutDropLimit                int32
	writeBatchSize                 int
	fanoutMaxBroadcastBytesPerTick int
	fanoutQueueShedDepth           int
	fanoutFairDebtMax              int32
	fanoutFairDebtInc              int32
	fanoutFairDebtDec              int32
	fanoutFairDebtWeightNs         int64
	fanoutRoundRobinWeightNs       int64
	fanoutCriticalWindowNs         int64
	fanoutCriticalBoostNs          int64
	fanoutRoundRobinEpoch          int64 // atomic cursor for tie-breaking fairness
	fanoutDebtEpoch                uint32

	// Adaptive recipient fanout scheduling
	fanoutMinRecipients  int
	fanoutMaxRecipients  int
	fanoutTarget         time.Duration
	fanoutRecipientLimit int64 // atomic
	activeStalenessNs    int64
	idleStalenessNs      int64
	activeWindowNs       int64
	lastFanoutTuneLog    int64 // atomic monotonic timestamp

	// Performance monitoring
	startTime time.Time
}

// Connection represents a WebSocket client connection.
// rawConn is the hijacked net.Conn returned by gobwas/ws after the HTTP upgrade.
//
// Write path: all writes are sent to writeCh and processed by a single persistent
// write-loop goroutine (startWriteLoop). Because only one goroutine writes to rawConn,
// no write mutex is needed.
//
// Lifecycle: cleanupConnection is guaranteed to run exactly once via closeOnce.
type Connection struct {
	player               *types.Player
	rawConn              net.Conn
	fd                   int // OS file descriptor (used by epoll remove)
	rateLimiter          *rate.Limiter
	writeCh              chan writeJob // buffered channel drained by startWriteLoop goroutine
	closeOnce            sync.Once     // ensures cleanupConnection body runs once
	lastActivity         int64         // monotonic ns, updated on each received frame (atomic)
	writeFailures        int32         // consecutive write timeouts/errors (atomic); reset on success
	fanoutDrops          int32         // consecutive dropped broadcast enqueues (atomic)
	fanoutFairDebt       int32         // anti-starvation debt for recipient selection fairness (atomic)
	fanoutDebtEpoch      uint32        // marks whether conn was selected in the current fairness epoch
	pendingStateNs       int64         // monotonic creation time of queued/in-flight world state (atomic)
	lastWriteAgeNs       int64         // completed world-state age (atomic)
	lastWriteObservedNs  int64         // monotonic time of age observation (atomic)
	pendingBroadcast     int32         // 0/1: whether a world-state broadcast job is already queued/in-flight
	lastMovementAckSeq   uint32        // latest authoritative input sequence queued to the writer
	staleInputStreak     int32         // consecutive movement inputs rejected as duplicate/out-of-order
	lastWorldStateSentNs int64         // monotonic timestamp of last successfully written world-state frame
	criticalUntilNs      int64         // monotonic ns until which this client receives criticality boost
	ctx                  context.Context
	cancel               context.CancelFunc
}

// New создает новый сервер
func New(cfg *config.Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	// Auto-detect worker count
	if cfg.Server.Workers == 0 {
		cfg.Server.Workers = runtime.NumCPU()
	}

	server := &Server{
		cfg:         cfg,
		gameWorld:   game.NewGameWorld(cfg),
		protocol:    &protocol.BinaryProtocol{},
		connections: make(map[uint32]*Connection, 4096),
		ctx:         ctx,
		cancel:      cancel,
		startTime:   time.Now(),
	}

	server.dilationBps = dilationBpsFull
	metrics.TimeDilationPercent.Set(100)
	metrics.TickIntervalMs.Set(float64(server.gameWorld.GetNominalTickInterval().Milliseconds()))

	server.fanoutDropLimit = int32(cfg.Net.FanoutDropStreak)
	if server.fanoutDropLimit < 1 {
		server.fanoutDropLimit = 1
	}
	server.writeBatchSize = cfg.Net.WriteBatchSize
	if server.writeBatchSize < 1 {
		server.writeBatchSize = 1
	}
	server.fanoutMaxBroadcastBytesPerTick = cfg.Net.FanoutMaxBroadcastBytesPerTick
	if server.fanoutMaxBroadcastBytesPerTick < 0 {
		server.fanoutMaxBroadcastBytesPerTick = 0
	}
	server.fanoutQueueShedDepth = cfg.Net.FanoutQueueShedDepth
	if server.fanoutQueueShedDepth < 1 {
		server.fanoutQueueShedDepth = 0
	}
	server.fanoutFairDebtMax = int32(cfg.Net.FanoutFairDebtMax)
	if server.fanoutFairDebtMax < 0 {
		server.fanoutFairDebtMax = 0
	}
	server.fanoutFairDebtInc = int32(cfg.Net.FanoutFairDebtInc)
	if server.fanoutFairDebtInc < 0 {
		server.fanoutFairDebtInc = 0
	}
	server.fanoutFairDebtDec = int32(cfg.Net.FanoutFairDebtDec)
	if server.fanoutFairDebtDec < 0 {
		server.fanoutFairDebtDec = 0
	}
	server.fanoutFairDebtWeightNs = cfg.Net.FanoutFairDebtWeightNs
	if server.fanoutFairDebtWeightNs < 0 {
		server.fanoutFairDebtWeightNs = 0
	}
	server.fanoutRoundRobinWeightNs = cfg.Net.FanoutRoundRobinWeightNs
	if server.fanoutRoundRobinWeightNs < 0 {
		server.fanoutRoundRobinWeightNs = 0
	}
	server.fanoutCriticalWindowNs = cfg.Net.FanoutCriticalWindow.Nanoseconds()
	if server.fanoutCriticalWindowNs < 0 {
		server.fanoutCriticalWindowNs = 0
	}
	server.fanoutCriticalBoostNs = cfg.Net.FanoutCriticalBoostNs
	if server.fanoutCriticalBoostNs < 0 {
		server.fanoutCriticalBoostNs = 0
	}

	server.fanoutMinRecipients = cfg.Net.FanoutMinRecipientsPerTick
	if server.fanoutMinRecipients < 1 {
		server.fanoutMinRecipients = 1
	}
	server.fanoutMaxRecipients = cfg.Net.FanoutMaxRecipientsPerTick
	if server.fanoutMaxRecipients > 0 && server.fanoutMinRecipients > server.fanoutMaxRecipients {
		server.fanoutMinRecipients = server.fanoutMaxRecipients
	}
	server.fanoutTarget = time.Duration(cfg.Net.FanoutTargetMs) * time.Millisecond
	if server.fanoutTarget <= 0 {
		server.fanoutTarget = 12 * time.Millisecond
	}
	server.activeStalenessNs = cfg.Net.WorldStateActiveStaleness.Nanoseconds()
	if server.activeStalenessNs <= 0 {
		server.activeStalenessNs = (150 * time.Millisecond).Nanoseconds()
	}
	server.idleStalenessNs = cfg.Net.WorldStateIdleStaleness.Nanoseconds()
	if server.idleStalenessNs < server.activeStalenessNs {
		server.idleStalenessNs = server.activeStalenessNs
	}
	server.activeWindowNs = cfg.Net.WorldStateActiveWindow.Nanoseconds()
	if server.activeWindowNs <= 0 {
		server.activeWindowNs = (1 * time.Second).Nanoseconds()
	}
	if server.fanoutMaxRecipients > 0 {
		atomic.StoreInt64(&server.fanoutRecipientLimit, int64(server.fanoutMaxRecipients))
		metrics.FanoutRecipientLimit.Set(float64(server.fanoutMaxRecipients))
	} else {
		metrics.FanoutRecipientLimit.Set(0)
	}

	// Start ping/keepalive loop (replaces per-shard ping ticker).
	go server.runPingLoop()

	// Инициализируем read-хендлер (epoll на Linux, goroutine на других платформах).
	server.rh = newReadHandler(server)

	// Регистрируем tick-driven broadcast: состояние кодируется один раз в тик, разосылается всем.
	server.gameWorld.SetTickBroadcaster(server.broadcastTick)

	// Start performance monitoring
	go server.performanceMonitor()

	return server
}

// Start запускает сервер
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// WebSocket endpoint
	mux.HandleFunc("/ws", s.handleWebSocket)

	// Static files
	mux.Handle("/", http.FileServer(http.Dir(s.cfg.Server.StaticDir)))

	// Health check
	mux.HandleFunc("/health", s.handleHealth)

	// Metrics endpoint (Prometheus format)
	mux.Handle("/metrics", promhttp.Handler())

	// Legacy JSON metrics for backwards compat
	mux.HandleFunc("/metrics/json", s.handleMetricsJSON)

	// pprof endpoints — /debug/pprof/, /debug/pprof/trace, /debug/pprof/block etc.
	// Block/mutex profiling enabled only when PPROF_BLOCK_RATE=1 (adds 10-30% CPU overhead).
	if os.Getenv("PPROF_BLOCK_RATE") == "1" {
		runtime.SetBlockProfileRate(1)     // record every blocking event
		runtime.SetMutexProfileFraction(1) // record every mutex contention event
	}
	mux.Handle("/debug/pprof/", http.DefaultServeMux)
	mux.Handle("/debug/pprof/cmdline", http.DefaultServeMux)
	mux.Handle("/debug/pprof/profile", http.DefaultServeMux)
	mux.Handle("/debug/pprof/symbol", http.DefaultServeMux)
	mux.Handle("/debug/pprof/trace", http.DefaultServeMux)

	// Periodically purge stale per-IP rate limiters to prevent unbounded memory growth.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.rateLimiters.Range(func(k, _ any) bool {
					s.rateLimiters.Delete(k)
					return true
				})
			}
		}
	}()

	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)

	slog.Info("server listening", "addr", addr)
	slog.Info("serving static files", "dir", s.cfg.Server.StaticDir)

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown stops accepting new connections, drains in-flight HTTP requests, then
// stops the simulation. Order matters: the world must outlive the listener so that
// a tick already in flight still finds a coherent connection map.
func (s *Server) Shutdown(ctx context.Context) error {
	var err error
	if s.httpServer != nil {
		err = s.httpServer.Shutdown(ctx)
	}

	// Cancel per-connection contexts so write loops exit instead of blocking on a
	// receive, then stop the tick loop and its workers.
	s.connectionsMu.RLock()
	conns := make([]*Connection, 0, len(s.connections))
	for _, conn := range s.connections {
		conns = append(conns, conn)
	}
	s.connectionsMu.RUnlock()
	for _, conn := range conns {
		conn.cancel()
	}

	s.cancel()
	s.gameWorld.Stop()
	slog.Info("server stopped", "drained_connections", len(conns))
	return err
}

const (
	maxClientFramePayload int64 = 125

	// maxStaleInputStreak — consecutive duplicate/old transitions tolerated before the
	// connection is considered non-conforming.
	maxStaleInputStreak int32 = 120
)

// validClientHeader enforces the subset of RFC 6455 used by the game protocol.
// Fragmented messages and text frames are deliberately unsupported: every client
// command is a single, small binary frame.
func validClientHeader(h ws.Header) bool {
	if !h.Masked || !h.Fin || h.Rsv != 0 || h.Length < 0 || h.Length > maxClientFramePayload {
		return false
	}
	switch h.OpCode {
	case ws.OpBinary, ws.OpClose, ws.OpPing, ws.OpPong:
		return true
	default:
		return false
	}
}

// clientHeaderRejectReason explains a validClientHeader failure. Cold path only —
// callers must gate it behind the boolean check so the hot path stays branch-free.
func clientHeaderRejectReason(h ws.Header) string {
	switch {
	case !h.Masked:
		return "unmasked_client_frame"
	case !h.Fin:
		return "fragmented_frame"
	case h.Rsv != 0:
		return "reserved_bits_set"
	case h.Length < 0 || h.Length > maxClientFramePayload:
		return "payload_too_large"
	default:
		return "unsupported_opcode"
	}
}

// logRejectedFrame reports at most one rejected frame per second across the whole
// server. Without it a single misbehaving client would flood the log, and without
// any logging at all a protocol-level disconnect is undiagnosable in production.
func (s *Server) logRejectedFrame(c *Connection, hdr ws.Header) {
	now := clock.Now()
	last := atomic.LoadInt64(&s.lastFrameRejectLog)
	if now-last < int64(time.Second) {
		return
	}
	if !atomic.CompareAndSwapInt64(&s.lastFrameRejectLog, last, now) {
		return
	}
	slog.Warn("rejected client frame",
		"player_id", c.player.ID,
		"reason", clientHeaderRejectReason(hdr),
		"opcode", hdr.OpCode,
		"length", hdr.Length,
		"masked", hdr.Masked,
		"fin", hdr.Fin)
}

// handleWebSocket обрабатывает WebSocket соединения
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Check connection limit before doing anything else.
	s.connectionsMu.RLock()
	connCount := len(s.connections)
	s.connectionsMu.RUnlock()
	if connCount >= s.cfg.Net.MaxConnections {
		http.Error(w, "Server full", http.StatusServiceUnavailable)
		return
	}

	// Rate limiting by IP (RemoteAddr includes port — extract host only).
	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		clientIP = r.RemoteAddr // fallback for unix sockets / tests
	}
	limiter := s.getOrCreateRateLimiter(clientIP)

	if !limiter.Allow() {
		metrics.IPRateLimited.Inc()
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	// Upgrade to WebSocket via gobwas/ws (hijacks the HTTP conn; no per-conn goroutine spawned).
	// ws.UpgradeHTTP performs the Upgrade handshake and returns the hijacked net.Conn.
	// Any origin is accepted (development / same-origin proxied).
	rawConn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err, "remote_addr", r.RemoteAddr)
		metrics.WSUpgradeErrors.Inc()
		return
	}

	// Create player and connection
	player := s.gameWorld.AddPlayer()
	connection := s.createConnection(player, rawConn)

	// Explicit identity must precede all state/event messages for this connection.
	s.sendWelcome(connection)

	// Send initial state BEFORE adding to s.connections so that the write loop
	// delivers the full world snapshot as the very first message the client
	// receives. If we add to the map first, a world tick can race here and
	// enqueue a delta/gamestate frame ahead of the initial state.
	s.sendInitialState(connection)

	s.connectionsMu.Lock()
	s.connections[player.ID] = connection
	s.connectionsMu.Unlock()

	// Existing clients discover the new player in the next world-state delta.
	// A separate PLAYER_JOINED broadcast duplicates that information and creates
	// O(N²) control messages during a connection ramp, delaying state frames.

	// Update metrics
	metrics.ConnectionsTotal.Inc()
	metrics.PlayersConnected.Inc()

	// Register with the read handler (epoll on Linux; goroutine on other platforms).
	// No handleConnection goroutine is spawned here — this is the key change that
	// reduces goroutine count from 2400 to ~2×GOMAXPROCS at 2400 clients.
	s.rh.register(s, connection)
}

// createConnection creates a new connection and starts its write-loop goroutine.
func (s *Server) createConnection(player *types.Player, rawConn net.Conn) *Connection {
	ctx, cancel := context.WithCancel(s.ctx)

	conn := &Connection{
		player:  player,
		rawConn: rawConn,
		writeCh: make(chan writeJob, writeChanSize),
		rateLimiter: rate.NewLimiter(
			rate.Limit(s.cfg.Net.MessageRateLimit),
			s.cfg.Net.BurstLimit,
		),
		lastActivity:         clock.Now(),
		lastWorldStateSentNs: clock.Now(),
		ctx:                  ctx,
		cancel:               cancel,
	}
	s.startWriteLoop(conn)
	return conn
}

// processMessage обрабатывает сообщение от клиента
func (s *Server) processMessage(connection *Connection, message []byte) {
	clientMsg, err := s.protocol.DecodeClientMessage(message)
	if err != nil {
		slog.Error("message decode failed", "player_id", connection.player.ID, "error", err)
		return
	}

	connection.player.IncrementMessageCount()

	switch clientMsg.Type {
	case protocol.MessageMove:
		metrics.MessagesReceived.WithLabelValues("move").Inc()
		s.markConnectionCritical(connection)

		result := s.gameWorld.QueueMovementInput(
			connection.player.ID,
			clientMsg.MovementVector.DX,
			clientMsg.MovementVector.DY,
			clientMsg.InputSequence,
		)
		switch result {
		case types.InputAccepted:
			atomic.StoreInt32(&connection.staleInputStreak, 0)

		case types.InputStale:
			// Idempotently ignore a duplicate/old transition. WebSocket preserves
			// order, so a long stale streak indicates a non-conforming sender.
			metrics.MovementInputsRejected.Inc()
			slog.Warn("movement input rejected", "player_id", connection.player.ID, "reason", result.String(), "sequence", clientMsg.InputSequence)
			if atomic.AddInt32(&connection.staleInputStreak, 1) >= maxStaleInputStreak {
				go s.cleanupConnection(connection)
			}
			return

		default:
			// A sequence gap or invalid sample indicates a broken client stream.
			metrics.MovementInputsRejected.Inc()
			slog.Warn("movement input rejected", "player_id", connection.player.ID, "reason", result.String(), "sequence", clientMsg.InputSequence)
			go s.cleanupConnection(connection)
			return
		}

		// ACK is emitted after the world worker has reconstructed the segment boundary.

	case protocol.MessageDirection:
		metrics.MessagesReceived.WithLabelValues("direction").Inc()
		s.markConnectionCritical(connection)
		s.gameWorld.ProcessEvent(types.GameEvent{
			PlayerID:    connection.player.ID,
			Type:        types.EventFace,
			FacingRight: clientMsg.Direction,
		})
		// Обновление направления разошлётся через tick broadcast.

	case protocol.MessageAttack:
		metrics.MessagesReceived.WithLabelValues("attack").Inc()
		s.markConnectionCritical(connection)
		s.gameWorld.TryAttack(connection.player.ID)
		// State=1 будет разослан всем через tick broadcast.

	case protocol.MessageAttackEnd:
		// Ignored: server is authoritative on attack duration.

	case protocol.MessageViewportUpdate:
		// Silently accepted — viewport-based culling not yet implemented.

	case protocol.MessageSyncRequest:
		metrics.MessagesReceived.WithLabelValues("sync_request").Inc()
		s.sendInitialState(connection)

	case protocol.MessagePing:
		metrics.MessagesReceived.WithLabelValues("ping").Inc()
		s.sendPong(connection, clientMsg.Nonce)
	}
}

func (s *Server) markConnectionCritical(conn *Connection) {
	if s.fanoutCriticalWindowNs <= 0 {
		return
	}
	nowNs := clock.Now()
	untilNs := nowNs + s.fanoutCriticalWindowNs
	for {
		curr := atomic.LoadInt64(&conn.criticalUntilNs)
		if curr >= untilNs {
			return
		}
		if atomic.CompareAndSwapInt64(&conn.criticalUntilNs, curr, untilNs) {
			return
		}
	}
}

// cleanupConnection очищает соединение. Guaranteed idempotent via closeOnce.
func (s *Server) cleanupConnection(c *Connection) {
	c.closeOnce.Do(func() {
		playerID := c.player.ID

		metrics.DisconnectionsTotal.Inc()
		metrics.PlayersConnected.Dec()
		metrics.SessionDuration.Observe(time.Since(c.player.JoinTime).Seconds())

		// Stop epoll watching (must happen before rawConn.Close).
		s.rh.remove(c)

		// Remove from connections map BEFORE cancelling ctx so that broadcastTick
		// cannot enqueue a new writeJob after the write loop exits (which would
		// leave a tickFrame ref unreleased or panic on a send to a closed channel).
		s.connectionsMu.Lock()
		delete(s.connections, playerID)
		s.connectionsMu.Unlock()

		// Notify other players that this player left (after map removal so the
		// departing connection does not receive its own leave notification).
		s.notifyPlayerLeft(playerID)

		// Cancel ctx → if the write-loop goroutine is still running, it will
		// receive ctx.Done() and call drainWriteCh before exiting.
		// If the write loop already exited (maxWriteFailures path), cancel is a
		// no-op for the goroutine, but we still drain here to release any tickFrame
		// refs that arrived in writeCh after the write loop drained and before
		// the map removal above completed (a narrow race window).
		c.cancel()
		drainWriteCh(c.writeCh)
		// Close the raw connection so any in-progress Write returns immediately.
		c.rawConn.Close()

		s.gameWorld.RemovePlayer(playerID)
	})
}

// getOrCreateRateLimiter получает или создает rate limiter для IP.
// Uses LoadOrStore to avoid the Load+Store TOCTOU race under concurrent connections.
// If cfg.Net.IPConnRate == 0, rate limiting is disabled (returns an infinite limiter).
func (s *Server) getOrCreateRateLimiter(ip string) *rate.Limiter {
	limit := rate.Limit(s.cfg.Net.IPConnRate)
	burst := s.cfg.Net.IPConnBurst
	if limit <= 0 {
		limit = rate.Inf
		burst = 0
	}
	newLimiter := rate.NewLimiter(limit, burst)
	if actual, loaded := s.rateLimiters.LoadOrStore(ip, newLimiter); loaded {
		return actual.(*rate.Limiter)
	}
	return newLimiter
}

// performanceMonitor мониторит производительность
func (s *Server) performanceMonitor() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return

		case <-ticker.C:
			s.logPerformanceStats()
		}
	}
}

// logPerformanceStats логирует статистику производительности
func (s *Server) logPerformanceStats() {
	// Metrics are exposed via /metrics (Prometheus). Periodic log removed.
}

// handleHealth обрабатывает health check
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"healthy","uptime_seconds":%d,"players":%d}`,
		int(time.Since(s.startTime).Seconds()),
		s.gameWorld.GetPlayerCount())
}

// handleMetricsJSON обрабатывает запрос метрик в JSON (legacy)
func (s *Server) handleMetricsJSON(w http.ResponseWriter, r *http.Request) {
	m := s.gameWorld.GetMetrics()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{
		"players": %d,
		"tick_duration_ns": %d,
		"uptime_seconds": %d,
		"goroutines": %d,
		"heap_alloc_mb": %d
	}`,
		m.ConnectedPlayers,
		m.TickDuration.Nanoseconds(),
		int(time.Since(s.startTime).Seconds()),
		runtime.NumGoroutine(),
		mem.HeapAlloc/1024/1024)
}
