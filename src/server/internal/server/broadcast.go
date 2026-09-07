package server

import (
	"container/heap"
	"encoding/binary"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gobwas/ws"

	"pixi_game_server/internal/clock"
	"pixi_game_server/internal/metrics"
	"pixi_game_server/internal/protocol"
	"pixi_game_server/internal/types"
)

func wsFrameSlice(slot []byte) []byte {
	payloadLen := len(slot) - 10
	switch {
	case payloadLen < 126:
		slot[8] = 0x82
		slot[9] = byte(payloadLen)
		return slot[8:]
	case payloadLen <= 65535:
		slot[6] = 0x82
		slot[7] = 0x7E
		slot[8] = byte(payloadLen >> 8)
		slot[9] = byte(payloadLen)
		return slot[6:]
	default:
		slot[0] = 0x82
		slot[1] = 0x7F
		slot[2] = byte(payloadLen >> 56)
		slot[3] = byte(payloadLen >> 48)
		slot[4] = byte(payloadLen >> 40)
		slot[5] = byte(payloadLen >> 32)
		slot[6] = byte(payloadLen >> 24)
		slot[7] = byte(payloadLen >> 16)
		slot[8] = byte(payloadLen >> 8)
		slot[9] = byte(payloadLen)
		return slot
	}
}

type tickFrame struct {
	data  []byte
	frame []byte
	refs  int32
}

func (f *tickFrame) release() {
	if atomic.AddInt32(&f.refs, -1) == 0 {
		f.data = f.data[:0]
		f.frame = nil
		broadcastFramePool.Put(f)
	}
}

var broadcastFramePool = sync.Pool{
	New: func() any {
		return &tickFrame{data: make([]byte, 0, 65536)}
	},
}

var connectionSlicePool = sync.Pool{
	New: func() any {
		s := make([]*Connection, 0, 4096)
		return &s
	},
}

var recipientSlicePool = sync.Pool{
	New: func() any {
		s := make([]*Connection, 0, 4096)
		return &s
	},
}

var scoredConnPool = sync.Pool{
	New: func() any {
		s := make(topKMinHeap, 0, 4096)
		return &s
	},
}

type scoredConnection struct {
	conn    *Connection
	score   int64
	rrBias  int64
	overdue bool
}

type topKMinHeap []scoredConnection

func (h topKMinHeap) Len() int { return len(h) }
func (h topKMinHeap) Less(i, j int) bool {
	if h[i].score == h[j].score {
		return h[i].rrBias < h[j].rrBias
	}
	return h[i].score < h[j].score
}
func (h topKMinHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *topKMinHeap) Push(x any) {
	*h = append(*h, x.(scoredConnection))
}

func (h *topKMinHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

const (

	broadcastWriteTimeout = 100 * time.Millisecond

	directWriteTimeout = 30 * time.Millisecond

	dilationBpsFull = 10000
	minDilationBps  = 3000

	dilationDebounceSevereTicks   = 2
	dilationDebounceModerateTicks = 4

	maxWriteFailures = 150

	writeChanSize = 32

	maxWriteBatchSizeLimit = 64
)

type writeJob struct {
	frame          *tickFrame
	direct         []byte
	ack            bool
	ackID          uint32
	ackX           uint16
	ackY           uint16
	ackSeq         uint32
	pong           bool
	pongNonce      uint32
	stateCreatedNs int64
	enqueuedNs     int64
	timeout        time.Duration
}

func writeJobFrame(job *writeJob, ackBuffer *[15]byte) []byte {
	if job.frame != nil {
		return job.frame.frame
	}
	if job.pong {
		ackBuffer[0] = 0x82
		ackBuffer[1] = 5
		ackBuffer[2] = protocol.MessagePong
		binary.LittleEndian.PutUint32(ackBuffer[3:], job.pongNonce)
		return ackBuffer[:7]
	}
	if !job.ack {
		return job.direct
	}

	ackBuffer[0] = 0x82
	ackBuffer[1] = 13
	ackBuffer[2] = protocol.MessageMovementAck
	binary.LittleEndian.PutUint32(ackBuffer[3:], job.ackID)
	binary.LittleEndian.PutUint16(ackBuffer[7:], job.ackX)
	binary.LittleEndian.PutUint16(ackBuffer[9:], job.ackY)
	binary.LittleEndian.PutUint32(ackBuffer[11:], job.ackSeq)
	return ackBuffer[:]
}

func (s *Server) sendPong(conn *Connection, nonce uint32) {
	select {
	case conn.writeCh <- writeJob{pong: true, pongNonce: nonce, timeout: directWriteTimeout}:
	default:
		metrics.BroadcastsDropped.Inc()
	}
}

func (s *Server) enqueueBroadcastJob(conn *Connection, frame *tickFrame, stateCreatedNs int64) bool {
	live := s.live.Load()
	if live.FanoutQueueShedDepth > 0 {
		depth := len(conn.writeCh)
		if depth >= live.FanoutQueueShedDepth {
			metrics.WSWriteQueueDepth.Observe(float64(depth))

			frame.release()
			metrics.BroadcastsShed.Inc()
			return false
		}
	}

	if !atomic.CompareAndSwapInt32(&conn.pendingBroadcast, 0, 1) {

		frame.release()
		metrics.BroadcastsShed.Inc()
		return true
	}

	atomic.StoreInt64(&conn.pendingStateNs, stateCreatedNs)
	select {
	case conn.writeCh <- writeJob{
		frame:          frame,
		stateCreatedNs: stateCreatedNs,
		enqueuedNs:     clock.Now(),
		timeout:        broadcastWriteTimeout,
	}:
		if atomic.LoadInt32(&conn.fanoutDrops) != 0 {
			atomic.StoreInt32(&conn.fanoutDrops, 0)
		}
		return true
	default:
		atomic.StoreInt64(&conn.pendingStateNs, 0)
		atomic.StoreInt32(&conn.pendingBroadcast, 0)
		frame.release()
		metrics.BroadcastsDropped.Inc()
		if atomic.AddInt32(&conn.fanoutDrops, 1) == live.FanoutDropStreak {
			go s.cleanupConnection(conn)
		}
		return false
	}
}

func (s *Server) startWriteLoop(c *Connection) {
	go func() {
		batchSize := s.live.Load().WriteBatchSize
		if batchSize < 1 {
			batchSize = 1
		} else if batchSize > maxWriteBatchSizeLimit {
			batchSize = maxWriteBatchSizeLimit
		}

		jobs := make([]writeJob, batchSize)
		frames := make([][]byte, batchSize)
		ackBuffers := make([][15]byte, batchSize)

		for {
			select {
			case first := <-c.writeCh:
				jobs[0] = first
				frames[0] = writeJobFrame(&jobs[0], &ackBuffers[0])

				count := 1
				maxTimeout := first.timeout
				for count < batchSize {
					select {
					case job := <-c.writeCh:
						jobs[count] = job
						frames[count] = writeJobFrame(&jobs[count], &ackBuffers[count])
						if job.timeout > maxTimeout {
							maxTimeout = job.timeout
						}
						count++
					default:
						goto writeBatch
					}
				}

			writeBatch:
				writeStart := time.Now()
				writeStartNs := clock.SinceEpoch(writeStart)
				for i := 0; i < count; i++ {
					if jobs[i].stateCreatedNs == 0 {
						continue
					}
					queueDelay := time.Duration(writeStartNs - jobs[i].enqueuedNs)
					stateAge := time.Duration(writeStartNs - jobs[i].stateCreatedNs)
					metrics.WorldStateQueueDelay.Observe(queueDelay.Seconds())
					metrics.WorldStateAgeAtWriteStart.Observe(stateAge.Seconds())
				}
				c.rawConn.SetWriteDeadline(time.Now().Add(maxTimeout))

				var n int64
				var err error
				if count == 1 {
					var nn int
					nn, err = c.rawConn.Write(frames[0])
					n = int64(nn)
				} else {
					buffers := net.Buffers(frames[:count])
					n, err = buffers.WriteTo(c.rawConn)
				}
				metrics.WSWriteBatchDuration.Observe(time.Since(writeStart).Seconds())
				metrics.WSWriteBatchJobs.Observe(float64(count))

				writeEndNs := clock.Now()
				for i := 0; i < count; i++ {
					if jobs[i].stateCreatedNs == 0 {
						continue
					}
					ageNs := writeEndNs - jobs[i].stateCreatedNs
					metrics.WorldStateAgeAtWriteEnd.Observe(time.Duration(ageNs).Seconds())
					atomic.StoreInt64(&c.lastWriteAgeNs, ageNs)
					atomic.StoreInt64(&c.lastWriteObservedNs, writeEndNs)
				}
				fatalWriteFailure := false
				if err != nil {
					metrics.WSWriteErrors.Inc()
					if atomic.AddInt32(&c.writeFailures, 1) >= maxWriteFailures {
						fatalWriteFailure = true
					}
				} else {
					atomic.StoreInt32(&c.writeFailures, 0)
					metrics.BytesSent.Add(float64(n))
					for i := 0; i < count; i++ {
						if jobs[i].stateCreatedNs == 0 {
							continue
						}
						atomic.StoreInt64(&c.lastWorldStateSentNs, writeEndNs)
					}
				}

				for i := 0; i < count; i++ {
					if jobs[i].frame != nil {
						atomic.StoreInt64(&c.pendingStateNs, 0)
						atomic.StoreInt32(&c.pendingBroadcast, 0)
						jobs[i].frame.release()
					}
					frames[i] = nil
					jobs[i] = writeJob{}
				}

				if fatalWriteFailure {
					go s.cleanupConnection(c)

					drainWriteCh(c.writeCh)
					return
				}

			case <-c.ctx.Done():

				drainWriteCh(c.writeCh)
				return
			}
		}
	}()
}

func drainWriteCh(ch chan writeJob) {
	for {
		select {
		case job := <-ch:
			if job.frame != nil {
				job.frame.release()
			}
		default:
			return
		}
	}
}

func (s *Server) selectRecipients(conns []*Connection, nowNs int64, hardLimit int) ([]*Connection, int, *[]*Connection) {
	n := len(conns)
	if n == 0 {
		return conns[:0], 0, nil
	}

	live := s.live.Load()

	limit := n
	if live.FanoutMaxRecipientsPerTick > 0 {
		curr := int(atomic.LoadInt64(&s.fanoutRecipientLimit))
		if curr < live.FanoutMinRecipientsPerTick {
			curr = live.FanoutMinRecipientsPerTick
		}
		if curr > live.FanoutMaxRecipientsPerTick {
			curr = live.FanoutMaxRecipientsPerTick
		}
		if curr < limit {
			limit = curr
		}
	}
	if hardLimit > 0 && hardLimit < limit {
		limit = hardLimit
	}

	if limit >= n {
		overdue := 0
		for _, conn := range conns {
			stalenessNs := nowNs - atomic.LoadInt64(&conn.lastWorldStateSentNs)
			idleForNs := nowNs - atomic.LoadInt64(&conn.lastActivity)
			deadlineNs := live.WorldStateIdleStalenessNs
			if idleForNs <= live.WorldStateActiveWindowNs {
				deadlineNs = live.WorldStateActiveStalenessNs
			}
			if stalenessNs >= deadlineNs {
				overdue++
			}
		}
		return conns, overdue, nil
	}

	selectedPtr := recipientSlicePool.Get().(*[]*Connection)
	selected := (*selectedPtr)[:0]
	if cap(selected) < limit {
		selected = make([]*Connection, 0, limit)
	}
	activeWindowNs := live.WorldStateActiveWindowNs
	activeStalenessNs := live.WorldStateActiveStalenessNs
	idleStalenessNs := live.WorldStateIdleStalenessNs
	debtWeightNs := live.FanoutFairDebtWeightNs
	roundRobinWeightNs := live.FanoutRoundRobinWeightNs
	criticalBoostNs := live.FanoutCriticalBoostNs
	rrEpoch := atomic.AddInt64(&s.fanoutRoundRobinEpoch, 1)
	modBase := int64(n)
	if modBase <= 0 {
		modBase = 1
	}
	if idleStalenessNs < activeStalenessNs {
		idleStalenessNs = activeStalenessNs
	}

	topPtr := scoredConnPool.Get().(*topKMinHeap)
	top := (*topPtr)[:0]
	if cap(top) < limit {
		top = make(topKMinHeap, 0, limit)
	}

	for _, conn := range conns {
		stalenessNs := nowNs - atomic.LoadInt64(&conn.lastWorldStateSentNs)
		if stalenessNs < 0 {
			stalenessNs = 0
		}

		idleForNs := nowNs - atomic.LoadInt64(&conn.lastActivity)
		deadlineNs := idleStalenessNs
		active := idleForNs <= activeWindowNs
		if active {
			deadlineNs = activeStalenessNs
		}

		isOverdue := stalenessNs >= deadlineNs

		score := stalenessNs
		if active {
			score += deadlineNs / 2
		}
		if isOverdue {
			score += deadlineNs
		}
		if criticalBoostNs > 0 && nowNs <= atomic.LoadInt64(&conn.criticalUntilNs) {
			score += criticalBoostNs
		}
		if debtWeightNs > 0 {
			debt := int64(atomic.LoadInt32(&conn.fanoutFairDebt))
			if debt > 0 {
				score += debt * debtWeightNs
			}
		}
		rrBias := int64(0)
		if roundRobinWeightNs > 0 {
			rrRank := (int64(conn.player.ID) + rrEpoch) % modBase
			rrBias = (modBase - rrRank) * roundRobinWeightNs / modBase
			score += rrBias
		}
		drops := int64(atomic.LoadInt32(&conn.fanoutDrops))
		if drops > 0 {
			penalty := drops * (deadlineNs / 8)
			if penalty > score/2 {
				penalty = score / 2
			}
			score -= penalty
		}

		item := scoredConnection{conn: conn, score: score, rrBias: rrBias, overdue: isOverdue}
		if len(top) < limit {

			top = append(top, item)
			if len(top) == limit {

				heap.Init(&top)
			}
			continue
		}

		if item.score > top[0].score || (item.score == top[0].score && item.rrBias > top[0].rrBias) {
			top[0] = item
			heap.Fix(&top, 0)
		}
	}

	if len(top) > 0 && len(top) < limit {
		heap.Init(&top)
	}

	overdueSelected := 0
	for i := range top {
		selected = append(selected, top[i].conn)
		if top[i].overdue {
			overdueSelected++
		}
		top[i] = scoredConnection{}
	}
	*topPtr = top[:0]
	scoredConnPool.Put(topPtr)

	*selectedPtr = selected
	return selected, overdueSelected, selectedPtr
}

func releaseConnSlice(conns []*Connection, buf *[]*Connection) {
	for i := range conns {
		conns[i] = nil
	}
	*buf = conns[:0]
	connectionSlicePool.Put(buf)
}

func releaseRecipientSlice(recipients []*Connection, recipientPtr *[]*Connection) {
	for i := range recipients {
		recipients[i] = nil
	}
	*recipientPtr = recipients[:0]
	recipientSlicePool.Put(recipientPtr)
}

func (s *Server) updateFairDebt(conns []*Connection, recipients []*Connection) {
	live := s.live.Load()
	if live.FanoutFairDebtMax <= 0 || len(conns) == 0 {
		return
	}
	epoch := atomic.AddUint32(&s.fanoutDebtEpoch, 1)
	if epoch == 0 {
		epoch = atomic.AddUint32(&s.fanoutDebtEpoch, 1)
	}
	for _, conn := range recipients {
		atomic.StoreUint32(&conn.fanoutDebtEpoch, epoch)
	}

	for _, conn := range conns {
		if atomic.LoadUint32(&conn.fanoutDebtEpoch) == epoch {
			if live.FanoutFairDebtDec <= 0 {
				atomic.StoreInt32(&conn.fanoutFairDebt, 0)
				continue
			}
			for {
				current := atomic.LoadInt32(&conn.fanoutFairDebt)
				if current <= 0 {
					break
				}
				next := current - live.FanoutFairDebtDec
				if next < 0 {
					next = 0
				}
				if atomic.CompareAndSwapInt32(&conn.fanoutFairDebt, current, next) {
					break
				}
			}
			continue
		}

		if live.FanoutFairDebtInc <= 0 {
			continue
		}
		for {
			current := atomic.LoadInt32(&conn.fanoutFairDebt)
			if current >= live.FanoutFairDebtMax {
				break
			}
			next := current + live.FanoutFairDebtInc
			if next > live.FanoutFairDebtMax {
				next = live.FanoutFairDebtMax
			}
			if atomic.CompareAndSwapInt32(&conn.fanoutFairDebt, current, next) {
				break
			}
		}
	}
}

func (s *Server) tuneRecipientLimit(total, selected, overdue, dropped int, fanoutDur time.Duration) {
	live := s.live.Load()
	if live.FanoutMaxRecipientsPerTick <= 0 {
		return
	}

	rawCurr := int(atomic.LoadInt64(&s.fanoutRecipientLimit))
	if rawCurr < 1 {
		rawCurr = min(total, live.FanoutMinRecipientsPerTick)
		if rawCurr < 1 {
			rawCurr = 1
		}
	}
	curr := rawCurr
	next := curr

	if total >= live.FanoutMinRecipientsPerTick && curr < live.FanoutMinRecipientsPerTick {

		next = live.FanoutMinRecipientsPerTick
	}

	if overdue > next {
		next = overdue
	}

	if dropped > 0 || fanoutDur > live.FanoutTarget*3/2 {
		next = int(float64(next) * 0.9)
	} else if fanoutDur > live.FanoutTarget {
		next = int(float64(next) * 0.95)
	} else if fanoutDur < live.FanoutTarget/2 && selected >= curr*9/10 {
		next = int(float64(next) * 1.05)
		if next == curr {
			next++
		}
	}

	if next < live.FanoutMinRecipientsPerTick {
		next = live.FanoutMinRecipientsPerTick
	}
	if next > live.FanoutMaxRecipientsPerTick {
		next = live.FanoutMaxRecipientsPerTick
	}
	if next > total {
		next = total
	}
	if next < live.FanoutMinRecipientsPerTick && total >= live.FanoutMinRecipientsPerTick {
		next = live.FanoutMinRecipientsPerTick
	}

	if next != rawCurr {
		atomic.StoreInt64(&s.fanoutRecipientLimit, int64(next))
		metrics.FanoutRecipientLimit.Set(float64(next))

		nowNano := clock.Now()
		prev := atomic.LoadInt64(&s.lastFanoutTuneLog)
		if nowNano-prev >= int64(5*time.Second) &&
			atomic.CompareAndSwapInt64(&s.lastFanoutTuneLog, prev, nowNano) {
			slog.Info("fanout recipient limit updated",
				"from", rawCurr,
				"to", next,
				"selected", selected,
				"overdue", overdue,
				"fanout_ms", fanoutDur.Milliseconds(),
				"target_ms", live.FanoutTarget.Milliseconds(),
				"dropped_jobs", dropped)
		}
	}
}

func (s *Server) tuneTimeDilation(writePressure, fanoutDur, computeDur time.Duration) {
	nominal := s.gameWorld.GetNominalTickInterval()
	if nominal <= 0 {
		return
	}

	severe := writePressure > 75*time.Millisecond || fanoutDur > 30*time.Millisecond ||
		computeDur > nominal+nominal/2
	moderate := !severe && (writePressure > 30*time.Millisecond || fanoutDur > 15*time.Millisecond ||
		computeDur > nominal)
	clear := writePressure < 10*time.Millisecond && fanoutDur < 6*time.Millisecond &&
		computeDur < nominal/2

	var severeStreak, moderateStreak int64
	switch {
	case severe:
		severeStreak = atomic.AddInt64(&s.dilationSevereStreak, 1)
		atomic.StoreInt64(&s.dilationModerateStreak, 0)
	case moderate:
		moderateStreak = atomic.AddInt64(&s.dilationModerateStreak, 1)
		atomic.StoreInt64(&s.dilationSevereStreak, 0)
	default:
		atomic.StoreInt64(&s.dilationSevereStreak, 0)
		atomic.StoreInt64(&s.dilationModerateStreak, 0)
	}

	curr := atomic.LoadInt64(&s.dilationBps)
	if curr <= 0 {
		curr = dilationBpsFull
	}
	next := curr

	switch {
	case severe && severeStreak >= dilationDebounceSevereTicks:
		next = curr - 1000
	case moderate && moderateStreak >= dilationDebounceModerateTicks:
		next = curr - 500
	case clear && curr < dilationBpsFull:
		next = curr + 200
	}

	if next < minDilationBps {
		next = minDilationBps
	}
	if next > dilationBpsFull {
		next = dilationBpsFull
	}

	if next == curr {
		return
	}
	atomic.StoreInt64(&s.dilationBps, next)
	if next < curr {
		metrics.TimeDilationChanges.WithLabelValues("down").Inc()
	} else {
		metrics.TimeDilationChanges.WithLabelValues("up").Inc()
	}
	metrics.TimeDilationPercent.Set(float64(next) / 100)

	newInterval := time.Duration(int64(nominal) * dilationBpsFull / next)
	s.gameWorld.SetTickInterval(newInterval)
	metrics.TickIntervalMs.Set(float64(newInterval.Milliseconds()))

	nowNano := clock.Now()
	prev := atomic.LoadInt64(&s.lastDilationLog)
	if nowNano-prev >= int64(5*time.Second) &&
		atomic.CompareAndSwapInt64(&s.lastDilationLog, prev, nowNano) {
		slog.Info("time dilation updated",
			"from_pct", curr/100,
			"to_pct", next/100,
			"tick_interval_ms", newInterval.Milliseconds(),
			"compute_ms", computeDur.Milliseconds(),
			"fanout_ms", fanoutDur.Milliseconds(),
			"write_pressure_ms", writePressure.Milliseconds())
	}
}

func (s *Server) currentDilationBps() uint16 {
	v := atomic.LoadInt64(&s.dilationBps)
	if v <= 0 {
		v = dilationBpsFull
	}
	if v > 65535 {
		v = 65535
	}
	return uint16(v)
}

func shouldEmitFrame(fullSync bool, changedCount int, velocityReplication bool) bool {
	return fullSync || changedCount > 0 || velocityReplication
}

func (s *Server) broadcastTick(allPlayers []types.PlayerState, changed []types.PlayerState, fullSync bool, worldTick uint32) bool {
	if len(allPlayers) == 0 {
		return false
	}

	live := s.live.Load()
	hasState := shouldEmitFrame(fullSync, len(changed), live.VelocityReplication)

	t1 := time.Now()
	sentAtNs := clock.SinceEpoch(t1)

	s.connectionsMu.RLock()
	n := len(s.connections)
	if n == 0 {
		s.connectionsMu.RUnlock()
		return true
	}
	buf := connectionSlicePool.Get().(*[]*Connection)
	conns := (*buf)[:0]
	if cap(conns) < n {
		conns = make([]*Connection, 0, n)
	}
	for _, conn := range s.connections {
		conns = append(conns, conn)
	}
	metrics.BroadcastTargets.Observe(float64(n))
	s.connectionsMu.RUnlock()

	s.enqueueAuthoritativeMovementAcks(conns)

	if !hasState {
		releaseConnSlice(conns, buf)
		return false
	}

	t0 := time.Now()

	stateSequence := atomic.AddUint32(&s.worldStateSeq, 1)
	f := broadcastFramePool.Get().(*tickFrame)
	f.data = f.data[:0]

	f.data = append(f.data, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	dilationBps := s.currentDilationBps()
	if fullSync {
		f.data = s.protocol.AppendGameState(f.data, allPlayers, stateSequence, worldTick, dilationBps)
		s.broadcastUnitRoster()
	} else {
		f.data = s.protocol.AppendDeltaGameState(f.data, changed, stateSequence, worldTick, dilationBps)
	}
	f.frame = wsFrameSlice(f.data)
	payloadSize := len(f.data) - 10
	if payloadSize > 0 {
		metrics.BroadcastPayloadBytes.Observe(float64(payloadSize))
	}
	if fullSync {
		metrics.BroadcastRecords.Observe(float64(len(allPlayers)))
	} else {
		metrics.BroadcastRecords.Observe(float64(len(changed)))
	}
	metrics.TickPhaseDuration.WithLabelValues("encode").Observe(time.Since(t0).Seconds())

	selectionLimit := n
	if live.FanoutMaxRecipientsPerTick > 0 {
		selectionLimit = int(atomic.LoadInt64(&s.fanoutRecipientLimit))
		if selectionLimit < live.FanoutMinRecipientsPerTick {
			selectionLimit = live.FanoutMinRecipientsPerTick
		}
		if selectionLimit > live.FanoutMaxRecipientsPerTick {
			selectionLimit = live.FanoutMaxRecipientsPerTick
		}
		if selectionLimit > n {
			selectionLimit = n
		}
	}

	budgetLimit := 0
	if budgetBytes := live.FanoutMaxBroadcastBytesPerTick; budgetBytes > 0 {
		frameBytes := len(f.frame)
		if frameBytes > 0 {
			budgetRecipients := budgetBytes / frameBytes
			if budgetRecipients < 1 {
				budgetRecipients = 1
			}
			metrics.BroadcastBudgetRecipients.Observe(float64(budgetRecipients))
			if budgetRecipients < selectionLimit {
				budgetLimit = budgetRecipients
				metrics.BroadcastBudgetHits.Inc()
				metrics.BroadcastBudgetTrimmed.Add(float64(selectionLimit - budgetRecipients))
			}
		}
	}

	selectStart := time.Now()
	recipients, overdue, recipientPtr := s.selectRecipients(conns, sentAtNs, budgetLimit)
	selectDur := time.Since(selectStart)
	metrics.TickFanoutSelectDuration.Observe(selectDur.Seconds())
	metrics.TickPhaseDuration.WithLabelValues("fanout_select").Observe(selectDur.Seconds())
	m := len(recipients)
	s.updateFairDebt(conns, recipients)
	if m == 0 {
		if recipientPtr != nil {
			releaseRecipientSlice(recipients, recipientPtr)
		}
		releaseConnSlice(conns, buf)

		f.data = f.data[:0]
		f.frame = nil
		broadcastFramePool.Put(f)
		return false
	}

	metrics.BroadcastRecipients.Observe(float64(m))
	metrics.BroadcastOverdueRecipients.Observe(float64(overdue))
	if deferred := n - m; deferred > 0 {
		metrics.BroadcastDeferred.Add(float64(deferred))
	}

	atomic.StoreInt32(&f.refs, int32(m))

	enqueueStart := time.Now()
	dropped := 0
	for _, conn := range recipients {
		if !s.enqueueBroadcastJob(conn, f, sentAtNs) {
			dropped++
		}
	}
	enqueueDur := time.Since(enqueueStart)
	metrics.TickFanoutEnqueueDuration.Observe(enqueueDur.Seconds())
	metrics.TickPhaseDuration.WithLabelValues("fanout_enqueue").Observe(enqueueDur.Seconds())

	if recipientPtr != nil {
		releaseRecipientSlice(recipients, recipientPtr)
	}
	pressure := populationWritePressure(conns, clock.Now())
	releaseConnSlice(conns, buf)

	fanoutDur := time.Since(t1)
	metrics.TickPhaseDuration.WithLabelValues("fanout_send").Observe(fanoutDur.Seconds())
	metrics.TickFanoutDuration.Observe(fanoutDur.Seconds())
	s.tuneRecipientLimit(n, m, overdue, dropped, fanoutDur)

	s.tuneTimeDilation(pressure, fanoutDur, s.gameWorld.GetTickDuration())

	if fanoutDur > 20*time.Millisecond {
		nowNano := clock.Now()
		prev := atomic.LoadInt64(&s.lastSlowFanoutLog)
		if nowNano-prev >= int64(5*time.Second) &&
			atomic.CompareAndSwapInt64(&s.lastSlowFanoutLog, prev, nowNano) {
			slog.Warn("slow broadcast fanout",
				"duration_ms", fanoutDur.Milliseconds(),
				"connections", n,
				"dropped_jobs", dropped,
				"payload_bytes", payloadSize,
				"full_sync", fullSync,
				"changed_players", len(changed),
				"all_players", len(allPlayers))
		}
	}
	return true
}

func (s *Server) broadcastEvent(frameBytes []byte) {
	s.connectionsMu.RLock()
	for _, conn := range s.connections {
		select {
		case conn.writeCh <- writeJob{direct: frameBytes, timeout: directWriteTimeout}:
		default:
			metrics.BroadcastsDropped.Inc()
		}
	}
	s.connectionsMu.RUnlock()
}

func (s *Server) sendInitialState(conn *Connection) {
	allPlayers := s.gameWorld.GetAllPlayers()

	f := broadcastFramePool.Get().(*tickFrame)
	f.data = f.data[:0]
	f.data = append(f.data, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	seq := atomic.LoadUint32(&s.worldStateSeq)

	worldTick := s.gameWorld.GetTickCount()
	f.data = s.protocol.AppendGameState(f.data, allPlayers, seq, worldTick, s.currentDilationBps())
	frame := wsFrameSlice(f.data)

	frameBytes := make([]byte, len(frame))
	copy(frameBytes, frame)

	f.data = f.data[:0]
	f.frame = nil
	broadcastFramePool.Put(f)

	select {
	case conn.writeCh <- writeJob{direct: frameBytes, timeout: directWriteTimeout}:
		atomic.StoreInt64(&conn.lastWorldStateSentNs, clock.Now())
	default:
		metrics.BroadcastsDropped.Inc()
	}
}

func (s *Server) sendDirect(conn *Connection, data []byte) {
	frameBytes, err := ws.CompileFrame(ws.NewBinaryFrame(data))
	if err != nil {
		return
	}
	select {
	case conn.writeCh <- writeJob{direct: frameBytes, timeout: directWriteTimeout}:
	default:
		metrics.BroadcastsDropped.Inc()
	}
}

func (s *Server) sendMovementAck(conn *Connection, playerID uint32, x, y uint16, inputSequence uint32) bool {
	select {
	case conn.writeCh <- writeJob{
		ack:     true,
		ackID:   playerID,
		ackX:    x,
		ackY:    y,
		ackSeq:  inputSequence,
		timeout: directWriteTimeout,
	}:
		return true
	default:
		metrics.BroadcastsDropped.Inc()
		return false
	}
}

func (s *Server) enqueueAuthoritativeMovementAcks(conns []*Connection) {
	for _, conn := range conns {
		sequence := conn.player.GetAppliedInputSequence()
		if sequence == atomic.LoadUint32(&conn.lastMovementAckSeq) {
			continue
		}
		x, y := conn.player.GetMovementAckPosition()
		if s.sendMovementAck(conn, conn.player.ID, x, y, sequence) {
			atomic.StoreUint32(&conn.lastMovementAckSeq, sequence)
		}
	}
}

func (s *Server) sendWelcome(conn *Connection) {
	data := s.protocol.EncodeWelcome(conn.player.ID, uint16(s.cfg.Game.TickRate), conn.player.GetUnitType())
	s.sendDirect(conn, data)
}

func (s *Server) sendUnitRoster(conn *Connection) {
	assignments := s.gameWorld.GetAllUnitAssignments()
	if len(assignments) == 0 {
		return
	}
	data := s.protocol.EncodeUnitRoster(assignments)
	s.sendDirect(conn, data)
}

func (s *Server) broadcastUnitRoster() {
	assignments := s.gameWorld.GetAllUnitAssignments()
	if len(assignments) == 0 {
		return
	}
	data := s.protocol.EncodeUnitRoster(assignments)
	frameBytes, err := ws.CompileFrame(ws.NewBinaryFrame(data))
	if err != nil {
		slog.Error("failed to compile unit roster frame", "error", err)
		return
	}
	s.broadcastEvent(frameBytes)
}

func (s *Server) notifyPlayerJoined(newPlayer *types.Player) {
	playerState := types.PlayerState{
		ID:        newPlayer.ID,
		X:         uint16(newPlayer.GetX()),
		Y:         uint16(newPlayer.GetY()),
		Direction: protocol.DirectionRight,
	}
	data := s.protocol.EncodePlayerJoined(playerState)
	frameBytes, err := ws.CompileFrame(ws.NewBinaryFrame(data))
	if err != nil {
		slog.Error("failed to compile player joined frame", "error", err)
		return
	}
	s.broadcastEvent(frameBytes)
}

func (s *Server) notifyPlayerLeft(leftPlayerID uint32) {
	data := s.protocol.EncodePlayerLeft(leftPlayerID)
	frameBytes, err := ws.CompileFrame(ws.NewBinaryFrame(data))
	if err != nil {
		slog.Error("failed to compile player left frame", "error", err)
		return
	}
	s.broadcastEvent(frameBytes)
}

func (s *Server) runPingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	pingFrame, _ := ws.CompileFrame(ws.NewPingFrame(nil))

	for {
		select {
		case <-ticker.C:
			cutoff := clock.Now() - int64(90*time.Second)
			s.connectionsMu.RLock()
			for _, conn := range s.connections {
				if atomic.LoadInt64(&conn.lastActivity) < cutoff {

					go s.cleanupConnection(conn)
					continue
				}
				select {
				case conn.writeCh <- writeJob{direct: pingFrame, timeout: directWriteTimeout}:
				default:
				}
			}
			s.connectionsMu.RUnlock()

		case <-s.ctx.Done():
			return
		}
	}
}
