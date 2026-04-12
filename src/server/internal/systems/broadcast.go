package systems

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"pixi_game_server/internal/types"
)

// BroadcastManager управляет эффективной рассылкой сообщений
type BroadcastManager struct {
	// Worker pools
	workerCount int
	workers     []*BroadcastWorker
	workQueue   chan BroadcastJob

	// Performance optimization
	batchSize      int
	batchTimeout   time.Duration
	pendingBatches map[uint32]*MessageBatch
	batchMutex     sync.Mutex

	// Stats
	messagesPerSec uint64
	batchesSent    uint64
	workersActive  uint32
}

// BroadcastJob представляет задачу рассылки
type BroadcastJob struct {
	SenderID  uint32
	Message   []byte
	TargetIDs []uint32
	Priority  JobPriority
	Timestamp int64
}

// MessageBatch группирует сообщения для batch отправки
type MessageBatch struct {
	Messages  [][]byte
	TargetIDs []uint32
	CreatedAt time.Time
	SenderID  uint32
}

// BroadcastWorker обрабатывает рассылку в отдельной goroutine
type BroadcastWorker struct {
	id       int
	manager  *BroadcastManager
	jobQueue chan BroadcastJob
	isActive uint32 // atomic flag
}

// JobPriority определяет приоритет задачи рассылки
type JobPriority uint8

const (
	PriorityLow    JobPriority = 0
	PriorityNormal JobPriority = 1
	PriorityHigh   JobPriority = 2
	PriorityUrgent JobPriority = 3
)

// NewBroadcastManager создает новый менеджер рассылки
func NewBroadcastManager(workerCount int) *BroadcastManager {
	if workerCount <= 0 {
		workerCount = 4 // Default
	}

	bm := &BroadcastManager{
		workerCount:    workerCount,
		workQueue:      make(chan BroadcastJob, 10000), // Large buffer
		batchSize:      100,                            // Batch up to 100 messages
		batchTimeout:   5 * time.Millisecond,           // Send batch after 5ms
		pendingBatches: make(map[uint32]*MessageBatch),
	}

	// Create workers
	bm.workers = make([]*BroadcastWorker, workerCount)
	for i := 0; i < workerCount; i++ {
		bm.workers[i] = &BroadcastWorker{
			id:       i,
			manager:  bm,
			jobQueue: make(chan BroadcastJob, 1000),
		}
		go bm.workers[i].run()
	}

	// Start batch processor
	go bm.batchProcessor()

	slog.Info("broadcast manager initialized", "workers", workerCount)
	return bm
}

// Broadcast отправляет сообщение списку игроков
func (bm *BroadcastManager) Broadcast(senderID uint32, message []byte, targetIDs []uint32, priority JobPriority) {
	job := BroadcastJob{
		SenderID:  senderID,
		Message:   message,
		TargetIDs: targetIDs,
		Priority:  priority,
		Timestamp: time.Now().UnixNano(),
	}

	// Try to queue job
	select {
	case bm.workQueue <- job:
		// Queued successfully
	default:
		// Queue full, handle based on priority
		if priority >= PriorityHigh {
			// Force queue for high priority
			go func() {
				bm.workQueue <- job
			}()
		}
		// Drop low priority messages when overloaded
	}
}

// BroadcastToViewport рассылает сообщение игрокам в viewport
func (bm *BroadcastManager) BroadcastToViewport(senderID uint32, message []byte, viewport types.ViewportBounds, allPlayers []types.PlayerState) {
	var targetIDs []uint32

	// Filter players by viewport
	for _, player := range allPlayers {
		if player.ID != senderID &&
			player.X >= viewport.MinX && player.X <= viewport.MaxX &&
			player.Y >= viewport.MinY && player.Y <= viewport.MaxY {
			targetIDs = append(targetIDs, player.ID)
		}
	}

	if len(targetIDs) > 0 {
		bm.Broadcast(senderID, message, targetIDs, PriorityNormal)
	}
}

// batchProcessor группирует сообщения в batch'и
func (bm *BroadcastManager) batchProcessor() {
	ticker := time.NewTicker(bm.batchTimeout)
	defer ticker.Stop()

	for {
		select {
		case job := <-bm.workQueue:
			bm.addToBatch(job)

		case <-ticker.C:
			bm.flushBatches()
		}
	}
}

// addToBatch добавляет задачу в batch
func (bm *BroadcastManager) addToBatch(job BroadcastJob) {
	bm.batchMutex.Lock()
	defer bm.batchMutex.Unlock()

	// Get or create batch for sender
	batch, exists := bm.pendingBatches[job.SenderID]
	if !exists {
		batch = &MessageBatch{
			SenderID:  job.SenderID,
			CreatedAt: time.Now(),
		}
		bm.pendingBatches[job.SenderID] = batch
	}

	// Add message to batch
	batch.Messages = append(batch.Messages, job.Message)
	batch.TargetIDs = append(batch.TargetIDs, job.TargetIDs...)

	// Send batch if it's full or urgent
	if len(batch.Messages) >= bm.batchSize || job.Priority >= PriorityHigh {
		bm.sendBatch(batch)
		delete(bm.pendingBatches, job.SenderID)
	}
}

// flushBatches отправляет все накопившиеся batch'и
func (bm *BroadcastManager) flushBatches() {
	bm.batchMutex.Lock()
	defer bm.batchMutex.Unlock()

	now := time.Now()
	for senderID, batch := range bm.pendingBatches {
		// Send batches older than timeout
		if now.Sub(batch.CreatedAt) >= bm.batchTimeout {
			bm.sendBatch(batch)
			delete(bm.pendingBatches, senderID)
		}
	}
}

// sendBatch отправляет batch воркеру
func (bm *BroadcastManager) sendBatch(batch *MessageBatch) {
	// Find least loaded worker
	workerIndex := bm.findBestWorker()
	worker := bm.workers[workerIndex]

	// Create job for the batch
	job := BroadcastJob{
		SenderID:  batch.SenderID,
		Message:   bm.combineBatchMessages(batch.Messages),
		TargetIDs: bm.deduplicateTargets(batch.TargetIDs),
		Priority:  PriorityNormal,
		Timestamp: time.Now().UnixNano(),
	}

	select {
	case worker.jobQueue <- job:
		atomic.AddUint64(&bm.batchesSent, 1)
	default:
		// Worker queue full, try next worker
		for i := 1; i < len(bm.workers); i++ {
			nextWorker := bm.workers[(workerIndex+i)%len(bm.workers)]
			select {
			case nextWorker.jobQueue <- job:
				atomic.AddUint64(&bm.batchesSent, 1)
				return
			default:
				continue
			}
		}
		// All workers busy, drop the batch (graceful degradation)
		slog.Warn("all broadcast workers busy, dropping batch", "sender_id", batch.SenderID)
	}
}

// findBestWorker находит наименее загруженного воркера
func (bm *BroadcastManager) findBestWorker() int {
	bestWorker := 0
	minQueueSize := len(bm.workers[0].jobQueue)

	for i, worker := range bm.workers {
		queueSize := len(worker.jobQueue)
		if queueSize < minQueueSize {
			minQueueSize = queueSize
			bestWorker = i
		}
	}

	return bestWorker
}

// combineBatchMessages объединяет сообщения batch'а
func (bm *BroadcastManager) combineBatchMessages(messages [][]byte) []byte {
	// Простая конкатенация, в реальности можно использовать compression
	totalSize := 0
	for _, msg := range messages {
		totalSize += len(msg)
	}

	combined := make([]byte, totalSize)
	offset := 0
	for _, msg := range messages {
		copy(combined[offset:], msg)
		offset += len(msg)
	}

	return combined
}

// deduplicateTargets удаляет дубликаты из списка целевых игроков
func (bm *BroadcastManager) deduplicateTargets(targets []uint32) []uint32 {
	seen := make(map[uint32]bool)
	unique := make([]uint32, 0, len(targets))

	for _, target := range targets {
		if !seen[target] {
			seen[target] = true
			unique = append(unique, target)
		}
	}

	return unique
}

// run запускает воркер рассылки
func (w *BroadcastWorker) run() {
	atomic.StoreUint32(&w.isActive, 1)
	defer atomic.StoreUint32(&w.isActive, 0)

	for job := range w.jobQueue {
		w.processJob(job)
		atomic.AddUint64(&w.manager.messagesPerSec, 1)
	}
}

// processJob обрабатывает одну задачу рассылки
func (w *BroadcastWorker) processJob(job BroadcastJob) {
	// В реальной реализации здесь была бы отправка через WebSocket connections
	// Для демонстрации просто логируем
	if len(job.TargetIDs) > 10 { // Log only large broadcasts
		slog.Debug("broadcasting", "worker_id", w.id, "bytes", len(job.Message), "targets", len(job.TargetIDs), "sender_id", job.SenderID)
	}
}

// GetStats возвращает статистику производительности
func (bm *BroadcastManager) GetStats() map[string]uint64 {
	activeWorkers := uint64(0)
	for _, worker := range bm.workers {
		if atomic.LoadUint32(&worker.isActive) == 1 {
			activeWorkers++
		}
	}

	return map[string]uint64{
		"messages_per_sec": atomic.LoadUint64(&bm.messagesPerSec),
		"batches_sent":     atomic.LoadUint64(&bm.batchesSent),
		"active_workers":   activeWorkers,
		"queue_size":       uint64(len(bm.workQueue)),
		"pending_batches":  uint64(len(bm.pendingBatches)),
	}
}
