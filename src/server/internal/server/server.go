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
	"time"

	"github.com/gobwas/ws"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"

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
	ctx    context.Context
	cancel context.CancelFunc

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
	player        *types.Player
	rawConn       net.Conn
	fd            int // OS file descriptor (used by epoll remove)
	rateLimiter   *rate.Limiter
	writeCh       chan writeJob // buffered channel drained by startWriteLoop goroutine
	closeOnce     sync.Once     // ensures cleanupConnection body runs once
	lastActivity  int64         // UnixNano, updated on each received frame (atomic)
	writeFailures int32         // consecutive write timeouts/errors (atomic); reset on success
	ctx           context.Context
	cancel        context.CancelFunc
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

	return http.ListenAndServe(addr, mux)
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

	// Send initial state BEFORE adding to s.connections so that the write loop
	// delivers the full world snapshot as the very first message the client
	// receives. If we add to the map first, a 30 Hz tick can race here and
	// enqueue a delta/gamestate frame ahead of the initial state.
	s.sendInitialState(connection)

	s.connectionsMu.Lock()
	s.connections[player.ID] = connection
	s.connectionsMu.Unlock()

	// Notify all existing players about the new player
	s.notifyPlayerJoined(player)

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
		lastActivity: time.Now().UnixNano(),
		ctx:          ctx,
		cancel:       cancel,
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

		// Server-authoritative: process movement vector, server computes position
		event := types.GameEvent{
			PlayerID:   connection.player.ID,
			Type:       types.EventMove,
			VectorX:    clientMsg.MovementVector.DX,
			VectorY:    clientMsg.MovementVector.DY,
			ClientTick: clientMsg.InputSequence,
		}
		s.gameWorld.ProcessEvent(event)

		// ACK with the position the client predicted (current + this move vector).
		// The server will apply the same formula in its next tick.
		// Sending this avoids false reconciliation: client delta = 0.
		speed := int32(s.cfg.Game.PlayerSpeedPerTick)
		dx := int32(clientMsg.MovementVector.DX)
		dy := int32(clientMsg.MovementVector.DY)
		ackX32 := int32(connection.player.GetX()) + dx*speed
		ackY32 := int32(connection.player.GetY()) + dy*speed

		// Clamp to world bounds (same as updatePlayerPosition)
		if ackX32 > int32(s.cfg.World.MaxX) {
			ackX32 = int32(s.cfg.World.MaxX)
		} else if ackX32 < int32(s.cfg.World.MinX) {
			ackX32 = int32(s.cfg.World.MinX)
		}
		if ackY32 > int32(s.cfg.World.MaxY) {
			ackY32 = int32(s.cfg.World.MaxY)
		} else if ackY32 < int32(s.cfg.World.MinY) {
			ackY32 = int32(s.cfg.World.MinY)
		}

		// Send movement acknowledgment via shard directChan (priority over broadcast).
		ackData := s.protocol.EncodeMovementAck(
			connection.player.ID,
			uint16(ackX32),
			uint16(ackY32),
			clientMsg.InputSequence,
		)
		s.sendDirect(connection, ackData)

		// Обновление позиции разошлётся через tick broadcast, не здесь.

	case protocol.MessageDirection:
		metrics.MessagesReceived.WithLabelValues("direction").Inc()
		s.gameWorld.ProcessEvent(types.GameEvent{
			PlayerID:    connection.player.ID,
			Type:        types.EventFace,
			FacingRight: clientMsg.Direction,
		})
		// Обновление направления разошлётся через tick broadcast.

	case protocol.MessageAttack:
		metrics.MessagesReceived.WithLabelValues("attack").Inc()
		s.gameWorld.TryAttack(connection.player.ID)
		// State=1 будет разослан всем через tick broadcast.

	case protocol.MessageAttackEnd:
		// Ignored: server is authoritative on attack duration.

	case protocol.MessageViewportUpdate:
		// Silently accepted — viewport-based culling not yet implemented.
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
