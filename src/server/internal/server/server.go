package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"strings"
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
	"pixi_game_server/internal/units"
)

type Server struct {
	cfg       *config.Config
	live      *config.LiveNet
	gameWorld *game.GameWorld
	protocol  *protocol.BinaryProtocol

	gameConfigJSON []byte
	unitsJSON      atomic.Pointer[[]byte]
	adminStore     *liveconfig.Store

	connectionsMu    sync.RWMutex
	connections      map[uint32]*Connection
	lifecycleMu      sync.Mutex
	stopping         bool
	admissions       int
	handlers         sync.WaitGroup
	workers          sync.WaitGroup
	shutdownOnce     sync.Once
	shutdownDone     chan struct{}
	managementServer *http.Server

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
	enqueueMu            sync.Mutex
	closed               bool
	needsFullState       atomic.Bool
	lastQueuedStateSeq   uint32
	hasQueuedState       bool
	resyncLimiter        *rate.Limiter
	controlLimiter       *rate.Limiter
	rateLimiter          *rate.Limiter
	writeCh              chan writeJob
	closeOnce            sync.Once
	lastActivity         int64
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

func New(cfg *config.Config) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())

	live := config.NewLiveNet(config.BuildLiveNetConfig(cfg))

	server := &Server{
		cfg:          cfg,
		live:         live,
		gameWorld:    game.NewGameWorld(cfg, live),
		protocol:     &protocol.BinaryProtocol{},
		connections:  make(map[uint32]*Connection, 4096),
		ctx:          ctx,
		cancel:       cancel,
		startTime:    time.Now(),
		shutdownDone: make(chan struct{}),
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

	server.workers.Add(1)
	go func() { defer server.workers.Done(); server.runPingLoop() }()

	server.gameWorld.SetTickBroadcaster(server.broadcastTick)
	server.workers.Add(1)
	go func() {
		defer server.workers.Done()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-server.ctx.Done():
				return
			case <-ticker.C:
				server.rateLimiters.Range(func(key, _ any) bool { server.rateLimiters.Delete(key); return true })
			}
		}
	}()

	return server, nil
}

func (s *Server) Live() *config.LiveNet {
	return s.live
}

func (s *Server) SetStaticBlobs(gameConfigJSON, unitsJSON []byte) {
	s.gameConfigJSON = gameConfigJSON
	s.unitsJSON.Store(&unitsJSON)
}

func (s *Server) UpdateUnitsJSON(unitsJSON []byte) {
	s.unitsJSON.Store(&unitsJSON)
}

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

func (s *Server) publicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.Handle("/", http.FileServer(http.Dir(s.cfg.Server.StaticDir)))
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/config", s.handleStaticConfig)
	mux.HandleFunc("/api/units", s.handleStaticUnits)
	for _, path := range []string{"/metrics", "/metrics/", "/debug/", "/api/admin/"} {
		mux.HandleFunc(path, http.NotFound)
	}
	return mux
}

func (s *Server) managementHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/metrics/json", s.handleMetricsJSON)
	if s.cfg.Server.EnablePprof {
		mux.Handle("/debug/pprof/", http.DefaultServeMux)
	}
	if s.adminStore != nil {
		mux.HandleFunc("PATCH /api/admin/units/{typeId}", func(w http.ResponseWriter, r *http.Request) {
			authorization := r.Header.Get("Authorization")
			token := strings.TrimPrefix(authorization, "Bearer ")
			expected := s.cfg.Server.AdminToken
			if !strings.HasPrefix(authorization, "Bearer ") || expected == "" || subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			s.handleAdminUpdateUnit(w, r)
		})
	}
	return mux
}

func (s *Server) Start() error {
	s.lifecycleMu.Lock()
	if s.stopping || s.httpServer != nil {
		s.lifecycleMu.Unlock()
		return errors.New("server already started or stopping")
	}
	if s.adminStore != nil && len(s.cfg.Server.AdminToken) < 32 {
		s.lifecycleMu.Unlock()
		return errors.New("unit admin API requires ADMIN_API_TOKEN of at least 32 bytes")
	}
	makeServer := func(addr string, handler http.Handler) *http.Server {
		return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	}
	s.httpServer = makeServer(net.JoinHostPort(s.cfg.Server.Host, fmt.Sprint(s.cfg.Server.Port)), s.publicHandler())
	managementAddr := s.cfg.Server.ManagementAddr
	if managementAddr == "" {
		managementAddr = "127.0.0.1:8110"
	}
	s.managementServer = makeServer(managementAddr, s.managementHandler())
	publicListener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		s.lifecycleMu.Unlock()
		return err
	}
	managementListener, err := net.Listen("tcp", managementAddr)
	if err != nil {
		publicListener.Close()
		s.lifecycleMu.Unlock()
		return err
	}
	s.workers.Add(2)
	errs := make(chan error, 2)
	for _, pair := range []struct {
		server   *http.Server
		listener net.Listener
	}{
		{s.httpServer, publicListener}, {s.managementServer, managementListener},
	} {
		go func() { defer s.workers.Done(); errs <- pair.server.Serve(pair.listener) }()
	}
	s.lifecycleMu.Unlock()
	slog.Info("server listening", "addr", publicListener.Addr(), "management_addr", managementListener.Addr())
	if os.Getenv("PPROF_BLOCK_RATE") == "1" && s.cfg.Server.EnablePprof {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
	}
	err = <-errs
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdownOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.stopping = true
		s.lifecycleMu.Unlock()
		go func() {
			defer close(s.shutdownDone)
			for _, srv := range []*http.Server{s.httpServer, s.managementServer} {
				if srv != nil {
					srv.Close()
				}
			}
			s.handlers.Wait()
			s.gameWorld.Stop()
			s.cancel()
			s.connectionsMu.RLock()
			conns := make([]*Connection, 0, len(s.connections))
			for _, c := range s.connections {
				conns = append(conns, c)
			}
			s.connectionsMu.RUnlock()
			for _, c := range conns {
				s.cleanupConnection(c)
			}
			s.workers.Wait()
		}()
	})
	select {
	case <-s.shutdownDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	s.lifecycleMu.Lock()
	if s.stopping || s.admissions >= s.live.Load().MaxConnections {
		s.lifecycleMu.Unlock()
		http.Error(w, "Server unavailable", http.StatusServiceUnavailable)
		return
	}
	s.admissions++
	s.handlers.Add(1)
	s.lifecycleMu.Unlock()
	defer s.handlers.Done()
	admitted := false
	defer func() {
		if !admitted {
			s.releaseAdmission()
		}
	}()
	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		clientIP = r.RemoteAddr
	}
	if !s.getOrCreateRateLimiter(clientIP).Allow() {
		metrics.IPRateLimited.Inc()
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	requestedUnitType := r.URL.Query().Get("unit")
	if requestedUnitType != "" && !units.IsValid(requestedUnitType) {
		http.Error(w, "Unknown unit", http.StatusBadRequest)
		return
	}
	rawConn, buffer, _, err := (ws.HTTPUpgrader{Timeout: 5 * time.Second}).Upgrade(r, w)
	if err != nil {
		metrics.WSUpgradeErrors.Inc()
		return
	}
	player := s.gameWorld.AddPlayer(requestedUnitType)
	connection := s.createConnection(player, rawConn)
	s.sendWelcome(connection)
	connection.needsFullState.Store(true)
	s.connectionsMu.Lock()
	s.connections[player.ID] = connection
	s.connectionsMu.Unlock()
	metrics.ConnectionsTotal.Inc()
	metrics.PlayersConnected.Inc()
	admitted = true
	s.startWriteLoop(connection)
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer s.cleanupConnection(connection)
		if buffer != nil {
			s.readLoop(connection, buffer.Reader)
		} else {
			s.readLoop(connection, rawConn)
		}
	}()
}

func (s *Server) releaseAdmission() {
	s.lifecycleMu.Lock()
	s.admissions--
	s.lifecycleMu.Unlock()
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
		resyncLimiter:        rate.NewLimiter(1, 1),
		controlLimiter:       rate.NewLimiter(10, 20),
		lastActivity:         clock.Now(),
		lastWorldStateSentNs: clock.Now(),
		ctx:                  ctx,
		cancel:               cancel,
	}
	return conn
}

func (s *Server) processMessage(connection *Connection, message []byte) {
	clientMsg, err := s.protocol.DecodeClientMessage(message)
	if err != nil {
		s.cleanupConnection(connection)
		return
	}

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
		s.queueAction(connection, types.PlayerAction{Type: types.ActionFace, Direction: clientMsg.Direction})

	case protocol.MessageAttack:
		metrics.MessagesReceived.WithLabelValues("attack").Inc()
		s.markConnectionCritical(connection)
		s.queueAction(connection, types.PlayerAction{Type: types.ActionAttack})

	case protocol.MessageBlockStart:
		metrics.MessagesReceived.WithLabelValues("block_start").Inc()
		s.markConnectionCritical(connection)
		s.queueAction(connection, types.PlayerAction{Type: types.ActionBlockStart})

	case protocol.MessageBlockEnd:
		metrics.MessagesReceived.WithLabelValues("block_end").Inc()
		s.markConnectionCritical(connection)
		s.queueAction(connection, types.PlayerAction{Type: types.ActionBlockEnd})

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

func (s *Server) queueAction(c *Connection, action types.PlayerAction) {
	if !s.gameWorld.QueueAction(c.player.ID, action) {
		s.cleanupConnection(c)
	}
}

func (c *Connection) enqueue(job writeJob) bool {
	c.enqueueMu.Lock()
	defer c.enqueueMu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.writeCh <- job:
		return true
	default:
		return false
	}
}

func (s *Server) cleanupConnection(c *Connection) {
	c.closeOnce.Do(func() {
		c.enqueueMu.Lock()
		c.closed = true
		c.cancel()
		c.rawConn.Close()
		c.enqueueMu.Unlock()
		s.connectionsMu.Lock()
		_, registered := s.connections[c.player.ID]
		delete(s.connections, c.player.ID)
		s.connectionsMu.Unlock()
		if registered {
			s.releaseAdmission()
			metrics.DisconnectionsTotal.Inc()
			metrics.PlayersConnected.Dec()
			metrics.SessionDuration.Observe(time.Since(c.player.JoinTime).Seconds())
			s.gameWorld.RemovePlayer(c.player.ID)
		}
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

var minTickWatchdogThreshold = 5 * time.Second

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.lifecycleMu.Lock()
	stopping := s.stopping
	s.lifecycleMu.Unlock()
	if stopping {
		http.Error(w, "Server draining", http.StatusServiceUnavailable)
		return
	}

	threshold := 20 * s.gameWorld.GetNominalTickInterval()
	if threshold < minTickWatchdogThreshold {
		threshold = minTickWatchdogThreshold
	}
	if age := s.gameWorld.TimeSinceLastTick(); age > threshold {
		http.Error(w, "Game tick loop stalled", http.StatusServiceUnavailable)
		return
	}

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
