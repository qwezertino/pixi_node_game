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
	"pixi_game_server/internal/liveconfig"
	"pixi_game_server/internal/metrics"
	"pixi_game_server/internal/protocol"
	"pixi_game_server/internal/types"
)

type Server struct {
	cfg       *config.Config
	live      *config.LiveNet
	gameWorld *game.GameWorld
	protocol  *protocol.BinaryProtocol

	gameConfigJSON []byte
	unitsJSON      atomic.Pointer[[]byte]
	adminStore     *liveconfig.Store

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

	fanoutRoundRobinEpoch int64
	fanoutDebtEpoch       uint32

	fanoutRecipientLimit int64
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

	live := config.NewLiveNet(config.BuildLiveNetConfig(cfg))

	server := &Server{
		cfg:         cfg,
		live:        live,
		gameWorld:   game.NewGameWorld(cfg, live),
		protocol:    &protocol.BinaryProtocol{},
		connections: make(map[uint32]*Connection, 4096),
		ctx:         ctx,
		cancel:      cancel,
		startTime:   time.Now(),
	}

	server.dilationBps = dilationBpsFull
	metrics.TimeDilationPercent.Set(100)
	metrics.TickIntervalMs.Set(float64(server.gameWorld.GetNominalTickInterval().Milliseconds()))

	if snap := live.Load(); snap.FanoutMaxRecipientsPerTick > 0 {
		atomic.StoreInt64(&server.fanoutRecipientLimit, int64(snap.FanoutMaxRecipientsPerTick))
		metrics.FanoutRecipientLimit.Set(float64(snap.FanoutMaxRecipientsPerTick))
	} else {
		metrics.FanoutRecipientLimit.Set(0)
	}

	go server.runPingLoop()

	server.rh = newReadHandler(server)

	server.gameWorld.SetTickBroadcaster(server.broadcastTick)

	go server.performanceMonitor()

	return server
}

// Live exposes the hot-swappable network config so callers (main.go's DB
// watcher) can push live updates into it.
func (s *Server) Live() *config.LiveNet {
	return s.live
}

// SetStaticBlobs records the exact gameConfig.json/units.json bytes the
// server booted with (from Postgres), so the TS client can fetch the same
// values it would otherwise have bundled at build time — see /api/config
// and /api/units. gameConfigJSON never changes after this (game rules need a
// restart); unitsJSON can — see UpdateUnitsJSON.
func (s *Server) SetStaticBlobs(gameConfigJSON, unitsJSON []byte) {
	s.gameConfigJSON = gameConfigJSON
	s.unitsJSON.Store(&unitsJSON)
}

// UpdateUnitsJSON swaps in freshly re-marshaled unit data after a live
// reload (see internal/liveconfig.WatchUnits), so GET /api/units serves the
// new values to any client that (re)connects from this point on.
func (s *Server) UpdateUnitsJSON(unitsJSON []byte) {
	s.unitsJSON.Store(&unitsJSON)
}

// RecomputeUnitTables re-derives the game loop's per-unit-type lookup tables
// (move speed, stamina, attack/combo timing) from the current units.All().
// Call after internal/units.LoadDefinitions picks up a change.
func (s *Server) RecomputeUnitTables() {
	s.gameWorld.RecomputeUnitTables()
}

func (s *Server) handleStaticConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(s.gameConfigJSON)
}

func (s *Server) handleStaticUnits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(*s.unitsJSON.Load())
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/ws", s.handleWebSocket)

	mux.Handle("/", http.FileServer(http.Dir(s.cfg.Server.StaticDir)))

	mux.HandleFunc("/health", s.handleHealth)

	mux.HandleFunc("/api/config", s.handleStaticConfig)
	mux.HandleFunc("/api/units", s.handleStaticUnits)

	if s.adminStore != nil {
		mux.HandleFunc("PATCH /api/admin/units/{typeId}", s.handleAdminUpdateUnit)
		slog.Warn("unit admin API enabled — anyone who can reach this server can rewrite unit balance, ENABLE_UNIT_ADMIN_API should never be set in production")
	}

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
	if connCount >= s.live.Load().MaxConnections {
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

	live := s.live.Load()
	conn := &Connection{
		player:  player,
		rawConn: rawConn,
		writeCh: make(chan writeJob, writeChanSize),
		rateLimiter: rate.NewLimiter(
			rate.Limit(live.MessageRateLimit),
			live.BurstLimit,
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
	windowNs := s.live.Load().FanoutCriticalWindowNs
	if windowNs <= 0 {
		return
	}
	nowNs := clock.Now()
	untilNs := nowNs + windowNs
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
	live := s.live.Load()
	limit := rate.Limit(live.IPConnRate)
	burst := live.IPConnBurst
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
