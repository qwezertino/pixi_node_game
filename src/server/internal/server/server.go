package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof"
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

type Server struct {
	cfg       *config.Config
	gameWorld *game.GameWorld
	protocol  *protocol.BinaryProtocol

	connectionsMu sync.RWMutex
	connections   map[uint32]*Connection
	rh            readHandler

	rateLimiters sync.Map

	ctx        context.Context
	cancel     context.CancelFunc
	httpServer *http.Server

	lastSlowFanoutLog  int64
	lastDilationLog    int64
	lastFrameRejectLog int64

	dilationBps            int64
	dilationSevereStreak   int64
	dilationModerateStreak int64
	worldStateSeq          uint32

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
	fanoutRoundRobinEpoch          int64
	fanoutDebtEpoch                uint32

	fanoutMinRecipients  int
	fanoutMaxRecipients  int
	fanoutTarget         time.Duration
	fanoutRecipientLimit int64
	activeStalenessNs    int64
	idleStalenessNs      int64
	activeWindowNs       int64
	lastFanoutTuneLog    int64

	startTime time.Time
}

type Connection struct {
	player               *types.Player
	rawConn              net.Conn
	fd                   int
	rateLimiter          *rate.Limiter
	writeCh              chan writeJob
	closeOnce            sync.Once
	lastActivity         int64
	writeFailures        int32
	fanoutDrops          int32
	fanoutFairDebt       int32
	fanoutDebtEpoch      uint32
	pendingStateNs       int64
	lastWriteAgeNs       int64
	lastWriteObservedNs  int64
	pendingBroadcast     int32
	lastMovementAckSeq   uint32
	staleInputStreak     int32
	lastWorldStateSentNs int64
	criticalUntilNs      int64
	ctx                  context.Context
	cancel               context.CancelFunc
}

func New(cfg *config.Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())

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

	go server.runPingLoop()

	server.rh = newReadHandler(server)

	server.gameWorld.SetTickBroadcaster(server.broadcastTick)

	go server.performanceMonitor()

	return server
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/ws", s.handleWebSocket)

	mux.Handle("/", http.FileServer(http.Dir(s.cfg.Server.StaticDir)))

	mux.HandleFunc("/health", s.handleHealth)

	mux.Handle("/metrics", promhttp.Handler())

	mux.HandleFunc("/metrics/json", s.handleMetricsJSON)

	if os.Getenv("PPROF_BLOCK_RATE") == "1" {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
	}
	mux.Handle("/debug/pprof/", http.DefaultServeMux)
	mux.Handle("/debug/pprof/cmdline", http.DefaultServeMux)
	mux.Handle("/debug/pprof/profile", http.DefaultServeMux)
	mux.Handle("/debug/pprof/symbol", http.DefaultServeMux)
	mux.Handle("/debug/pprof/trace", http.DefaultServeMux)

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

func (s *Server) Shutdown(ctx context.Context) error {
	var err error
	if s.httpServer != nil {
		err = s.httpServer.Shutdown(ctx)
	}

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

	maxStaleInputStreak int32 = 120
)

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

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {

	s.connectionsMu.RLock()
	connCount := len(s.connections)
	s.connectionsMu.RUnlock()
	if connCount >= s.cfg.Net.MaxConnections {
		http.Error(w, "Server full", http.StatusServiceUnavailable)
		return
	}

	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		clientIP = r.RemoteAddr
	}
	limiter := s.getOrCreateRateLimiter(clientIP)

	if !limiter.Allow() {
		metrics.IPRateLimited.Inc()
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	rawConn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err, "remote_addr", r.RemoteAddr)
		metrics.WSUpgradeErrors.Inc()
		return
	}

	requestedUnitType := r.URL.Query().Get("unit")
	player := s.gameWorld.AddPlayer(requestedUnitType)
	connection := s.createConnection(player, rawConn)

	s.sendWelcome(connection)

	s.sendInitialState(connection)
	s.sendUnitRoster(connection)

	s.connectionsMu.Lock()
	s.connections[player.ID] = connection
	s.connectionsMu.Unlock()

	metrics.ConnectionsTotal.Inc()
	metrics.PlayersConnected.Inc()

	s.rh.register(s, connection)
}

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
			clientMsg.Sprint,
		)
		switch result {
		case types.InputAccepted:
			atomic.StoreInt32(&connection.staleInputStreak, 0)

		case types.InputStale:

			metrics.MovementInputsRejected.Inc()
			slog.Warn("movement input rejected", "player_id", connection.player.ID, "reason", result.String(), "sequence", clientMsg.InputSequence)
			if atomic.AddInt32(&connection.staleInputStreak, 1) >= maxStaleInputStreak {
				go s.cleanupConnection(connection)
			}
			return

		default:

			metrics.MovementInputsRejected.Inc()
			slog.Warn("movement input rejected", "player_id", connection.player.ID, "reason", result.String(), "sequence", clientMsg.InputSequence)
			go s.cleanupConnection(connection)
			return
		}

	case protocol.MessageDirection:
		metrics.MessagesReceived.WithLabelValues("direction").Inc()
		s.markConnectionCritical(connection)
		s.gameWorld.ProcessEvent(types.GameEvent{
			PlayerID:  connection.player.ID,
			Type:      types.EventFace,
			Direction: clientMsg.Direction,
		})

	case protocol.MessageAttack:
		metrics.MessagesReceived.WithLabelValues("attack").Inc()
		s.markConnectionCritical(connection)
		s.gameWorld.TryAttack(connection.player.ID)

	case protocol.MessageAttackEnd:

	case protocol.MessageBlockStart:
		metrics.MessagesReceived.WithLabelValues("block_start").Inc()
		s.markConnectionCritical(connection)
		s.gameWorld.TryBlockStart(connection.player.ID)

	case protocol.MessageBlockEnd:
		metrics.MessagesReceived.WithLabelValues("block_end").Inc()
		s.markConnectionCritical(connection)
		s.gameWorld.EndBlock(connection.player.ID)

	case protocol.MessageViewportUpdate:

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

func (s *Server) cleanupConnection(c *Connection) {
	c.closeOnce.Do(func() {
		playerID := c.player.ID

		metrics.DisconnectionsTotal.Inc()
		metrics.PlayersConnected.Dec()
		metrics.SessionDuration.Observe(time.Since(c.player.JoinTime).Seconds())

		s.rh.remove(c)

		s.connectionsMu.Lock()
		delete(s.connections, playerID)
		s.connectionsMu.Unlock()

		s.notifyPlayerLeft(playerID)

		c.cancel()
		drainWriteCh(c.writeCh)

		c.rawConn.Close()

		s.gameWorld.RemovePlayer(playerID)
	})
}

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

func (s *Server) logPerformanceStats() {

}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"healthy","uptime_seconds":%d,"players":%d}`,
		int(time.Since(s.startTime).Seconds()),
		s.gameWorld.GetPlayerCount())
}

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
