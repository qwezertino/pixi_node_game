package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* handlers on DefaultServeMux
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
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
	upgrader  websocket.Upgrader
	protocol  *protocol.BinaryProtocol

	// Connection management
	connections sync.Map // map[uint32]*Connection
	shards      []*shard // write shards: N workers instead of 2400 goroutines

	// Rate limiting
	rateLimiters sync.Map // map[string]*rate.Limiter

	// Server state
	ctx    context.Context
	cancel context.CancelFunc

	// Performance monitoring
	startTime time.Time
}

// Connection представляет WebSocket соединение
type Connection struct {
	player      *types.Player
	conn        *websocket.Conn
	rateLimiter *rate.Limiter
	ctx         context.Context
	cancel      context.CancelFunc
}

// New создает новый сервер
func New(cfg *config.Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	// Auto-detect worker count
	if cfg.Server.Workers == 0 {
		cfg.Server.Workers = runtime.NumCPU()
	}

	server := &Server{
		cfg:       cfg,
		gameWorld: game.NewGameWorld(cfg),
		protocol:  &protocol.BinaryProtocol{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  cfg.Server.ReadBufferSize,
			WriteBufferSize: cfg.Server.WriteBufferSize,
			WriteBufferPool: &sync.Pool{},
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
		},
		ctx:       ctx,
		cancel:    cancel,
		startTime: time.Now(),
	}

	// Инициализируем write-шарды (N = GOMAXPROCS воркеров вместо 2400 горутин).
	server.initShards(ctx)

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
	// Включаем block и mutex profiler: показывают где горутины блокируются (source of p99).
	runtime.SetBlockProfileRate(1)     // record every blocking event
	runtime.SetMutexProfileFraction(1) // record every mutex contention event
	mux.Handle("/debug/pprof/", http.DefaultServeMux)
	mux.Handle("/debug/pprof/cmdline", http.DefaultServeMux)
	mux.Handle("/debug/pprof/profile", http.DefaultServeMux)
	mux.Handle("/debug/pprof/symbol", http.DefaultServeMux)
	mux.Handle("/debug/pprof/trace", http.DefaultServeMux)

	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)

	slog.Info("server listening", "addr", addr)
	slog.Info("serving static files", "dir", s.cfg.Server.StaticDir)

	return http.ListenAndServe(addr, mux)
}

// handleWebSocket обрабатывает WebSocket соединения
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Rate limiting by IP
	clientIP := r.RemoteAddr
	limiter := s.getOrCreateRateLimiter(clientIP)

	if !limiter.Allow() {
		metrics.IPRateLimited.Inc()
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	// Upgrade connection
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err, "remote_addr", r.RemoteAddr)
		metrics.WSUpgradeErrors.Inc()
		return
	}

	// Create player and connection
	player := s.gameWorld.AddPlayer()
	connection := s.createConnection(player, conn)

	s.connections.Store(player.ID, connection)

	// Send initial state
	s.sendInitialState(connection)

	// Notify all existing players about the new player
	s.notifyPlayerJoined(player)

	// Update metrics
	metrics.ConnectionsTotal.Inc()
	metrics.PlayersConnected.Inc()

	// Start connection handlers
	go s.handleConnection(connection)
}

// createConnection создает новое соединение и регистрирует его в шарде.
func (s *Server) createConnection(player *types.Player, conn *websocket.Conn) *Connection {
	ctx, cancel := context.WithCancel(s.ctx)

	c := &Connection{
		player: player,
		conn:   conn,
		rateLimiter: rate.NewLimiter(
			rate.Limit(s.cfg.Net.MessageRateLimit),
			s.cfg.Net.BurstLimit,
		),
		ctx:    ctx,
		cancel: cancel,
	}
	s.shardFor(player.ID).add(c)
	return c
}

// handleConnection обрабатывает соединение
func (s *Server) handleConnection(connection *Connection) {
	defer s.cleanupConnection(connection)

	// Set timeouts
	connection.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	connection.conn.SetPongHandler(func(string) error {
		connection.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Read messages
	for {
		select {
		case <-connection.ctx.Done():
			return

		default:
			_, message, err := connection.conn.ReadMessage()
			if err != nil {
				metrics.WSReadErrors.Inc()
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNoStatusReceived) {
					slog.Warn("websocket unexpected close", "player_id", connection.player.ID, "error", err)
				} else {
					slog.Debug("websocket read closed", "player_id", connection.player.ID, "error", err)
				}
				return
			}

			metrics.BytesReceived.Add(float64(len(message)))

			// Rate limiting
			if !connection.rateLimiter.Allow() {
				slog.Warn("rate limit exceeded", "player_id", connection.player.ID)
				metrics.MessagesRateLimited.Inc()
				continue
			}

			s.processMessage(connection, message)
		}
	}
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
		metrics.MessagesReceived.WithLabelValues("viewport").Inc()
		connection.player.ViewportWidth = clientMsg.ViewportWidth
		connection.player.ViewportHeight = clientMsg.ViewportHeight
	}
}

// cleanupConnection очищает соединение
func (s *Server) cleanupConnection(connection *Connection) {
	playerID := connection.player.ID

	metrics.DisconnectionsTotal.Inc()
	metrics.PlayersConnected.Dec()
	metrics.SessionDuration.Observe(time.Since(connection.player.JoinTime).Seconds())

	// Notify other players that this player left
	s.notifyPlayerLeft(playerID)

	// Дерегистрируем из шарда до закрытия соединения.
	s.shardFor(playerID).remove(playerID)

	connection.cancel()
	connection.conn.Close()
	s.connections.Delete(playerID)
	s.gameWorld.RemovePlayer(playerID)
}

// getOrCreateRateLimiter получает или создает rate limiter для IP
func (s *Server) getOrCreateRateLimiter(ip string) *rate.Limiter {
	if limiter, exists := s.rateLimiters.Load(ip); exists {
		return limiter.(*rate.Limiter)
	}

	limiter := rate.NewLimiter(rate.Limit(10), 20) // 10 req/sec, burst 20
	s.rateLimiters.Store(ip, limiter)
	return limiter
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
		"events_per_second": %d,
		"uptime_seconds": %d,
		"goroutines": %d,
		"heap_alloc_mb": %d
	}`,
		m.ConnectedPlayers,
		m.TickDuration.Nanoseconds(),
		m.EventsPerSecond,
		int(time.Since(s.startTime).Seconds()),
		runtime.NumGoroutine(),
		mem.HeapAlloc/1024/1024)
}

// Helper function
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
