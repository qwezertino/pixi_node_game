package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// LoadTest проводит нагрузочное тестирование сервера
func main() {
	serverURL := "ws://localhost:8108/ws"
	numClients := 1000 // Начнем с 1K клиентов
	duration := 30 * time.Second

	log.Printf("🧪 Starting load test: %d clients for %v", numClients, duration)

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var wg sync.WaitGroup
	connected := make(chan struct{}, numClients)
	errors := make(chan error, numClients)

	// Статистика
	var connectCount, errorCount, messageCount int64

	// Запуск клиентов
	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			if err := runClient(ctx, serverURL, clientID, connected, errors); err != nil {
				select {
				case errors <- err:
				default:
				}
			}
		}(i)

		// Throttle connection rate
		if i%50 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Мониторинг статистики
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-connected:
				connectCount++
			case err := <-errors:
				errorCount++
				log.Printf("❌ Client error: %v", err)
			case <-ticker.C:
				log.Printf("📊 Connected: %d, Errors: %d, Messages: %d",
					connectCount, errorCount, messageCount)
			}
		}
	}()

	wg.Wait()
	log.Printf("✅ Load test completed: %d connections, %d errors", connectCount, errorCount)
}

func runClient(ctx context.Context, serverURL string, clientID int, connected chan<- struct{}, errors chan<- error) error {
	u, err := url.Parse(serverURL)
	if err != nil {
		return err
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("client %d failed to connect: %v", clientID, err)
	}
	defer conn.Close()

	connected <- struct{}{}

	// Send movement commands periodically
	ticker := time.NewTicker(100 * time.Millisecond) // 10 Hz movement
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-ticker.C:
			// Send random movement
			movement := createMoveMessage()
			if err := conn.WriteMessage(websocket.BinaryMessage, movement); err != nil {
				return fmt.Errorf("client %d write error: %v", clientID, err)
			}

		default:
			// Read messages from server
			conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
					return fmt.Errorf("client %d read error: %v", clientID, err)
				}
				// Timeout is ok
			}
		}
	}
}

func createMoveMessage() []byte {
	// Simple move message: type (1) + packed movement (1) + sequence (4) = 6 bytes
	msg := make([]byte, 6)
	msg[0] = 0x01 // Move message type

	// Random movement vector (-1, 0, 1)
	directions := []int8{-1, 0, 1}
	dx := directions[time.Now().UnixNano()%3]
	dy := directions[(time.Now().UnixNano()/3)%3]

	// Pack movement
	packed := uint8(dx+1) | ((uint8(dy+1) & 0x03) << 2)
	msg[1] = packed

	// Sequence number (simple counter)
	sequence := uint32(time.Now().UnixNano() / 1000000) // milliseconds
	msg[2] = byte(sequence)
	msg[3] = byte(sequence >> 8)
	msg[4] = byte(sequence >> 16)
	msg[5] = byte(sequence >> 24)

	return msg
}
