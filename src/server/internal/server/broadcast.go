package server

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"pixi_game_server/internal/metrics"
	"pixi_game_server/internal/types"
)

// directMsg — срочное сообщение конкретному соединению (ACK, join, left).
// Маршрутизируется через shard.directChan с приоритетом перед broadcast.
type directMsg struct {
	connID uint32
	data   []byte
}

// shard управляет фиксированным подмножеством соединений.
// Один shard-воркер пишет ~(total/N) соединениям последовательно —
// вместо 2400 горутин-connectionSender мы имеем N горутин (N = GOMAXPROCS).
type shard struct {
	mu    sync.RWMutex
	conns map[uint32]*Connection

	// broadcastChan получает PreparedMessage раз в тик.
	// Буфер 2: если воркер не успел — tick всё равно не блокируется.
	broadcastChan chan *websocket.PreparedMessage

	// eventChan получает join/left PreparedMessage — редкие события для всех.
	// Буфер 256: при рампе 20 join/sec и N=8 шардов — ~2.5 event/sec/shard.
	eventChan chan *websocket.PreparedMessage

	// directChan получает срочные per-connection сообщения (ACK).
	// Обрабатывается с приоритетом перед broadcast.
	directChan chan directMsg
}

func newShard() *shard {
	return &shard{
		conns:         make(map[uint32]*Connection),
		broadcastChan: make(chan *websocket.PreparedMessage, 2),
		eventChan:     make(chan *websocket.PreparedMessage, 256),
		directChan:    make(chan directMsg, 4096),
	}
}

// add регистрирует соединение в шарде.
func (sh *shard) add(conn *Connection) {
	sh.mu.Lock()
	sh.conns[conn.player.ID] = conn
	sh.mu.Unlock()
}

// remove удаляет соединение из шарда.
func (sh *shard) remove(playerID uint32) {
	sh.mu.Lock()
	delete(sh.conns, playerID)
	sh.mu.Unlock()
}

// sendDirect отправляет срочное сообщение конкретному соединению.
// non-blocking: если directChan забит — дропаем с метрикой.
func (sh *shard) sendDirect(connID uint32, data []byte) {
	select {
	case sh.directChan <- directMsg{connID: connID, data: data}:
	default:
		metrics.SendChannelDropped.Inc()
	}
}

// run — основной цикл воркера шарда.
// Архитектура приоритетов:
//  1. Неблокирующий drain directChan — ACK всегда идёт раньше broadcast.
//  2. Blocking select на broadcastChan | directChan | ping | ctx.
//
// Запись в websocket.Conn потокобезопасна сама по себе только для одного
// writer'а — здесь воркер единственный writer для всех своих conn.
func (sh *shard) run(ctx context.Context) {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	writeDeadline := 10 * time.Second

	for {
		// Priority pass: drain directChan and eventChan before blocking on tick.
		for {
			select {
			case dm := <-sh.directChan:
				sh.mu.RLock()
				conn, ok := sh.conns[dm.connID]
				sh.mu.RUnlock()
				if ok {
					conn.conn.SetWriteDeadline(time.Now().Add(writeDeadline))
					if err := conn.conn.WriteMessage(websocket.BinaryMessage, dm.data); err != nil {
						metrics.WSWriteErrors.Inc()
						conn.cancel()
					} else {
						metrics.BytesSent.Add(float64(len(dm.data)))
					}
				}
			case pm := <-sh.eventChan:
				sh.mu.RLock()
				for _, conn := range sh.conns {
					conn.conn.SetWriteDeadline(time.Now().Add(writeDeadline))
					if err := conn.conn.WritePreparedMessage(pm); err != nil {
						metrics.WSWriteErrors.Inc()
						conn.cancel()
					}
				}
				sh.mu.RUnlock()
			default:
				goto block
			}
		}

	block:
		select {
		case <-ctx.Done():
			return

		case pm := <-sh.broadcastChan:
			// Write PreparedMessage to all connections in this shard under RLock.
			sh.mu.RLock()
			for _, conn := range sh.conns {
				conn.conn.SetWriteDeadline(time.Now().Add(writeDeadline))
				if err := conn.conn.WritePreparedMessage(pm); err != nil {
					metrics.WSWriteErrors.Inc()
					conn.cancel()
				}
			}
			sh.mu.RUnlock()

		case pm := <-sh.eventChan:
			sh.mu.RLock()
			for _, conn := range sh.conns {
				conn.conn.SetWriteDeadline(time.Now().Add(writeDeadline))
				if err := conn.conn.WritePreparedMessage(pm); err != nil {
					metrics.WSWriteErrors.Inc()
					conn.cancel()
				}
			}
			sh.mu.RUnlock()

		case dm := <-sh.directChan:
			sh.mu.RLock()
			conn, ok := sh.conns[dm.connID]
			sh.mu.RUnlock()
			if ok {
				conn.conn.SetWriteDeadline(time.Now().Add(writeDeadline))
				if err := conn.conn.WriteMessage(websocket.BinaryMessage, dm.data); err != nil {
					metrics.WSWriteErrors.Inc()
					conn.cancel()
				} else {
					metrics.BytesSent.Add(float64(len(dm.data)))
				}
			}

		case <-ping.C:
			sh.mu.RLock()
			for _, conn := range sh.conns {
				conn.conn.SetWriteDeadline(time.Now().Add(writeDeadline))
				if err := conn.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					conn.cancel()
				}
			}
			sh.mu.RUnlock()
		}
	}
}

// initShards создаёт N шардов и запускает воркеры.
// N = GOMAXPROCS (= числу OS-потоков Go runtime).
func (s *Server) initShards(ctx context.Context) {
	n := runtime.GOMAXPROCS(0)
	s.shards = make([]*shard, n)
	for i := range s.shards {
		sh := newShard()
		s.shards[i] = sh
		go sh.run(ctx)
	}
	slog.Info("write shards initialized", "count", n)
}

// shardFor возвращает шард для данного playerID.
func (s *Server) shardFor(playerID uint32) *shard {
	return s.shards[playerID%uint32(len(s.shards))]
}

// ── Broadcast ─────────────────────────────────────────────────────────────────

// broadcastTick кодирует состояние игроков и кидает PreparedMessage в каждый шард.
// WS-фрейм строится 1 раз; N шардов (N=GOMAXPROCS) пишут своим ~300 клиентам.
// Thundering herd: 2400 горутин → N пробуждений.
func (s *Server) broadcastTick(allPlayers []types.PlayerState, changed []types.PlayerState, fullSync bool) {
	if len(allPlayers) == 0 {
		return
	}

	var data []byte
	if fullSync || len(changed) == 0 {
		data = s.protocol.EncodeGameState(allPlayers)
	} else {
		data = s.protocol.EncodeDeltaGameState(changed)
	}

	pm, err := websocket.NewPreparedMessage(websocket.BinaryMessage, data)
	if err != nil {
		slog.Error("failed to prepare broadcast message", "error", err)
		return
	}

	for _, sh := range s.shards {
		select {
		case sh.broadcastChan <- pm:
		default:
			// Shard still busy with previous tick — skip, client will catch up.
			metrics.SendChannelDropped.Inc()
		}
	}
}

// ── Per-connection sends ──────────────────────────────────────────────────────

// sendInitialState отправляет начальное состояние новому клиенту через directChan.
func (s *Server) sendInitialState(connection *Connection) {
	allPlayers := s.gameWorld.GetAllPlayers()
	data := s.protocol.EncodeGameState(allPlayers)
	s.shardFor(connection.player.ID).sendDirect(connection.player.ID, data)
}

// sendDirect отправляет raw-сообщение конкретному соединению через его шард.
func (s *Server) sendDirect(conn *Connection, data []byte) {
	s.shardFor(conn.player.ID).sendDirect(conn.player.ID, data)
}

// broadcastEvent отправляет PreparedMessage во все шарды через eventChan.
// O(N_shards) вместо O(N_connections) — ключевая оптимизация для join/left.
func (s *Server) broadcastEvent(pm *websocket.PreparedMessage) {
	for _, sh := range s.shards {
		select {
		case sh.eventChan <- pm:
		default:
			// eventChan переполнен — крайне маловероятно при cap=256 и 20 join/sec.
			metrics.SendChannelDropped.Inc()
		}
	}
}

// notifyPlayerJoined уведомляет всех игроков о новом игроке.
// Клиент фильтрует собственный join по player ID.
func (s *Server) notifyPlayerJoined(newPlayer *types.Player) {
	playerState := types.PlayerState{
		ID:          newPlayer.ID,
		X:           uint16(newPlayer.GetX()),
		Y:           uint16(newPlayer.GetY()),
		FacingRight: true,
	}
	data := s.protocol.EncodePlayerJoined(playerState)
	pm, err := websocket.NewPreparedMessage(websocket.BinaryMessage, data)
	if err != nil {
		slog.Error("failed to prepare player joined message", "error", err)
		return
	}
	s.broadcastEvent(pm)
}

// notifyPlayerLeft уведомляет всех игроков об отключении.
func (s *Server) notifyPlayerLeft(leftPlayerID uint32) {
	data := s.protocol.EncodePlayerLeft(leftPlayerID)
	pm, err := websocket.NewPreparedMessage(websocket.BinaryMessage, data)
	if err != nil {
		slog.Error("failed to prepare player left message", "error", err)
		return
	}
	s.broadcastEvent(pm)
}
