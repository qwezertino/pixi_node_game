# pixi_node_game — актуальная база знаний проекта

> Срез по коду на 2026-09-02: рабочее дерево поверх commit `0ec0572` (`main`). Этот файл — быстрый контекст для RAG/AI и разработчика. При конфликте документации с кодом источником истины является код.

## 1. Назначение и фактический статус

`pixi_node_game` — прототип браузерной 2D top-down multiplayer-игры:

- клиент: TypeScript + Pixi.js;
- сервер: Go, авторитетная симуляция движения;
- транспорт: бинарные сообщения поверх WebSocket;
- наблюдаемость: Prometheus, Grafana, Loki, Promtail;
- целевой сценарий: 1500–2000 одновременно видимых игроков в одном общем мире/viewport.

Серверный simulation/read path рассчитан на тысячи соединений. Доставка world state остаётся **all-to-all**: каждому выбранному получателю отправляется одна и та же глобальная дельта. Стоимость сети растёт как `O(records × recipients)` и остаётся основным ограничением раньше CPU симуляции.

Состав дельты сокращён **velocity replication** (default on, `VELOCITY_REPLICATION`). Позиция — детерминированная функция скорости на обеих сторонах, поэтому сервер отправляет запись только когда интегрирование скорости клиентом дало бы неверный результат: сменился неугадываемый вход (VX/VY/state/facing) либо фактическая позиция разошлась с предсказанием (кламп у границы мира). Остальных игроков клиент доинтегрирует сам. Измеренное сокращение — примерно `3.3–3.8×` по записям при 1.5 сменах направления в секунду на игрока; квадратичный характер это не убирает, но сдвигает предел.

`MAX_CONNECTIONS=12000` — только admission limit. Это не доказательство поддержки 12 000 активных игроков с приемлемой частотой обновления, трафиком и браузерным FPS.

## 2. Текущие значения по умолчанию

Источник игровых значений: `src/shared/gameConfig.json`.

| Параметр | Значение |
|---|---:|
| Server tick rate | 20 Hz |
| Base replication interval | 100 ms (10 Hz) |
| Full sync interval | 30 s |
| Player speed | 4 world units/tick |
| World | 6000 × 3000 |
| Spawn X | 1500–3000 |
| Spawn Y | 500–1500 |
| Attack duration | 1000 ms |
| Sprite base scale | 2 |
| Max connections | 12 000 |
| Per-connection message rate | 120 msg/s, burst 20 |
| Per-IP connect rate | 10 conn/s, burst 20 |
| Velocity replication | on |
| Keyframe divisor | 50 (1/50 игроков за broadcast) |
| Protocol version | 3 |

Важно: при `20 Hz` скорость 4 units/tick означает 80 units/s по каждой активной оси. Диагональ не нормализуется, поэтому её модуль выше в `sqrt(2)` раза.

## 3. Стек и версии

| Слой | Технология |
|---|---|
| Renderer | Pixi.js `^8.6.2` |
| Client | TypeScript `~5.7.2` |
| Bundler | Vite `^6.0.2` |
| Package manager/build image | Bun 1.2 |
| Server module | Go module `pixi_game_server`, directive `go 1.25.0` |
| Docker Go builder | `golang:1.26.2-alpine` |
| WebSocket | `github.com/gobwas/ws v1.4.0` |
| Metrics | `prometheus/client_golang v1.23.2` |
| Rate limiter | `golang.org/x/time v0.5.0` |
| Linux polling | `golang.org/x/sys v0.43.0`, epoll |

## 4. Карта репозитория

```text
/
├── README.md
├── Makefile
├── package.json, bun.lock
├── index.html, vite.config.ts, tsconfig.json
├── public/                         # CSS, favicon, sprite sheets
├── docs/
│   ├── pixi_node_game_rag.md       # этот актуальный срез
│   ├── research_performance.md     # история старых исследований/рефакторингов
│   ├── fanout_latency_playbook.md  # заметки по A/B fanout
│   └── task.txt                    # исходное ТЗ/ранние идеи, не current architecture
├── src/shared/
│   ├── gameConfig.json             # source of truth для игровых default values
│   └── gameConfig.ts               # типы и TS exports
├── src/client/
│   ├── main.ts
│   ├── controllers/                # local movement/prediction, animations
│   ├── game/playerManager.ts       # remote entities + interpolation
│   ├── network/                    # manager, Web Worker, binary codec
│   └── utils/                      # coordinates, input, FPS UI, sprites
├── src/server/
│   ├── go.mod, go.sum
│   ├── cmd/server/main.go
│   ├── internal/
│   │   ├── config/                 # embedded JSON + env overrides
│   │   ├── game/world.go           # authoritative world/tick/delta (+ *_test.go, white-box)
│   │   ├── metrics/metrics.go
│   │   ├── protocol/binary.go      # (+ *_test.go, white-box)
│   │   ├── server/                 # HTTP/WS, epoll, write/fanout (+ *_test.go, white-box)
│   │   ├── systems/visibility.go   # spatial grid bookkeeping only
│   │   └── types/types.go
│   └── tests/                      # external `pkg_test` packages, exported-API-only
│       └── types/                  # input queue: only Enqueue/DequeueMovementInput
├── docker/                         # image, compose, monitoring
└── utils/testing/
    ├── artillery/                  # processor + tracked example config
    └── protocol/                   # end-to-end protocol probes
        ├── run.sh                  # build + start server + run all probes
        ├── ab-velocity.sh          # A/B velocity replication
        ├── lib/harness.mjs         # shared client; lib/proto.mjs генерируется
        └── probes/                 # determinism, pacing, dead-reckoning, ...
```

`utils/testing/artillery/artillery-config.yml` — локальный ignored-файл. В git хранится только `.example`; перед запуском теста локальный файл должен существовать.

## 5. Конфигурация и build

### Приоритет конфигурации

1. Environment variables.
2. Embedded `gameConfig.json`.
3. Hardcoded server-infrastructure defaults в `config.go`.

`src/shared/gameConfig.json` компилируется в клиент. Для сервера Makefile/Docker копирует его в `src/server/internal/config/gameConfig.json`, потому что `embedded.go` использует `//go:embed gameConfig.json`.

Локальный `make build-server` оставляет эту копию в рабочем дереве; `make clean` её удаляет. Ручной `go build` без предварительного копирования завершится ошибкой embed.

### Реально читаемые env variables

| Группа | Variables |
|---|---|
| Bind/static | `PORT`, `HOST`, `STATIC_DIR` |
| Workers/admission | `WORKERS`, `MAX_CONNECTIONS` |
| Input limits | `RATE_LIMIT_MSG_SEC`, `RATE_LIMIT_BURST`, `IP_CONN_RATE`, `IP_CONN_BURST` |
| Game override | `TICK_RATE`, `SYNC_INTERVAL_SEC`, `BATCH_INTERVAL_MS`, `PLAYER_SPEED`, `ATTACK_DURATION_MS` |
| World override | `WORLD_WIDTH`, `WORLD_HEIGHT`, `SPAWN_MIN_X`, `SPAWN_MAX_X`, `SPAWN_MIN_Y`, `SPAWN_MAX_Y` |
| Replication model | `VELOCITY_REPLICATION` (bool, default `true`), `KEYFRAME_DIVISOR` (default 50, `0` = off) |
| Fanout | `FANOUT_MAX_BROADCAST_BYTES_PER_TICK`, `FANOUT_QUEUE_SHED_DEPTH`, `FANOUT_DROP_STREAK`, `WRITE_BATCH_SIZE` |
| Selection/fairness | `FANOUT_FAIR_DEBT_MAX`, `FANOUT_FAIR_DEBT_INC`, `FANOUT_FAIR_DEBT_DEC`, `FANOUT_FAIR_DEBT_WEIGHT_NS`, `FANOUT_ROUND_ROBIN_WEIGHT_NS` |
| Critical/freshness | `FANOUT_CRITICAL_WINDOW_MS`, `FANOUT_CRITICAL_BOOST_NS`, `FANOUT_MIN_RECIPIENTS_PER_TICK`, `FANOUT_MAX_RECIPIENTS_PER_TICK`, `FANOUT_TARGET_MS` |
| Staleness | `WORLD_STATE_ACTIVE_STALENESS_MS`, `WORLD_STATE_IDLE_STALENESS_MS`, `WORLD_STATE_ACTIVE_WINDOW_MS` |
| Runtime/profiling | `GOGC`, `GOMAXPROCS`, `GOMEMLIMIT`, `PPROF_BLOCK_RATE` |

`.example-env` содержит устаревшие неиспользуемые параметры: `EVENT_CHANNEL_SIZE`, `SEND_CHANNEL_SIZE`, `BROADCAST_WORKERS`, `READ_BUFFER_SIZE`, `WRITE_BUFFER_SIZE`. Он также не перечисляет новые fanout/IP parameters. Не считать его полной схемой конфигурации.

`GOGC` фактически задаётся через `debug.SetGCPercent`: default 400, если env невалиден/отсутствует. `GOMEMLIMIT` читает сам Go runtime; hardcoded `2GiB` нет. В tracked `.example-env` сейчас указано `512MiB`.

### Команды

```bash
make install
make build
make build-client
make build-server
make build-server-linux
make build-release
make dev-client
make dev-server
make dev
make run
make load-test
make protocol-test              # end-to-end protocol probes; PROBE=<name> для одного
make protocol-ab                # A/B velocity replication; CLIENTS=/TURNS=/SECONDS=
make docker-upbuild
make docker-test
make docker-down
```

Vite dev server: `:8109`. Go server/default production HTTP: `:8108`.

Vite dev server proxy-ит `/ws` с `:8109` на Go server `127.0.0.1:8108`; production использует same-origin WebSocket через Go static server.

## 6. Server runtime architecture

### Startup и endpoints

`cmd/server/main.go` настраивает JSON `slog`, GOMAXPROCS/GC, загружает config и вызывает `server.New(cfg).Start()`.

| Endpoint | Назначение |
|---|---|
| `/ws` | WebSocket upgrade |
| `/health` | health JSON + uptime/player count |
| `/metrics` | Prometheus |
| `/metrics/json` | legacy JSON, вызывает `runtime.ReadMemStats` |
| `/debug/pprof/*` | pprof |
| `/` | static client files |

Block/mutex profiler включается только при `PPROF_BLOCK_RATE=1`; pprof routes зарегистрированы всегда.

### Connection lifecycle

1. Проверка текущего connection count и per-IP token bucket.
2. `ws.UpgradeHTTP`, создание игрока с monotonic uint32 ID (первый ID 1001).
3. Запуск одной persistent write goroutine для соединения.
4. Клиенту последовательно ставятся в очередь `WELCOME` и full initial state до добавления connection в глобальную map.
5. Connection добавляется; существующие клиенты обнаруживают новичка в следующей state delta, отдельного join fanout нет.
6. Linux: fd регистрируется в epoll; non-Linux: запускается read goroutine.
7. Cleanup идемпотентен через `sync.Once`: remove epoll/map, `PLAYER_LEFT`, cancel/drain/close, remove world player.

Дисконнект по вине клиента ограничен реально невосстановимыми случаями. Duplicate/out-of-order movement sequence — штатное следствие retransmit и reordering middlebox, поэтому шаг игнорируется, а сессия рвётся только после `maxStaleInputStreak = 120` подряд. Переполнение input ring (`PlayerInputQueueCapacity = 256`, около 12.8 s отставания) означает, что реконсиляция уже невозможна — это разрыв. Rate limit дропает сообщение и рвёт соединение после `maxRateLimitStreak = 200` подряд, чтобы одиночный burst от прокси не стоил сессии. Отказ по WS-заголовку логируется с троттлингом не чаще раза в секунду на весь сервер.

`SIGINT`/`SIGTERM` запускают graceful shutdown: `http.Server.Shutdown` с таймаутом 10 s, затем отмена per-connection контекстов и `GameWorld.Stop()`.

Leave event остаётся all-to-all. Массовый connect всё ещё создаёт суммарно `O(N²)` initial-snapshot bytes, но больше не создаёт вторую `O(N²)` волну `PLAYER_JOINED` jobs.

`PLAYER_LEFT` при velocity replication стал обязательным, а не оптимизацией: отсутствие игрока в дельте больше не отличимо от «двигается предсказуемо», поэтому удаление сущности возможно только по явному сообщению или по full snapshot.

### Read path

Linux implementation:

- 1 `EpollWait` goroutine;
- `2 × GOMAXPROCS` persistent read workers;
- `EPOLLONESHOT` и rearm после одного frame;
- pooled 64-byte input buffers;
- synchronous decode/process;
- 100 ms read deadline на обработку frame.

Non-Linux implementation использует одну read goroutine на connection и выделяет payload buffer для каждого frame.

Read path принимает только одиночные (`FIN=1`) masked binary/control frames без RSV bits и с payload не более 125 bytes. Fragmentation, continuation, text, unmasked и oversized frames приводят к закрытию connection до payload allocation. Origin при upgrade пока не ограничивается.

Превышение per-connection message rate закрывает connection, а не молча выбрасывает MOVE: продолжать sequence stream после потерянного шага означало бы необратимо нарушить deterministic reconciliation.

### World tick

`GameWorld` хранит игроков в `map[uint32]*Player` под `RWMutex`; поля player state читаются/пишутся atomically.

Каждый tick:

1. Под коротким `RLock` копируются player pointers.
2. До `GOMAXPROCS` persistent workers параллельно потребляют максимум один queued movement input на игрока и применяют attack timeout.
3. Game loop последовательно строит `PlayerState`, сравнивает с последним успешно принятым replication baseline и получает накопленный global changed set.
4. Нет изменений — delta broadcast пропускается.
5. Один adaptive replication gate ограничивает рассылку: base 100 ms, под write/fanout pressure интервал может вырасти до 120 ms.
6. Раз в 30 s отправляется full state.

Movement server-authoritative и детерминирован по шагам: каждый клиентский MOVE — ровно один predicted step; server хранит pointer-free ring на 256 inputs для каждого player и потребляет максимум один input за simulation tick. MOVE и STOP проходят в TCP/WebSocket порядке. Если новых inputs нет, сервер самопроизвольно позицию не меняет. Duplicate/out-of-order sequence или переполнение ring закрывает connection вместо тихой потери шага и необратимого рассинхрона.

### Spatial grid

`VisibilityManager` поддерживает сетку 100×100 units:

- default world даёт 60×30 = 1800 cells;
- отдельный `RWMutex` на cell;
- `sync.Map playerID → cell`;
- Add/Move/Remove вызываются из world lifecycle.

Но query видимости отсутствует, viewport координаты connection не хранятся, и grid **не участвует в broadcast**. Message 13 принимается и игнорируется. Это bookkeeping для будущего AOI, а не действующий interest management.

## 7. Broadcast/write path

World state кодируется один раз в общий reference-counted `tickFrame`:

- pool buffer initial capacity: 64 KiB;
- 10 bytes reserve под максимальный WS header;
- payload appended без промежуточной копии;
- refs равен числу выбранных recipients;
- последний writer возвращает frame в `sync.Pool`.

Для каждого connection:

- `writeCh` capacity = 32;
- одна persistent write goroutine;
- `pendingBroadcast` обеспечивает latest-state semantics: одновременно максимум один queued/in-flight world-state frame;
- default queue shedding threshold = 6;
- `WRITE_BATCH_SIZE=8`, hard clamp 64;
- broadcast write deadline = 100 ms;
- direct write deadline = 30 ms;
- disconnect после 150 последовательных write failures или configured fanout-drop streak.

Recipient selection ранжирует staleness, recent activity, critical window, fairness debt и round-robin bias. Однако defaults `FANOUT_MAX_RECIPIENTS_PER_TICK=0` и byte budget `0` означают unlimited/all connections.

При включённом recipient cap/byte budget часть клиентов получает тот же global state реже. Это degradation/freshness control, не уменьшение состава state по AOI.

Старый неиспользуемый `FANOUT_WORKERS` pool удалён. Enqueue выполняется последовательным дешёвым проходом, а реальные writes параллельны по persistent per-connection writers.

Adaptive pacing учитывает и fanout duration, и максимальный фактический возраст world-state при завершении socket write. Отдельные histogram metrics показывают queue delay, age at write start и age at write end. Movement ACK кодируется в переиспользуемый writer buffer без `ws.CompileFrame`/heap buffer и коалесцируется до последнего input sequence на replication tick уже после применения world update.

### Pacing квантуется целыми тиками

Репликация вычисляется на границах тиков, поэтому интервал, не кратный тику, честно выдержать нельзя — он бьётся о тик. При 50 ms тике и запросе 100 ms примерно половина тиков попадала чуть-чуть под порог и откладывалась на целый тик: измеренная каденция была бимодальной `100/150 ms` (mean 125.6, stdev 24.8), фактические `8.0 Hz` вместо 10, а клиентская adaptive interpolation delay упиралась в потолок 300 ms.

`replicationIntervalNs` округляет интервал до целого числа тиков, `replicationDue` добавляет допуск в полтика против дрожания тикера (тик приходит только по сетке тиков, поэтому допуск не может впустить лишнюю отправку). После этого: mean 99.8 ms, stdev 0.6 ms, шаг позиции ровно 8 px, расчётная delay около 220 ms.

Следствие: шаги adaptive controller в 3–10 ms лежат ниже гранулярности тика. Его потолок поэтому выводится из тика (`adaptiveBatchCeiling` = base + 2 тика), иначе квантование вернуло бы значение к base и backoff стал бы no-op.

### Два независимых счётчика пейсинга

`lastAckPassNs` пасует проход по movement ACK, `lastBroadcastNs` — кадр состояния и продвигается только при реальной отправке. Общий счётчик позволил бы тику, отдавшему только ACK, занять слот состояния и задержать следующую дельту на полный интервал.

ACK эмитятся **до** проверки на пустую дельту. Игрок, прижатый к границе мира, продолжает потреблять инпуты, не меняя ни одного реплицируемого поля; если бы ACK ехал на дельте, такой клиент никогда не подрезал бы pending-input ring и переполнил бы его.

### Heartbeat-кадр

При velocity replication пустая дельта всё равно уходит — голым 13-байтным заголовком (`shouldEmitFrame`). Клиент доинтегрирует пропущенных игроков **только при приходе кадра**, используя `worldTick` из заголовка как счётчик шагов; подавление пустых кадров заморозило бы всех удалённых игроков до чьей-нибудь смены направления. Стоимость — около 2 Mbit/s на 2000 клиентов при 10 Hz против сотен Mbit/s, которые стоили бы опущенные записи.

Keyframe-ротация досылает `1/KEYFRAME_DIVISOR` игроков за broadcast с абсолютной позицией, чтобы клиент, пропустивший запись (shed, потеря), сошёлся не дожидаясь смены направления у того игрока.

## 8. Binary protocol

Все multi-byte числа little-endian. WebSocket framing добавляется отдельно.

### Client → server

| ID | Имя | Payload | Фактическое поведение |
|---:|---|---:|---|
| 1 | JOIN | 1 | constant есть, decoder не принимает; клиент не отправляет |
| 2 | LEAVE | 1 | constant есть, decoder не принимает; disconnect идёт через WS close |
| 3 | MOVE | 6 | `type + packed(dx,dy) + inputSequence:u32` |
| 4 | DIRECTION | 2 | client посылает signed `-1/+1`; server считает `1` right, всё прочее left |
| 5 | ATTACK | 9 | client добавляет `x:f32,y:f32`; server валидирует размер, но координаты игнорирует |
| 6 | ATTACK_END | 1 | принимается, но server игнорирует: timeout authoritative |
| 13 | VIEWPORT | 5 | client enum/encoder отсутствует; server валидирует размер, но размеры не читает и не сохраняет |

Packed movement: bits 0–1 = `dx+1`, bits 2–3 = `dy+1`; нормальные значения `dx/dy ∈ {-1,0,1}`.

### Server → client

| ID | Имя | Формат |
|---:|---|---|
| 7 | GAME_STATE | `type:1 + stateSequence:u32 + worldTick:u32 + count:u32 + N×player` |
| 8 | MOVEMENT_ACK | `type:1 + playerID:u32 + x:u16 + y:u16 + inputSequence:u32` = 13 bytes |
| 11 | PLAYER_JOINED | `type:1 + player` = 12 bytes |
| 12 | PLAYER_LEFT | `type:1 + playerID:u32` = 5 bytes |
| 14 | DELTA_GAME_STATE | тот же header/player layout, только changed players |
| 15 | WELCOME | `type:1 + protocolVersion:u8 + tickRate:u16 + playerID:u32` = 8 bytes |

`ProtocolVersion = 3`. Клиент сверяет её с `PROTOCOL_VERSION` из `messages.ts` и при расхождении отказывается декодировать world state. Проверка обязательна, а не гигиена: world-state кадр не содержит per-record framing, поэтому разошедшийся декодер не падает — он молча выдаёт неверные player ID.

История версий: v2 ввела varint-делта ID, v3 добавила `worldTick`.

Client→server ID 16 — `SYNC_REQUEST` размером 1 byte. Клиент отправляет его при разрыве world-state sequence; сервер отвечает адресным full snapshot. Поэтому queue shedding не оставляет клиента с бессрочно устаревшей delta-базой.

World-state header = 13 bytes: `type:1 + stateSequence:u32 + worldTick:u32 + count:u32`.

`worldTick` — номер симуляционного тика, который описывают записи. Клиент берёт разницу с предыдущим полученным кадром как число шагов для dead reckoning. Настенное время для этого недостаточно точно и не переживает кадр, отброшенный шеддингом; `stateSequence` не подходит, потому что растёт по кадрам, а не по тикам.

Player wire record — переменной длины, типично 8 bytes:

```text
varint(ID - previousID) + X:u16 + Y:u16 + VX:i8 + VY:i8 + flags:u8
flags = facingRight:bit7 | state:bits0..6
```

Записи отсортированы по ID, поэтому делта всегда положительна и при плотных ID сервера укладывается в один байт (первая запись кадра несёт абсолютный ID, обычно 2 байта). Varint — LEB128. Худший случай 12 bytes на запись, замеренная экономия против фиксированного `u32` ID — около 24%.

`PlayerState.ClientTick` существует в Go type, но в world-state record не сериализуется. Reconciliation sequence приходит только отдельным ACK. По этой же причине `ClientTick` намеренно не участвует в предикате дельты: он добавлял бы записи, кодирующиеся байт-в-байт идентично baseline.

State sequence — uint32, увеличивается на каждый реально закодированный global broadcast. Клиент использует wrap-aware filtering stale/duplicate state.

## 9. Bandwidth model для общего viewport

Обозначения:

- `N` — connected recipients;
- `R` — records, реально попавших в кадр;
- `F` — фактическая частота рассылки;
- payload = `13 + ~8R` bytes;
- server application egress ≈ `(WS frame bytes) × recipients × F`.

При default replication `F = 10 Hz` ровно (после квантования по тикам). Simulation работает на 20 Hz. TCP/IP/TLS/Ethernet overhead и retransmits ниже не учтены.

Ключевой вопрос — чему равно `R`. Без velocity replication `R = M`, числу изменившихся игроков, то есть при общем движении `R ≈ N`. С velocity replication `R` определяется частотой смены направления, а не числом движущихся:

| Смен направления/с на игрока | Доля предсказуемых записей | Сокращение записей |
|---:|---:|---:|
| 0.5 | 98% | ~50× |
| 1.5 | 84% | ~3.3–3.8× |
| 3 | 71% | ~3× |
| 6 | 34% | ~1.5× |

Замеры сняты ботами (`utils/testing/protocol/probes/bandwidth.mjs`), которые меняют направление по расписанию. Живые игроки поворачивают рывками — цифры выше описывают форму зависимости, а не прогноз. Метрики `game_delta_*` собирают то же самое на реальном трафике, и решения стоит принимать по ним.

| Сценарий, все двигаются | На клиента | Server egress |
|---|---:|---:|
| 2000 players, all-to-all по позициям | ~160 KB/s | ~320 MB/s = 2.56 Gbit/s |
| 2000 players, velocity replication при 1.5 смен/с | ~42 KB/s | ~83 MB/s ≈ 0.67 Gbit/s |

Full sync добавляет `(13 + ~8N) × N` bytes раз в 30 s.

Velocity replication не меняет квадратичный характер — она уменьшает коэффициент. Для устойчивых 1500–2000 в одном viewport поверх неё нужен LOD по дистанции или AOI: разная частота обновления для ближних и дальних игроков. Отдельно нужен явный продуктовый budget: допустимый downstream клиента, NIC egress и максимальная staleness.

## 10. Client architecture и масштабирование

`NetworkManager` по умолчанию создаёт Web Worker. Worker обслуживает WebSocket и передаёт входящий `ArrayBuffer` на main thread через transfer list; обе socket реализации задают `binaryType = "arraybuffer"`. Binary decode всё ещё выполняется на main thread — Worker пока не является decode worker.

`NetworkManager`:

- хранит все players в JS object;
- full state заменяет object;
- delta мутирует только changed records без копии 2000-entry object;
- callback получает только incoming changed set и флаг full/delta;
- local identity получает из versioned `WELCOME` (старый ID heuristic оставлен лишь как fallback совместимости);
- сверяет `protocolVersion` из `WELCOME` и отказывается декодировать world state при расхождении;
- отбрасывает stale state sequence и запрашивает full state при gap;
- считает `elapsedTicks` как разницу `worldTick` с **предыдущим полученным** кадром, а не с предыдущим отправленным сервером: кадр, отброшенный шеддингом, обязан оставить dead reckoning на верном шаге. Разрыв больше `MAX_DEAD_RECKON_TICKS = 20` трактуется как stall/wrap — экстраполяция подавляется;
- переподключается с экспоненциальным backoff и джиттером. Сервер выдаёт новый player ID на каждый accept, поэтому reconnect — новая сессия: сбрасываются identity, players, sequence-состояние; колбэк `onSessionStart` переустанавливает предсказанную позицию и чистит pending inputs, иначе первый ACK новой сессии прилетел бы как огромная коррекция.

`PlayerManager` создаёт отдельные `AnimatedSprite`, `AnimationController` и snapshot array для каждого remote player. Каждый render frame обходит всех remote players для интерполяции.

Dead reckoning встроен **не заменой интерполятора, а подачей в него**. `RemotePlayer.deadReckon(elapsedTicks)` воспроизводит запись, которую сервер решил не отправлять, и кладёт её в тот же snapshot buffer через `pushSnapshot`. Интерполятор не изменён и не отличает синтезированный снапшот от сетевого; синхронизация серверных часов на клиенте по-прежнему отсутствует и не понадобилась. Интегрирование намеренно без клампа — оно зеркалит серверное предсказание, а любой применённый сервером кламп приходит как настоящая запись следующим кадром.

State callback теперь обрабатывает только changed entities; отсутствие ID означает удаление только в full snapshot. Sprite-sheet promise и texture arrays кэшируются на путь, параллельное создание одной remote entity дедуплицируется. Интерполяция remote players выполняется каждый render frame, а не только внутри fixed simulation loop. Player container отключён от event traversal; удаляемый `AnimatedSprite` останавливается и уничтожается.

Remote interpolation:

- snapshots timestamped локальным `performance.now()` при получении;
- buffer max 32;
- adaptive delay 100–300 ms;
- EWMA по arrival interval/jitter;
- interpolation без extrapolation, после newest snapshot позиция удерживается;
- server clock synchronization отсутствует.

Local player использует fixed-step prediction и ACK reconciliation. Server ACK содержит фактическую позицию после world update и последний применённый input sequence; несколько inputs между replication ticks коалесцируются. Клиент заменяет logical position на `ACK position + replay(unacknowledged inputs)`, поэтому network delay сдвигает момент обработки MOVE/STOP, но не итоговую координату. ACK cadence связан с replication gate и обычно составляет до 10 Hz, а не simulation 20 Hz. Catch-up после background-tab ограничен четырьмя шагами, pending buffer — 256 inputs.

Фактический browser budget на 1500–2000 `AnimatedSprite` всё ещё не подтверждён benchmark. Основные оставшиеся риски клиента: отдельный auto-updating `AnimatedSprite`/animation ticker на сущность, object churn при decode, main-thread decode и отсутствие render LOD/ParticleContainer strategy.

## 11. Load testing и observability

Tracked Artillery template по умолчанию создаёт примерно 1200 arrivals за 120 s (10/s + 10/s), но каждая VU выполняет конечный loop `count: 25`, поэтому число одновременно активных connections не равно автоматически 1200. Локальный ignored config в текущем workspace переключён на профиль, подписанный как 2400 arrivals.

Все локальные VU приходят с одного IP. Профиль 20 connects/s превышает default `IP_CONN_RATE=10` после burst 20, поэтому без отдельного override часть upgrades получит HTTP 429 и ожидаемые 2400 connections не будут достигнуты.

Artillery client:

- MOVE примерно раз в 0.5 s;
- direction probabilistic;
- attack probabilistic;
- не декодирует/валидирует server state;
- не измеряет browser render/decode cost;
- его local spawn/bounds comments/values устарели относительно world 6000×3000.

Таким тестом можно нагружать connection/fanout path, но нельзя доказать playable experience для 2000 реальных клиентов.

### Protocol probes

`utils/testing/protocol/` — end-to-end проверки против живого сервера, дополняющие Go unit tests: те закрепляют чистые функции, эти — поведение, которое видно только когда обе стороны разговаривают.

```bash
make protocol-test              # все probes
make protocol-test PROBE=pacing # один
make protocol-ab                # A/B velocity replication
```

`run.sh` сам собирает сервер, поднимает его на scratch-директории, прогоняет probes и падает, если упал probe или сервер записал ERROR.

Probes декодируют кадры **настоящим клиентским декодером**, который `run.sh` бандлит из `src/client` в `lib/proto.mjs` (git-ignored). Ручная копия разошлась бы, а разошедшийся декодер здесь не падает — он молча выдаёт неверные player ID.

| Probe | Что фиксирует |
|---|---|
| `determinism` | Один инпут — ровно один шаг симуляции; дистанция равна `steps × playerSpeedPerTick` для каждого взгляда каждого клиента на каждого. |
| `pacing` | Ровная каденция репликации; джиттер оплачивается дважды — меньше обновлений и больше interpolation delay. |
| `dead-reckoning` | Наблюдатель, восстанавливающий позицию одной скоростью, сходится точно на авторитетный `MOVEMENT_ACK` движущегося. |
| `ack-flow` | ACK продолжают идти при пустой дельте. |
| `resilience` | Дубликаты sequence и burst стоят сообщений, а не сессии. |
| `bandwidth` | Не pass/fail: отдаёт записи и байты на проводе плюс состав дельты. Используется `ab-velocity.sh`. |

Probes читают `src/shared/gameConfig.json`, поэтому смена tick rate или скорости не обесценивает ассерты молча. Экрана они не видят: доказывают корректность и ровность потока, но не то, как выглядит движение.

Основные Prometheus metrics:

- connections: `game_players_connected`, `game_connections_total`, `game_disconnections_total`;
- input integrity: `game_movement_inputs_rejected_total`;
- tick: `game_tick_duration_seconds`, `game_ticks_total`, `game_tick_phase_seconds`, `game_tick_world_step_seconds`;
- fanout: `game_tick_fanout_send_seconds`, `...select_seconds`, `...enqueue_seconds`;
- payload/targets: `game_broadcast_payload_bytes`, `game_broadcast_targets`, `game_broadcast_recipients`;
- degradation: `game_broadcasts_dropped_total`, `game_broadcasts_shed_total`, `game_broadcast_deferred_total`, budget hit/trim metrics;
- write path: `game_ws_write_errors_total`, `game_ws_write_batch_seconds`, `game_ws_write_batch_jobs`, `game_ws_write_queue_depth`;
- delta composition: `game_delta_players_count`, `game_delta_ratio`, `game_delta_vector_changes`, `game_delta_position_only`, `game_delta_clamped_players`, `game_delta_keyframes`, `game_delta_predictable_ratio`;
- wire: `game_broadcast_records` (записи, реально попавшие в кадр);
- adaptive: `game_adaptive_batch_interval_ms`, `game_fanout_recipient_limit`.
- write pressure: `game_world_state_queue_delay_seconds`, `game_world_state_age_at_write_start_seconds`, `game_world_state_age_at_write_end_seconds`.

`game_bytes_sent_total` считает успешно переданные WS frame bytes из `Write/WriteTo`, но не IP/TCP overhead. Для реального канала дополнительно измерять host/container NIC counters.

`docs/research_performance.md` и комментарии в коде содержат исторические цифры (включая 30 Hz, 5 ms timeout, lazy write queues, ~70 goroutines). Текущая реализация уже другая: 20 Hz, 100 ms broadcast timeout, persistent writer на connection. Эти цифры полезны как история, но не как свежий benchmark commit `0ec0572`.

## 12. Известные риски и приоритеты

### P0 — до заявления «1500–2000 playable»

1. **Проверить плавность в браузере.** Ни одна автоматическая проверка в репозитории не смотрит на экран. Velocity replication даёт коррекцию около 8–23 px при смене направления, которую interpolation delay (~220 ms) должна скрывать. Нужны две вкладки и живой взгляд. Если рывки заметны — сначала поднять `KEYFRAME_DIVISOR`, при необходимости `VELOCITY_REPLICATION=false` для сравнения.
2. **Собрать `game_delta_*` на живых сессиях.** Выигрыш velocity replication определяется частотой смены направления, а она у ботов и людей разная. Все текущие цифры получены ботами.
3. Провести browser benchmark 1500/2000 entities: decode ms, main-thread frame time p95/p99, draw calls, GPU/CPU memory и downstream.
4. Провести отдельный-host end-to-end load test; локальный Artillery конкурирует с сервером за CPU и loopback.
5. Решить, нужен ли LOD по дистанции: velocity replication снижает egress примерно до 0.67 Gbit/s при 2000 игроках, что всё ещё половина гигабитного канала.
6. Перенести decode с main thread либо перейти на typed/SoA state representation, если browser profile подтвердит GC/frame-time проблему.

### P1 — correctness и устойчивость

1. Сделать config validation: positive tick rate, корректные spawn ranges/world bounds, значения в пределах uint16.
2. Защитить pprof/metrics и настроить Origin/TLS/reverse proxy для production.
3. Проверить поведение velocity replication при массовом shedding: клиент детектирует разрыв sequence и запрашивает full snapshot, то есть перегрузка может породить волну resync. Keyframe-ротация смягчает, но обратная связь не измерена.

### P2 — cleanup/maintainability

1. Синхронизировать local `.env`, README и historical performance docs с current code; в local `.env` есть параметры несуществующих AOI/WebRTC implementations.
2. Проверить переход remote rendering на более дешёвый общий animation clock/ParticleContainer, если visual requirements позволяют.
3. Рассмотреть sticky vector на сервере (продолжать последний вектор при пустой очереди инпутов, с жёстким лимитом шагов). Это сократило бы upstream с 20 msg/s на клиента, но требует, чтобы экстраполированные шаги потребляли sequence — иначе ACK и клиентский реплей дважды учтут одни и те же шаги и дадут рывок.

## 13. Проверенный baseline этого среза

```text
bun/Vite production client build: PASS
TypeScript noEmit: PASS
ESLint: PASS
Go build / vet: PASS
go test ./...: PASS (52 assertions)
go test -race ./...: PASS
make protocol-test: PASS (5 probes)
```

Замеры протокольных probes на этом срезе:

- `determinism`: 8 клиентов, 64/64 замеров дистанции ровно 240 px, 0 разрывов sequence, 0 ошибок декодера;
- `pacing`: mean 99.8 ms, stdev 0.6 ms, все интервалы в одной корзине;
- `dead-reckoning`: 300 инпутов с 8 сменами направления, восстановленная позиция расходится с авторитетной на 0 px, 88% обновлений синтезированы клиентом;
- `ack-flow`: 51/51 подтверждено, ACK продолжают идти при пустой дельте;
- `resilience`: дубликаты sequence и burst из 200 сообщений не разрывают сессию;
- `ab-velocity` (12 клиентов, 1.5 смен/с): 1001 → 305 записей, 9240 → 3770 байт.

Байтовое отношение отстаёт от отношения записей, потому что 13-байтный заголовок — заметная доля маленького кадра; на тысячах игроков они сходятся.

Ранний Artillery ramp достигал 2291 одновременно connected clients на 4 `GOMAXPROCS`, но это измерение сделано **до** velocity replication и quantised pacing и требует повтора. Artillery в любом случае не измеряет browser rendering.

Сборка в restricted environment требует writable `GOCACHE`, например `GOCACHE=/tmp/pixi-node-game-go-cache`.

## 14. Gotchas для следующего разработчика/AI

- Не писать «30 Hz»: current shared default — 20 Hz simulation, 10 Hz replication.
- Не писать «full sync каждую секунду»: current default — 30 s.
- Не писать `writeCh cap=4`: current constant — 32.
- Не писать `broadcastWriteTimeout=5ms`: current constant — 100 ms.
- Не писать «11 байт на игрока»: record переменной длины, типично 8 (varint ID delta).
- Не писать «header 9 байт»: current — 13 (добавлен `worldTick`).
- Protocol version — 3. Клиент её проверяет; расхождение декодера **не** даёт ошибку, а даёт неверные player ID.
- Не называть visibility grid действующим AOI: у него нет query API, он только ведёт учёт.
- Не переносить white-box Go-тесты (`classifyDelta`, `replicationIntervalNs`, `appendUvarint`, `validClientHeader` и т.д.) в отдельную папку: Go даёт доступ к неэкспортированным символам только файлам в той же директории, что и код. В отдельную папку (`src/server/tests/`) вынесены только тесты, использующие исключительно публичный API — см. `src/server/tests/README.md`.
- Не добавлять обратно `FANOUT_WORKERS`: старый неиспользуемый pool удалён.
- Не считать Web Worker местом binary decode: decode идёт на main thread.
- Не считать `MAX_CONNECTIONS` benchmark capacity.
- Не подавлять пустые кадры при velocity replication: heartbeat — это то, по чему клиент делает шаг dead reckoning.
- Не определять «упор в границу» как «позиция не изменилась»: при диагональном упоре в угол одна ось зажата, другая едет. Проверять предсказание по каждой оси.
- Не выражать pacing в миллисекундах помимо тика: интервал квантуется целыми тиками, а шаги adaptive controller в 3–10 ms лежат ниже гранулярности тика.
- ACK эмитятся до проверки на пустую дельту — не сворачивать их обратно на payload.
- Перед manual Go build обеспечить `internal/config/gameConfig.json` для embed.
- Vite `:8109` proxy-ит `/ws` на Go `127.0.0.1:8108`.
- `docker-compose.yml` находится в `docker/`; Makefile уже передаёт правильный `-f` и `--env-file`.
- Docker поднимает `nofile=65536` и `somaxconn=65535`; host/kernel/NIC limits всё равно нужно проверять отдельно.
