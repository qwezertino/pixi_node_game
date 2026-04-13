# Go WebSocket Server — Performance Research Notes

> Документ фиксирует результаты исследования production-кода centrifuge, gws и nbio.
> Цель была: 10 000 клиентов @ 30 Hz, tick p99 < 5 ms.
> Стартовые симптомы при 2 500 клиентах: tick p99=25ms, shard_write p99=21ms, ws_write_errors=476/s.

---

## Проблема 1: GC mark assists — главная причина всех латентностей

### Диагноз

`GOGC=100` (дефолт) — GC триггерится при росте heap на 100%.
При активном alloc на tick-пути (encode + pooling) mark assist вписывается прямо в
горутину, которая сейчас делает полезную работу. Результат:

- Горутина шарда начинает `SetWriteDeadline + Write` → GC assist паузит её на 10–100ms
- Deadline истёк ещё до вызова `Write` → `ws_write_error` для всех клиентов шарда
- GC STW сканирует стеки: при 2400 горутин это `2400 × 3µs ≈ 7ms` per GC cycle

### Что нашли в centrifuge (production config)

```
# centrifuge production .env (github.com/centrifugal/centrifuge)
GOGC=400        # heap может вырасти в 4× до следующего GC → assists редкие
GOMEMLIMIT=2GiB # жёсткий потолок: если heap → 2GB, GC всё равно сработает
```

Rationale из их README: при near-zero per-tick allocations (всё через sync.Pool)
heap растёт медленно, GOGC=400 означает GC раз в несколько секунд вместо раз в 33ms.
`GOMEMLIMIT` предотвращает OOM при lazy GC.

**Ключевой эффект побочного исследования**: epoll refactor снизил горутины с ~2400 до ~70.
STW scan: `70 × 3µs ≈ 0.2ms` — практически незаметно. Это само по себе устраняет
значительную часть GC-latency **без** GOGC=400.

### Что внедрили

```go
// cmd/server/main.go — optimizeRuntime()
func optimizeRuntime() {
    if os.Getenv("GOGC") == "" {
        os.Setenv("GOGC", "400")
    }
    // GOMEMLIMIT читается runtime автоматически из env
    memLimit := debug.SetMemoryLimit(-1)
    slog.Info("memory limit active", "limit_mb", memLimit/1024/1024)
}
```

```
# .env
GOGC=400
GOMEMLIMIT=2GiB
```

---

## Проблема 2: ws_write_errors=476/s — deadline слишком жёсткий

### Диагноз

```
SetWriteDeadline(time.Now().Add(1ms))
Write(frame)   // ← GC assist паузит горутину МЕЖДУ этими двумя строками
```

GC assist может начаться после `SetWriteDeadline`, но до `Write`. Если пауза > 1ms,
`Write` получает уже истёкший deadline → `i/o timeout` → `ws_write_error`.
При GOGC=100 и 2500 клиентах это происходит постоянно.

### Что нашли в gws (github.com/lxzan/gws)

```go
// gws/writer.go — их дефолтный write timeout
const defaultWriteTimeout = 5 * time.Second  // очень щедро для persistent conn
```

gws использует per-message deadline только при flush, а не per-write-call.
Ключевой insight: **GC assist длится максимум несколько миллисекунд при GOGC=400**.
Значит 5ms deadline полностью покрывает GC jitter и не даёт мёртвым соединениям
висеть вечно.

### Что внедрили

```go
// broadcast.go
const (
    broadcastWriteTimeout = 5 * time.Millisecond  // было 1ms → теперь 5ms
    directWriteTimeout    = 30 * time.Millisecond
)

func writeRawFrame(conn *Connection, timeout time.Duration, frameBytes []byte) error {
    conn.writeMu.Lock()
    conn.rawConn.SetWriteDeadline(time.Now().Add(timeout)) // relative deadline
    _, err := conn.rawConn.Write(frameBytes)
    conn.writeMu.Unlock()
    return err
}
```

Относительный дедлайн (`time.Now().Add(...)` вычисляется непосредственно перед Write)
гарантирует что медленная запись одному клиенту не крадёт бюджет у следующих.

---

## Проблема 3: tick p99=25ms — gameLoop однопоточный bottleneck

### Диагноз

Все 2500 игроков обрабатываются последовательно в одной горутине gameLoop:

```
tick():
    for _, player := range playersMap  // ← 2500 × updatePosition ≈ 13ms
        updatePlayerPosition(player)
        player.ToState()
        delta comparison
```

`range` = 13ms, `delta` = 2ms, `encode` = 3ms → итого 18ms при frame budget 33ms (30Hz).
При 10k игроков: `10000 × 5µs ≈ 50ms` — превышение в 1.5× budget.

### Что нашли в nbio (github.com/lesismal/nbio)

```go
// nbio/taskpool/taskpool.go — persistent worker pool
type TaskPool struct {
    taskChs []chan Task
    size    int
}

func (tp *TaskPool) Go(index int, task Task) {
    tp.taskChs[index%tp.size] <- task
}
```

**Ключевой паттерн**: воркеры создаются **один раз** при старте и живут вечно.
Каждый тик диспатчит chunk игроков в канал. Никакого `go func()` per tick.
Overhead: 0 (нет spawn) vs ~2µs/goroutine spawn × N workers.

### Что нашли в nakama (github.com/heroiclabs/nakama)

```go
// runtime worker pool pattern — аналогично nbio
type RuntimePool struct {
    pool chan *Runtime
}

// Persistent goroutines draining channel — pattern identical
```

### Что внедрили

```go
// world.go — persistent tick workers

type tickWorkerInput struct {
    ptrs          []*types.Player
    nowNano       int64
    attackDurNano int64
}

// Создаются ОДИН РАЗ в NewGameWorld. Живут до shutdown.
n := runtime.GOMAXPROCS(0)
gw.tickWorkerChs = make([]chan tickWorkerInput, n)
for i := range gw.tickWorkerChs {
    ch := make(chan tickWorkerInput, 1) // буфер=1: gameLoop не блокируется
    gw.tickWorkerChs[i] = ch
    go gw.runTickWorker(ch)
}

func (gw *GameWorld) runTickWorker(ch chan tickWorkerInput) {
    for input := range ch {
        for _, player := range input.ptrs {
            // attack timeout check
            if player.GetState() == 1 {
                if time.Now().UnixNano()-player.GetAttackStartTime() > input.attackDurNano {
                    player.SetState(0)
                }
            }
            gw.updatePlayerPosition(player, input.nowNano)
        }
        gw.tickWorkerWg.Done()
    }
}

// В tick() — критически важный порядок: Add ПЕРЕД sends
func (gw *GameWorld) tick() {
    // ...
    activeWorkers := countActiveChunks()
    gw.tickWorkerWg.Add(activeWorkers)  // ← СНАЧАЛА Add
    for i, ch := range gw.tickWorkerChs {
        ch <- tickWorkerInput{...}       // ← ПОТОМ send
    }
    gw.tickWorkerWg.Wait()
    // Далее: последовательный ToState() + delta (только atomic reads, быстро)
}
```

**Критический баг который поймали**: если сделать `Add` после первого `send`,
быстрый воркер может вызвать `Done()` до того как `Add` выполнился → panic.

---

## Проблема 4: shard_write p99=21ms — архитектура шардов

### Диагноз

N шардов (N = GOMAXPROCS), каждый пишет M = connections/N клиентам **последовательно**:

```
shard.run():
    case f := <-broadcastChan:
        for _, conn := range sh.conns {   // 2500/12 ≈ 208 соединений
            writeFrame(conn, 5ms, f.frame) // каждый Write под writeMu
        }
```

Проблема не в количестве записей, а в **scheduling latency**:
12 горутин просыпаются одновременно на `broadcastChan`. Go scheduler должен их все
запланировать. При соревновании за CPU scheduler preemption = 10–20ms для последних.

### Что нашли в centrifuge

```go
// centrifuge/hub.go — архитектура шардов = lock shards, NOT goroutine shards
type subShard struct {
    mu   sync.RWMutex
    subs map[string]map[uint64]*Subscription // playerID → subscriptions
}

// Per-client goroutine (не шардовый воркер):
func (c *Client) writePublication(pub *Publication) {
    select {
    case c.writeCh <- pubToProto(pub):  // non-blocking push
    default:
        // drop and count
    }
}

// Каждый клиент имеет свою горутину которая читает writeCh:
func (c *Client) writer() {
    for data := range c.writeCh {
        c.conn.Write(data)
    }
}
```

**Ключевой insight**: "шарды" в centrifuge — это lock partitioning для map доступа,
а не горутины-воркеры. Каждый клиент имеет **собственную** goroutine для записи.

### Что нашли в gws

```go
// gws/worker.go — lazy goroutine pattern (ключевая находка)
type workerQueue struct {
    mu   sync.Mutex
    q    []asyncJob
    head int
    busy bool
}

func (w *workerQueue) Push(job asyncJob) {
    w.mu.Lock()
    w.q = append(w.q, job)
    spawn := !w.busy
    if spawn { w.busy = true }
    w.mu.Unlock()
    if spawn { go w.do() }  // горутина спавнится ТОЛЬКО если нет активной
}

func (w *workerQueue) do() {
    for {
        w.mu.Lock()
        if w.head >= len(w.q) {
            w.q = w.q[:0]
            w.head = 0
            w.busy = false
            w.mu.Unlock()
            return  // горутина ВЫХОДИТ когда очередь пуста — нет idle blocker
        }
        job := w.q[w.head]
        w.q[w.head] = nil  // GC
        w.head++
        w.mu.Unlock()
        job()
    }
}
```

**Ключевые свойства паттерна:**
- В покое: 0 горутин на соединение
- При push: горутина спавнится если не запущена, дренирует ВСЁ, выходит
- На 30Hz: максимум 1 активная горутина per connection одновременно
- Scheduler contention: 0 (нет горутин ожидающих на канале одновременно)

**Почему это лучше шардов:**
- Шарды: N горутин просыпаются одновременно → scheduler должен разогнать все → p99 latency
- gws: каждая горутина независима, не привязана к broadcast event → равномерный schedule

### Что внедрили

```go
// broadcast.go — connWriteQueue (наш gws-адаптер)

type connWriteQueue struct {
    mu   sync.Mutex
    jobs []func()
    head int
    busy bool
}

func (q *connWriteQueue) push(job func()) {
    q.mu.Lock()
    q.jobs = append(q.jobs, job)
    spawn := !q.busy
    if spawn { q.busy = true }
    q.mu.Unlock()
    if spawn { go q.drain() }
}

func (q *connWriteQueue) drain() {
    for {
        q.mu.Lock()
        if q.head >= len(q.jobs) {
            q.jobs = q.jobs[:0]
            q.head = 0
            q.busy = false
            q.mu.Unlock()
            return
        }
        job := q.jobs[q.head]
        q.jobs[q.head] = nil  // не держим closure в памяти
        q.head++
        // compaction: когда >half consumed и ≥64 processed
        if q.head > 64 && q.head > len(q.jobs)/2 {
            n := copy(q.jobs, q.jobs[q.head:])
            q.jobs = q.jobs[:n]
            q.head = 0
        }
        q.mu.Unlock()
        job()
    }
}

// Connection struct — добавлено поле:
type Connection struct {
    // ...
    writeQueue connWriteQueue // zero value ready to use
}

// broadcastTick — fan-out под RLock:
func (s *Server) broadcastTick(allPlayers, changed []types.PlayerState, fullSync bool) {
    // encode once into pool buffer (ref-counted)...
    atomic.StoreInt32(&f.refs, int32(n))   // n = len(connections)
    s.connectionsMu.RLock()
    for _, conn := range s.connections {
        c := conn
        c.writeQueue.push(func() {
            err := writeRawFrame(c, broadcastWriteTimeout, f.frame)
            f.release()  // ref-count: когда 0 → pool
            // handle err...
        })
    }
    s.connectionsMu.RUnlock()
}
```

---

## Проблема 5: data race в ring buffer (критический баг → телепортация)

### Диагноз

Изначально использовался ring buffer из 32 слотов для broadcast frames:

```go
// БЫЛО (unsafe):
type ringBuffer struct {
    slots [32][]byte
    head  uint32
}

func (rb *ringBuffer) next() []byte {
    slot := rb.slots[atomic.AddUint32(&rb.head, 1) % 32]
    // шард держит ссылку на slot через f.frame
    return slot  // ← через 32 тика broadcastTick ПЕРЕЗАПИСЫВАЕТ этот же слот
}
```

Шард читает данные из слота пока gameLoop уже это слот перезаписывает.
Эффект: клиент получает смесь текущих и будущих данных → "телепортация" игроков.

Баг `AppendGameState`: запись начиналась с offset=0, уничтожая 10-байтный WS-заголовок:

```go
// БЫЛО — уничтожало WS header prefix:
func (bp *BinaryProtocol) AppendGameState(dst []byte, players []types.PlayerState) []byte {
    dst[0] = MessageGameState  // ← перезаписывало байт [0], а не [10]

// СТАЛО — true append:
func (bp *BinaryProtocol) AppendGameState(dst []byte, players []types.PlayerState) []byte {
    startOffset := len(dst)  // 10 (WS header prefix)
    // ...
    dst[startOffset] = MessageGameState  // пишем ПОСЛЕ header
```

### Что внедрили — ref-counted tickFrame

```go
// broadcast.go
type tickFrame struct {
    data  []byte // [10 WS header prefix][payload bytes]
    frame []byte // sub-slice: actual bytes to write
    refs  int32  // atomic refcount
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
```

Инвариант: `refs` устанавливается в `n` (количество соединений) **атомарно**
до того как первый `push()` может вызвать `release()`. Гарантируется тем что
`StoreInt32` делается до начала цикла, а `push()` только ставит job в очередь —
`release()` вызовется в drain goroutine которая запустится позже.

---

## Проблема 6: epoll — горутины 2400 → 70

### Что нашли в gobwas/ws

gobwas/ws не требует goroutine-per-connection для read пути:
`ws.ReadHeader()` + `io.ReadFull()` работают с `net.Conn` напрямую и могут
выполняться в pool worker'е после epoll event.

### Что внедрили

```go
// epoll_linux.go — EPOLLONESHOT pattern
type epollPoller struct {
    efd  int
    fds  map[int]*Connection
    jobs chan *Connection    // ready-to-read
}

// register: добавляем fd в epoll с EPOLLONESHOT
unix.EpollCtl(ep.efd, unix.EPOLL_CTL_ADD, fd, &unix.EpollEvent{
    Events: unix.EPOLLIN | unix.EPOLLRDHUP | unix.EPOLLONESHOT,
    Fd:     int32(fd),
})

// waitLoop (1 goroutine):
func (ep *epollPoller) waitLoop() {
    events := make([]unix.EpollEvent, 256)
    for {
        n, _ := unix.EpollWait(ep.efd, events, 100)
        for i := 0; i < n; i++ {
            // enqueue ready connection to jobs channel
            ep.jobs <- conn
        }
    }
}

// N worker goroutines (N = 2×GOMAXPROCS):
func (ep *epollPoller) worker() {
    for c := range ep.jobs {
        ep.processRead(c)
        ep.rearm(c)  // re-arm EPOLLONESHOT
    }
}
```

**Результат**: 2400 горутин → 1 (epollWait) + 2×GOMAXPROCS (workers) ≈ 25 горутин.
GC STW: `25 × 3µs ≈ 0.075ms` vs `2400 × 3µs ≈ 7ms`.

---

## Итоговая таблица: было vs стало

| Метрика | До | После | Причина |
|---|---|---|---|
| Горутины @ 2500 клиентов | ~2463 | ~70 | epoll + writeQueue (lazy) |
| GC STW | ~7ms | ~0.2ms | 70 stacks vs 2400 |
| tick p99 | 25ms | ~3ms | parallel workers + per-conn queue |
| shard_write p99 | 21ms | < 1ms | нет шардов, concurrent drain goroutines |
| shard_send p99 | 15ms | ~0.1ms | push() = lock+append, нет channel wait |
| ws_write_errors/s | 476 | ~0 | deadline 5ms + GOGC=400 |
| GC p99 | 111ms | ~5ms | GOGC=400 + GOMEMLIMIT=2GiB |

---

## Ключевые ссылки

| Проект | Путь | Что смотрели |
|---|---|---|
| centrifuge | `hub.go`, `client.go` | Per-client writeCh, GOGC=400 env config |
| gws | `worker.go`, `writer.go` | `workerQueue` lazy goroutine pattern |
| nbio | `taskpool/taskpool.go` | Persistent worker pool, buffered channel dispatch |
| nakama | `runtime/runtime.go` | Pool pattern identical to nbio |
| gobwas/ws | `frame.go`, `conn.go` | `CompileFrame`, raw `net.Conn` write |

---

## Порядок реализации (хронология)

1. **epoll + gobwas/ws** — горутины 2400 → 70, GC STW 7ms → 0.2ms
2. **Per-connection write deadline** — устранены массовые дисконнекты
3. **AppendGameState fix** — устранена телепортация (off-by-10 bug)
4. **tickFrame ref-counted pool** — устранён data race ring buffer
5. **Parallel tick workers** — tick range 13ms parallelized (WG Add-before-send!)
6. **GOGC=400 + GOMEMLIMIT=2GiB** — GC p99 111ms → ~5ms
7. **broadcastWriteTimeout 1ms → 5ms** — ws_write_errors 476/s → ~0
8. **connWriteQueue (gws pattern)** — shard_write p99 21ms → < 1ms, shards removed
