package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
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

	// Broadcasting
	broadcastChan chan BroadcastMessage
	broadcastWG   sync.WaitGroup

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
	sendChan    chan []byte
	ctx         context.Context
	cancel      context.CancelFunc
}

// BroadcastMessage сообщение для рассылки
type BroadcastMessage struct {
	PlayerID uint32
	Data     []byte
	Viewport types.ViewportBounds
}

// New создает новый сервер
func New(cfg *config.Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	// Auto-detect worker count
	if cfg.Server.Workers == 0 {
		cfg.Server.Workers = runtime.NumCPU()
	}
	if cfg.Net.BroadcastWorkers == 0 {
		cfg.Net.BroadcastWorkers = runtime.NumCPU() * 2
	}

	server := &Server{
		cfg:       cfg,
		gameWorld: game.NewGameWorld(cfg),
		protocol:  &protocol.BinaryProtocol{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  cfg.Server.ReadBufferSize,
			WriteBufferSize: cfg.Server.WriteBufferSize,
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
		},
		broadcastChan: make(chan BroadcastMessage, 10000), // Large buffer for broadcasts
		ctx:           ctx,
		cancel:        cancel,
		startTime:     time.Now(),
	}

	// Start broadcast workers
	for i := 0; i < cfg.Net.BroadcastWorkers; i++ {
		server.broadcastWG.Add(1)
		go server.broadcastWorker(i)
	}

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

// createConnection создает новое соединение
func (s *Server) createConnection(player *types.Player, conn *websocket.Conn) *Connection {
	ctx, cancel := context.WithCancel(s.ctx)

	return &Connection{
		player: player,
		conn:   conn,
		rateLimiter: rate.NewLimiter(
			rate.Limit(s.cfg.Net.MessageRateLimit),
			s.cfg.Net.BurstLimit,
		),
		sendChan: make(chan []byte, s.cfg.Net.SendChannelSize), // Buffer for outgoing messages
		ctx:      ctx,
		cancel:   cancel,
	}
}

// handleConnection обрабатывает соединение
func (s *Server) handleConnection(connection *Connection) {
	defer s.cleanupConnection(connection)

	// Start message sender
	go s.connectionSender(connection)

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
		// Get client's reported position from the message
		clientX := uint16(clientMsg.Position.X)
		clientY := uint16(clientMsg.Position.Y)

		// Get current server position for validation
		serverX := connection.player.GetX()
		serverY := connection.player.GetY()

		// Calculate position difference for validation
		deltaX := int32(clientX) - int32(serverX)
		deltaY := int32(clientY) - int32(serverY)
		distanceSquared := deltaX*deltaX + deltaY*deltaY

		// Threshold for acceptable position difference (in pixels)
		const maxAllowedDistanceSquared = 50 * 50 // 50 pixels tolerance

		var ackX, ackY uint16

		// If client position is too far from server, reject it and send server position
		if distanceSquared > maxAllowedDistanceSquared {
			// Process movement from current server position
			event := types.GameEvent{
				PlayerID:   connection.player.ID,
				Type:       types.EventMove,
				VectorX:    clientMsg.MovementVector.DX,
				VectorY:    clientMsg.MovementVector.DY,
				ClientTick: clientMsg.InputSequence,
			}
			s.gameWorld.ProcessEvent(event)

			// Send server's authoritative position
			ackX = connection.player.GetX()
			ackY = connection.player.GetY()
		} else {
			// Client position is acceptable, use it as starting point for movement
			// Set player to client's reported position first
			connection.player.SetX(clientX)
			connection.player.SetY(clientY)

			// Then process the movement from that position
			event := types.GameEvent{
				PlayerID:   connection.player.ID,
				Type:       types.EventMove,
				VectorX:    clientMsg.MovementVector.DX,
				VectorY:    clientMsg.MovementVector.DY,
				ClientTick: clientMsg.InputSequence,
			}
			s.gameWorld.ProcessEvent(event)

			// Acknowledge the client's original position (lag compensation)
			ackX = clientX
			ackY = clientY
		}

		// Send movement acknowledgment
		ackData := s.protocol.EncodeMovementAck(
			connection.player.ID,
			ackX,
			ackY,
			clientMsg.InputSequence,
		)

		select {
		case connection.sendChan <- ackData:
		default:
			slog.Warn("movement ack channel full", "player_id", connection.player.ID)
		}

		// Broadcast player movement to other clients
		stateData := s.protocol.EncodePlayerMovement(
			connection.player.ID,
			clientMsg.MovementVector.DX,
			clientMsg.MovementVector.DY,
		)

		// Define broadcast viewport (for now, broadcast to all - can optimize later)
		viewport := types.ViewportBounds{
			MinX: 0,
			MinY: 0,
			MaxX: 65535,
			MaxY: 65535,
		}

		broadcast := BroadcastMessage{
			PlayerID: connection.player.ID,
			Data:     stateData,
			Viewport: viewport,
		}

		select {
		case s.broadcastChan <- broadcast:
		default:
			slog.Warn("movement broadcast channel full, skipped")
			// Broadcast channel full, skip
		}

	case protocol.MessageDirection:
		metrics.MessagesReceived.WithLabelValues("direction").Inc()
		s.gameWorld.ProcessEvent(types.GameEvent{
			PlayerID:    connection.player.ID,
			Type:        types.EventFace,
			FacingRight: clientMsg.Direction,
		})

		// Broadcast direction change
		stateData := s.protocol.EncodePlayerDirection(
			connection.player.ID,
			clientMsg.Direction,
		)

		viewport := types.ViewportBounds{MinX: 0, MinY: 0, MaxX: 65535, MaxY: 65535}
		broadcast := BroadcastMessage{
			PlayerID: connection.player.ID,
			Data:     stateData,
			Viewport: viewport,
		}

		select {
		case s.broadcastChan <- broadcast:
		default:
			// Broadcast channel full, skip
		}

	case protocol.MessageAttack:
		metrics.MessagesReceived.WithLabelValues("attack").Inc()
		s.gameWorld.ProcessEvent(types.GameEvent{
			PlayerID: connection.player.ID,
			Type:     types.EventAttack,
		})

		// Broadcast attack state
		playerX := connection.player.GetX()
		playerY := connection.player.GetY()
		stateData := s.protocol.EncodePlayerAttack(
			connection.player.ID,
			playerX,
			playerY,
		)

		viewport := types.ViewportBounds{MinX: 0, MinY: 0, MaxX: 65535, MaxY: 65535}
		broadcast := BroadcastMessage{
			PlayerID: connection.player.ID,
			Data:     stateData,
			Viewport: viewport,
		}

		select {
		case s.broadcastChan <- broadcast:
		default:
			// Broadcast channel full, skip
		}

	case protocol.MessageViewportUpdate:
		metrics.MessagesReceived.WithLabelValues("viewport").Inc()
		connection.player.ViewportWidth = clientMsg.ViewportWidth
		connection.player.ViewportHeight = clientMsg.ViewportHeight
	}
}

// sendInitialState отправляет начальное состояние клиенту
func (s *Server) sendInitialState(connection *Connection) {
	allPlayers := s.gameWorld.GetAllPlayers()
	data := s.protocol.EncodeGameState(allPlayers)

	select {
	case connection.sendChan <- data:
	default:
		slog.Warn("initial state send failed", "player_id", connection.player.ID)
	}
}

// notifyPlayerJoined уведомляет всех игроков о присоединении нового игрока
func (s *Server) notifyPlayerJoined(newPlayer *types.Player) {

	playerState := types.PlayerState{
		ID:          newPlayer.ID,
		X:           uint16(newPlayer.GetX()),
		Y:           uint16(newPlayer.GetY()),
		VX:          0,
		VY:          0,
		FacingRight: true,
		State:       0, // IDLE
		ClientTick:  0,
	}

	data := s.protocol.EncodePlayerJoined(playerState)

	sentCount := 0
	totalConnections := 0

	s.connections.Range(func(key, value interface{}) bool {
		connection := value.(*Connection)
		totalConnections++

		// Skip the new player - they already got the full state
		if connection.player.ID == newPlayer.ID {
			return true
		}

		select {
		case connection.sendChan <- data:
			sentCount++
		default:
		}

		return true
	})

}

// notifyPlayerLeft уведомляет всех игроков об отключении игрока
func (s *Server) notifyPlayerLeft(leftPlayerID uint32) {

	data := s.protocol.EncodePlayerLeft(leftPlayerID)

	sentCount := 0
	totalConnections := 0

	s.connections.Range(func(key, value interface{}) bool {
		connection := value.(*Connection)
		totalConnections++

		// Skip the leaving player - they're already disconnected
		if connection.player.ID == leftPlayerID {
			return true
		}

		select {
		case connection.sendChan <- data:
			sentCount++
		default:
		}

		return true
	})

}

// connectionSender отправляет сообщения клиенту
func (s *Server) connectionSender(connection *Connection) {
	ticker := time.NewTicker(time.Second * 30) // Ping interval
	defer ticker.Stop()

	for {
		select {
		case <-connection.ctx.Done():
		case data := <-connection.sendChan:
			connection.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := connection.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				metrics.WSWriteErrors.Inc()
				if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					slog.Warn("websocket write error", "player_id", connection.player.ID, "error", err)
				}
				return
			}
			metrics.BytesSent.Add(float64(len(data)))

		case <-ticker.C:
			connection.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := connection.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
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

	connection.cancel()
	connection.conn.Close()
	s.connections.Delete(playerID)
	s.gameWorld.RemovePlayer(playerID)
}

// broadcastWorker обрабатывает рассылку сообщений
func (s *Server) broadcastWorker(workerID int) {
	defer s.broadcastWG.Done()

	for {
		select {
		case <-s.ctx.Done():
			return

		case broadcast := <-s.broadcastChan:
			s.processBroadcast(broadcast)
		}
	}
}

// processBroadcast обрабатывает рассылку
func (s *Server) processBroadcast(broadcast BroadcastMessage) {

	sentCount := 0
	totalConnections := 0

	s.connections.Range(func(key, value interface{}) bool {
		connection := value.(*Connection)
		totalConnections++

		// Skip sender
		if connection.player.ID == broadcast.PlayerID {
			return true
		}

		// Check viewport visibility
		playerX := connection.player.GetX()
		playerY := connection.player.GetY()

		if playerX >= broadcast.Viewport.MinX && playerX <= broadcast.Viewport.MaxX &&
			playerY >= broadcast.Viewport.MinY && playerY <= broadcast.Viewport.MaxY {

			select {
			case connection.sendChan <- broadcast.Data:
				sentCount++
				metrics.BroadcastsSent.Inc()
			default:
				slog.Warn("send channel full", "player_id", connection.player.ID)
				metrics.BroadcastsDropped.Inc()
			}
		} // viewport check removed from log

		return true
	})
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
