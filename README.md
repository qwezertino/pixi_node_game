# Persistent Medieval War

Технический прототип массовой браузерной 2D-войны из [GDD](docs/GDD%20%E2%80%94%20Persistent%20Medieval%20War.md). Клиент написан на TypeScript и PixiJS, авторитетный сервер — на Go. PostgreSQL хранит игровые настройки и ростер юнитов, Redis доставляет уведомления об их live-reload.

> Текущий этап: **network/combat prototype, MVP 1 ещё не завершён**. В проекте уже можно подключить несколько клиентов, выбрать юнита, двигаться, сталкиваться со стенами/постройками, ускоряться, блокировать и запускать анимации атак. Атаки пока не наносят урон: hit detection, damage, death и полноценный respawn ещё не реализованы.

## Состояние реализации относительно GDD

Аудит выполнен по коду и конфигурации проекта 12 сентября 2026 года. «Данные готовы» означает, что параметры и ассеты существуют, но соответствующая игровая механика ещё может не использовать их.

| Область GDD | Статус | Что есть сейчас | Что осталось |
|---|---|---|---|
| Multiplayer foundation | Реализовано | Go WebSocket server, бинарный protocol v14, server-authoritative simulation 20 Hz, client prediction/reconciliation, delta/full sync, ping/pong | Проверка игрового поведения на целевом масштабе и разделение мира на simulation regions |
| Мир и перемещение | Частично | Один квадратный мир 32000×32000 world units (3.2×3.2 км), целочисленные координаты, WASD, направление по мыши, sprint, границы мира; server-authoritative коллизии игрока со статическими препятствиями и разрушаемыми постройками (см. ниже) | Наполнение карты (terrain, procedural generation), территории, мосты; player-player столкновения выключены осознанно (ghost movement) |
| Юниты | Частично | 11 определений юнитов, выбор при подключении, разные HP/speed/stamina/timings, реальные sprite sheets | Ограничения доступности и стоимости, kingdom/Royal Guard permissions, смена роли через respec point |
| Combat controls | Частично | Attack state, server-side timing, combo queue, attack stamina cost, stationary block и block drain | Hitboxes, hit detection, damage/DR, projectiles, dodge, ability logic, interrupt/recovery rules и friendly-fire filtering |
| HP, death, respawn | Заготовка | HP хранится и показывается; dev-кнопка позволяет переподключиться с другим юнитом | Получение урона, смерть, corpse state, respawn points, spawn selection и потеря стоимости погибшего юнита |
| Kingdoms / King / Officers | Не реализовано | — | 2 Kingdoms для MVP 1, затем 3; membership, роли, permissions и назначения |
| Banners / groups / chat | Не реализовано | — | Player Banners, leader/officers, символика, отдельный чат и claim capacity |
| Territories / claim | Не реализовано | — | Territory graph, ownership, adjacency/supply checks, Claim Banner, 60-секундный capture flow |
| Economy | Данные готовы | Стоимость юнитов Wood/Stone/Iron хранится в БД | Local Stockpile, списание Unit Cost, бесплатный Citizen, Unit Return 100%, потеря ресурсов при смерти |
| Resources | Не реализовано | — | Nodes, manual gathering, deposit, passive production, Mine/Quarry/Sawmill, capital minimum income |
| Buildings / siege | Данные готовы | У юнитов описаны anti-shield/anti-wood и Fire Arrow параметры | Blueprints, Citizen construction/repair, walls/gates/Town Hall, foundations, ram/catapult и structure damage |
| Supply / raiding | Не реализовано | — | Supply network, отключение производства/respawn и raid gameplay |
| Command / active battles | Не реализовано | — | Map orders, rally/attack/defend/retreat markers и battle activity UI |
| Campaign / victory / progression | Не реализовано | — | 7-дневная кампания, persistence, reset, victory, статистика, профиль и cosmetics |
| Operations | Реализовано | PostgreSQL, Redis, Docker Compose, Prometheus, Grafana, Loki, Promtail, health checks, pprof option, load/protocol tooling | Production deployment policy, backups/migrations и подтверждение SLO целевыми нагрузочными тестами |

### Прогресс по этапам

- **MVP 1 — частично:** готовы сетевой фундамент, одна техническая карта, выбор Citizen/Spearman/Archer/Guard Swordsman, HP как состояние и базовые combat inputs. Критический незакрытый вертикальный срез: damage → death → respawn → stockpile → Unit Cost/Return → Banner claim.
- **MVP 2 — не начат на уровне gameplay:** ресурсные типы есть только в стоимости юнитов.
- **MVP 3 — не начат:** построек, blueprints, repair и foundations нет.
- **MVP 4 — частично только по данным:** Heavy Knight и Greatsword заведены в ростер, но их полноценные counters/abilities, supply, markers и массовый battle loop отсутствуют.
- **MVP 5 — не начат:** campaign, procedural map и 3 Kingdoms отсутствуют. Значение `max_connections=12000` — инфраструктурный лимит конфигурации, а не подтверждение готовности gameplay к 12 000 игроков.

## Управление

| Ввод | Действие |
|---|---|
| `WASD` | Движение |
| Мышь | Направление взгляда |
| `Shift` | Sprint с расходом stamina |
| ЛКМ | Атака / продолжение combo |
| ПКМ (удерживать) | Block, если он доступен юниту; движение снимает block |
| `F3` | Расширенная сетевая/FPS-статистика |

В development-сборке также доступны панель просмотра/редактирования юнитов и кнопка переподключения с повторным выбором юнита. Это отладочные инструменты, не игровые Unit Return или Respawn.

## Технологии

| Слой | Технологии |
|---|---|
| Client | TypeScript 5.7, PixiJS 8.6, Vite 6, Bun |
| Server | Go 1.25+ (`go.mod`), `gobwas/ws`, raw `net.Conn`; Docker build uses Go 1.26.2 |
| Data/config | PostgreSQL 16, Redis 7 |
| Observability | Prometheus, Grafana, Loki, Promtail |
| Tests | Go test suite, `bun test` для клиентского collision-модуля, protocol probes, Artillery |

## Быстрый запуск

Нужны Bun, Go 1.25+ и Docker Compose. Если `.env` ещё нет:

```bash
cp .example-env .env
make install
```

### Полностью в Docker

```bash
make docker-upbuild-core
```

Игра будет доступна на <http://localhost:8108>. Команда поднимает game server, PostgreSQL и Redis без monitoring stack.

Чтобы запустить весь стек:

```bash
make docker-upbuild
```

| Сервис | Адрес по умолчанию |
|---|---|
| Game | <http://localhost:8108> |
| Prometheus | <http://localhost:9090> |
| Grafana | <http://localhost:3000> (`admin` / значение `GRAFANA_ADMIN_PASSWORD`) |
| Loki | <http://localhost:3100> |

### Локальная разработка

Сервер больше не имеет файлового fallback для игровых настроек: перед локальным запуском ему нужны PostgreSQL и Redis.

```bash
# Терминал 1: инфраструктура и game container
make docker-up-core

# Остановить только game container, оставив PostgreSQL и Redis
docker compose -f docker/docker-compose.yml --project-name pixi_game --env-file .env stop game

# Терминал 2: Go server :8108 и Vite :8109
make dev
```

Для раздельного запуска используются `make dev-server` и `make dev-client`. Vite dev server доступен на <http://localhost:8109> и проксирует `/ws` и `/api` на Go server.

### Сборка

```bash
make build
make run
```

`make build-client` создаёт web bundle в `dist/`, `make build-server` — `dist/server`, `make build-release` — Linux release build.

## Конфигурация

Текущие значения проекта имеют приоритет над ранними ориентировочными числами GDD.

| Источник | Назначение | Применение изменений |
|---|---|---|
| `game_settings` в PostgreSQL | Tick rate, coordinate scale, world/spawn size, client scale, debug mode, background | После перезапуска; часть значений может быть переопределена environment variables |
| `units` в PostgreSQL | Ростер, статы, стоимость, способности и пути к ассетам | Live-reload через Redis; текущие HP/stamina уже подключённых игроков сохраняются до переподключения |
| `game_config` в PostgreSQL | Network/fanout limits и live spawn bounds | Live-reload через Redis |
| `campaigns` / `campaign_map_colliders` в PostgreSQL | Immutable snapshot карты активной кампании (сейчас один фиксированный `campaign_id=1` — полноценного campaign lifecycle ещё нет) | После перезапуска сервера, только на старте |
| `structure_collision_state` / `structure_colliders` в PostgreSQL | Revision и геометрия разрушаемых построек (walls/gates/towers); включает три тестовые стены вне spawn area | Восстанавливается при старте; runtime-изменения применяются только через игровую mutation-фазу, не прямой правкой строк |
| `.env` | Адреса сервисов, server/runtime overrides, credentials | Обычно после перезапуска |

Начальная схема и seed находятся в `docker/postgres/init/001_init.sql`. Этот файл применяется PostgreSQL только при первом создании пустого data directory; правка seed не меняет уже существующую БД — для уже инициализированного dev-volume новые таблицы (включая коллизионные из последнего пункта) нужно накатить вручную тем же SQL-файлом или пересоздать volume.

Для уже инициализированной development-БД новый размер применяется вручную, после чего game server нужно перезапустить:

```sql
UPDATE game_settings
SET world_width = 32000, world_height = 32000
WHERE id = 1;
```

Основной baseline:

| Параметр | Значение |
|---|---|
| Simulation tick | 20 Hz |
| Full sync interval | 30 s |
| Coordinate scale | 10 world units = 1 m |
| World | 32000×32000 units = 3200×3200 m = 3.2×3.2 km |
| Initial spawn area | X 1500–3000, Y 500–1500 |
| Configured connection limit | 12 000 |
| Binary protocol | v14 |

Live network settings можно менять утилитой:

```bash
make build-configctl
./dist/configctl list
./dist/configctl get fanout_target_ms
./dist/configctl set fanout_target_ms 6
./dist/configctl set-many world_state_active_staleness_ms=220 world_state_idle_staleness_ms=650
```

Unit editor включается через `ENABLE_UNIT_ADMIN_API=true` и требует `ADMIN_API_TOKEN` длиной не менее 32 байт. Management API слушает `MANAGEMENT_ADDR` (локально по умолчанию `127.0.0.1:8110`) и не публикуется на public game listener.

## HTTP и WebSocket endpoints

Public listener, по умолчанию `:8108`:

| Path | Назначение |
|---|---|
| `/` | Собранный web client |
| `/ws` | WebSocket game connection |
| `/health` | Health check сервера и game loop |
| `/api/config` | Client-facing world/game config |
| `/api/units` | Актуальный ростер юнитов |
| `/api/map-colliders` | Immutable snapshot статической геометрии карты активной кампании (ETag/304); структуры (стены, ворота) передаются отдельно по WebSocket |

Management listener, по умолчанию `127.0.0.1:8110`:

| Path | Назначение |
|---|---|
| `/metrics` | Prometheus metrics |
| `/metrics/json` | Legacy JSON metrics |
| `/debug/pprof/` | Профилирование при `ENABLE_PPROF=true` |
| `PATCH /api/admin/units/{typeId}` | Изменение unit stats при включённом admin API и Bearer token |

## Архитектура текущего прототипа

- Сервер авторитетно применяет последний movement input на фиксированном tick и обрабатывает очередь дискретных attack/block/face actions.
- Simulation разбивает обработку игроков между постоянными tick workers. Каждый игрок движется через integer-only swept-collision solver (`internal/collision`) против неизменяемой карты (`MapStaticGrid`) и разрушаемых построек (`StructureGrid`); anti-tunneling обеспечен micro-step проверкой по 1 world unit, диагональное столкновение превращается в sliding. Игроки друг сквозь друга проходят осознанно (ghost movement) — `DynamicGrid` пересобирается каждый tick и готовит spatial index для будущего combat, но не используется для взаимной блокировки движения. Combat damage пока отсутствует.
- Постройки с собственным коллайдером (стены, ворота, башни) меняют solid-state только через revision-gated mutation batch на границе тика, с footprint-clearance проверкой перед активацией — детали в `docs/collisions_plan.md`.
- Каждое WebSocket-соединение имеет read loop и постоянный write loop с очередью размером 32. Broadcast frame разделяется между получателями с reference counting.
- Репликация использует full snapshots и delta state, velocity prediction, keyframes, приоритет свежих/активных соединений и time dilation/backpressure под нагрузкой.
- Клиент выполняет prediction/reconciliation локального движения и интерполяцию удалённых игроков через тот же solver, что и сервер (чистый TypeScript-порт `src/client/collision/` без зависимости от Pixi) — карта грузится один раз через `/api/map-colliders`, актуальные постройки синхронизируются snapshot/delta-сообщениями по WebSocket с revision-based resync при расхождении; декодирование сети вынесено в Web Worker.
- Public и management endpoints разделены: metrics, pprof и unit admin API не доступны на игровом listener.

## Проверки

```bash
# Все Go unit/integration tests
cd src/server && go test ./...

# Production client build
bun run build:client

# Отдельная проверка TypeScript
./node_modules/.bin/tsc --noEmit

# Клиентский collision-модуль (src/client/collision) — включает сценарии,
# скопированные из Go golden-тестов для проверки client/server паритета
bun test src/client/collision/

# End-to-end binary protocol probes
make protocol-test

# Artillery load test (нужен запущенный server)
make load-test
```

Дополнительные сценарии и интерпретация результатов описаны в `utils/testing/protocol/README.md`, `utils/testing/artillery/README.md` и `docs/fanout_latency_playbook.md`.

## Приоритет следующего вертикального среза

Чтобы завершить MVP 1, практичнее всего идти по зависимостям:

1. Server-side hit detection, damage/DR и синхронизация HP.
2. Death и настоящий respawn с выбором разрешённого юнита.
3. Kingdom membership и friendly/enemy distinction.
4. Local Stockpile, Unit Cost и атомарная покупка роли.
5. Unit Return с 100% refund возле friendly respec point и 0% refund при смерти.
6. Territory model, physical Claim Banner и capture timer.

После этого появится минимальный проверяемый loop GDD: **выбрать роль → сражаться → сохранить или потерять вложенные ресурсы → захватить территорию**.

## Документы

- [Основной GDD](docs/GDD%20%E2%80%94%20Persistent%20Medieval%20War.md)
- [Юниты и баланс](docs/UNITS.md)
- [План системы коллизий](docs/collisions_plan.md)
- [Protocol tests](utils/testing/protocol/README.md)
- [Artillery tests](utils/testing/artillery/README.md)
- [Fanout/latency playbook](docs/fanout_latency_playbook.md)
