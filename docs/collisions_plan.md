# Базовая система коллизий игроков со статическими объектами

## Карта плана

Ключевые части реализации вынесены в отдельные разделы:

1. `Spatial Grid: размеры и адресация` — общая структура cells, индексация, buckets и deduplication.
2. `MapStaticGrid` — immutable индекс сгенерированной карты, базовых замков и природных препятствий.
3. `StructureGrid` — индекс неподвижных, но создаваемых и разрушаемых во время кампании построек.
4. `Anti-tunneling и sliding algorithm` — swept broad phase, micro-steps по 1 world unit, hard limit скорости и X→Y sliding.
5. `DynamicGrid` — отдельный front/back индекс итоговых позиций игроков, перестраиваемый после movement каждый tick.
6. `Порядок simulation tick` — точная граница между structure mutations, parallel movement, rebuild Dynamic Grid, будущим combat и replication.

`Spatial Grid` — это общий подход к пространственному разбиению, а не отдельный третий тип данных. Внутри `CollisionWorld` существуют три независимых слоя с одинаковой адресацией cells: неизменяемый на протяжении кампании `MapStaticGrid`, изменяемый только событиями `StructureGrid` и пересобираемый каждый tick `DynamicGrid`.

## Цель и критерии готовности

Система должна авторитетно запрещать игроку пересекать статические препятствия при обычном движении, sprint и максимальной разрешённой скорости, сохраняя плавный client prediction без постоянных server corrections. Расчёт обязан сохранить текущий fixed-tick и integer-coordinate подход проекта, разрешать параллельное выполнение movement workers и не вводить полноценный physics engine.

Функция считается готовой, когда:

- foot ellipse игрока никогда не пересекает внутреннюю область enabled AABB карты или solid-постройки;
- игрок не проходит сквозь препятствие толщиной от `1 world unit` при любом разрешённом движении до `64 world units/tick`;
- диагональное движение вдоль стены превращается в sliding, а не в полную остановку;
- server и client получают одинаковый результат на общем наборе golden scenarios;
- два игрока проходят друг сквозь друга и ни один расчёт движения не изменяет состояние второго;
- MapStaticGrid и StructureGrid не создают allocations и не требуют mutex contention в movement hot path;
- добавление, разрушение или смена solid-state одной постройки изменяет только затронутые StructureGrid cells и не перестраивает карту целиком;
- Dynamic Grid существует, перестраивается каждый tick и содержит согласованные итоговые позиции всех игроков для следующего combat-этапа;
- collision phase для 1700 игроков занимает не более 10% базового бюджета tick в синтетическом benchmark типичной карты.

## Summary

Реализовать server-authoritative коллизии без физического движка:

- игрок — axis-aligned foot ellipse с центром между ступнями; ширина покрывает полную видимую ширину персонажа в authored neutral/movement sprite с padding, глубина — только занимаемую площадь земли;
- статические объекты — axis-aligned rectangles;
- игроки проходят друг сквозь друга и не влияют на движение других игроков;
- движение проверяется дискретными шагами по 1 world unit, что исключает tunneling при текущих скоростях;
- при диагональном столкновении используется sliding;
- геометрия сгенерированной карты загружается один раз и хранится в immutable `MapStaticGrid`;
- стены, ворота, шахты, лесопилки и другие неподвижные постройки хранятся в event-driven `StructureGrid`;
- клиент получает immutable map snapshot, актуальный structure snapshot и последующие structure collision deltas;
- movement solver проверяет `MapStaticGrid` и `StructureGrid`;
- `DynamicGrid` перестраивается каждый tick, хранит spatial bounds игроков и готовит broad phase для combat;
- текущие parallel tick workers сохраняются.

## Implementation Changes

### Зафиксированные правила координат и геометрии

- `1 world unit = 0.1 м`; authoritative position игрока хранится как целые X/Y и означает `groundPoint` между ступнями — центр movement foot ellipse.
- Foot ellipse задаётся двумя положительными целыми значениями `radiusX/radiusY` для каждого unit type. `radiusX` покрывает полную видимую ширину персонажа в neutral/movement sprite плюс authored padding `1–2 source px`, округлённый наружу до world units; прозрачные поля texture и отдельные attack/effect overhang не учитываются. `radiusY` независимо описывает глубину опоры на земле.
- Footprint не зависит от facing или animation frame: оружие, плащ, attack effect, голова и торс никогда не меняют movement collider. Изменение размеров существующего игрока при live config запрещено без controlled respawn/revalidation.
- Movement collider, combat hurtbox и attack hitbox являются разными понятиями. Foot ellipse нельзя автоматически использовать как окончательную combat-геометрию.
- Static AABB задаётся `minX`, `minY`, `maxX`, `maxY`. Требуется `minX < maxX` и `minY < maxY`; минимальная толщина объекта — `1 world unit`.
- Внутренняя область AABB считается solid. Касание границы разрешено: ellipse сталкивается с AABB только при строгом ellipse-overlap; равенство означает корректный контакт без проникновения.
- Все authoritative вычисления выполняются через `int32`/`int64`. `uint16` используется только при хранении/кодировании уже проверенной позиции. Float calculations в collision solver запрещены.
- Порядок разрешения осей всегда X, затем Y. Он одинаков на сервере и клиенте и не зависит от tick, player ID или порядка объектов.
- Игроки находятся в ghost-mode относительно движения: их footprints не участвуют в `MoveFootprint`, хотя сами игроки индексируются Dynamic Grid.

### Категории объектов и критерий выбора grid

Категория определяется не только возможностью физически сдвинуть объект, но и его жизненным циклом:

| Объект | Grid | Причина |
|---|---|---|
| Рельеф, скалы, вода, мосты как часть generation | `MapStaticGrid` | Не меняются до конца кампании |
| Постоянный корпус базового замка | `MapStaticGrid` | Является частью сгенерированной карты |
| Разрушаемая секция замка или ворота | `StructureGrid` | Solid-state может измениться |
| Стена, башня, шахта, лесопилка, Town Hall | `StructureGrid` | Создаётся/разрушается во время кампании, хотя не двигается |
| Игрок | `DynamicGrid` | Позиция меняется каждый tick |
| Телега, таран, другой будущий movable object | `DynamicGrid` | Позиция может меняться каждый tick |

Ownership, HP, production и render state сами по себе не определяют grid. Если геометрия объекта не меняется до конца кампании, она относится к карте; если collider может появиться, исчезнуть или переключиться, это структура.

Campaign generator может создать оба вида данных за один проход. Например, для базового замка он записывает неразрушаемый фундамент/скалы в map snapshot, а ворота и разрушаемые wall sections — как стартовые completed structure entities. Аналогично природный Dense Forest/Stone Deposit/Iron Vein относится к карте, а построенные на разрешённой точке Sawmill/Mine/Quarry — к StructureGrid.

### Collision model и хранение карты

- При создании кампании генератор формирует полный collision snapshot карты и сохраняет его вместе с `campaign_id`, seed и версией алгоритма генерации. Хранить итоговый snapshot необходимо, чтобы обновление генератора после deploy не изменило уже идущую кампанию.
- Использовать таблицу `campaign_map_colliders`:

```sql
CREATE TABLE IF NOT EXISTS campaign_map_colliders (
    campaign_id BIGINT NOT NULL,
    collider_id BIGINT NOT NULL,
    min_x       INTEGER NOT NULL,
    min_y       INTEGER NOT NULL,
    max_x       INTEGER NOT NULL,
    max_y       INTEGER NOT NULL,
    render_kind TEXT NOT NULL DEFAULT 'stone_wall',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    PRIMARY KEY (campaign_id, collider_id),
    CHECK (campaign_id > 0 AND collider_id > 0),
    CHECK (min_x >= 0 AND min_y >= 0),
    CHECK (max_x > min_x AND max_y > min_y)
);
```

- Координаты и размеры хранить целыми world units; `min < max`, объект обязан находиться внутри мира.
- В первой версии loader принимает только непустой `render_kind`, известный клиентскому registry; seed использует `stone_wall`. Неизвестный kind является startup error, чтобы физика не загрузилась без соответствующего отображения.
- Startup-код загружает и валидирует enabled colliders только активной кампании. Невалидный объект останавливает запуск сервера.
- Сортировать строки по `collider_id`, преобразовывать ID объекта во внутренний плотный index и хранить геометрию в contiguous slice. Grid buckets содержат внутренние indices, а не указатели и не database IDs.
- Ввести startup limits: не более `100 000` enabled colliders и не более `2 000 000` суммарных записей collider→cell. Превышение останавливает запуск с понятной ошибкой, чтобы ошибочная карта не исчерпала память.
- Для технической карты, пока campaign generator не реализован, разрешить один заранее созданный map snapshot с фиксированным `campaign_id`.
- Начало новой кампании является controlled world transition: остановить admission и ticks старого мира, создать/загрузить новый snapshot, построить три новых grid, сменить campaignId/mapVersion и только затем допускать подключения. Горячо заменять MapStaticGrid посреди movement tick запрещено.

### Хранение построек

- Gameplay entity постройки и её collision geometry хранить раздельно: `structures` содержит тип, владельца, HP, construction state и production; `structure_colliders` содержит один или несколько AABB одной постройки.
- Collision persistence использует отдельную revision активной кампании:

```sql
CREATE TABLE IF NOT EXISTS structure_collision_state (
    campaign_id BIGINT PRIMARY KEY,
    revision    BIGINT NOT NULL DEFAULT 0,
    CHECK (campaign_id > 0 AND revision >= 0)
);

CREATE TABLE IF NOT EXISTS structure_colliders (
    campaign_id     BIGINT NOT NULL,
    structure_id    BIGINT NOT NULL,
    part_id         INTEGER NOT NULL,
    min_x           INTEGER NOT NULL,
    min_y           INTEGER NOT NULL,
    max_x           INTEGER NOT NULL,
    max_y           INTEGER NOT NULL,
    render_kind     TEXT NOT NULL,
    solid           BOOLEAN NOT NULL,
    updated_revision BIGINT NOT NULL,
    PRIMARY KEY (campaign_id, structure_id, part_id),
    CHECK (campaign_id > 0 AND structure_id > 0 AND part_id > 0),
    CHECK (min_x >= 0 AND min_y >= 0),
    CHECK (max_x > min_x AND max_y > min_y),
    CHECK (updated_revision >= 0)
);
```

- Gameplay state transition, collider rows и новая revision фиксируются одной PostgreSQL transaction. В память попадает только успешно сохранённый mutation batch; повторный запуск восстанавливает ровно опубликованную revision.
- Для выдачи revision transaction блокирует строку `structure_collision_state` активной кампании через `SELECT ... FOR UPDATE`, увеличивает revision ровно на 1 и записывает все collider changes. Параллельные команды строительства поэтому не получают одинаковый номер.
- Database worker не изменяет grid напрямую. После commit он передаёт immutable batch в game-loop queue; `effectiveTick` назначается не раньше следующего ещё не начавшегося tick. Ошибка commit отклоняет gameplay transition и не создаёт mutation.
- Ввести отдельные limits для StructureGrid: не более `100 000` collider parts и `2 000 000` collider→cell memberships активной кампании. Placement, который превысит limit, отклоняется до persistence/mutation.
- Минимальный collision record:

```text
StructureCollider
  structureId: uint64
  partId: uint32
  minX, minY, maxX, maxY: int32
  renderKind: string
  solid: bool
```

- Composite key `(structureId, partId)` стабильно идентифицирует часть составной постройки. Замок может иметь отдельные AABB стен, башен и ворот; разрушение ворот не удаляет постоянный корпус.
- При запуске `StructureGrid` восстанавливается из всех существующих colliders активной кампании с `solid=true`.
- Изменения построек применяются без restart. Каждое принятое изменение получает монотонный `structureCollisionRevision` и effective tick.
- Добавить три видимые завершённые тестовые стены вне spawn area как structure records, а не как часть карты:
  - вертикальная: `(3300,700)–(3340,1800)`;
  - горизонтальная: `(3300,1760)–(4300,1800)`;
  - отдельный блок: `(4200,600)–(4500,900)`.
- Ручное изменение collision rows напрямую в PostgreSQL не является live-update API. Runtime changes проходят только через authoritative gameplay command/event; после restart состояние восстанавливается из БД.

### Startup data flow

```text
active campaign
    ├── campaign map metadata: seed + generatorVersion
    ├── campaign_map_colliders ORDER BY collider_id
    └── solid structure_colliders ORDER BY structure_id, part_id
                         ↓
          validate bounds, kinds and limits
                         ↓
    ┌────────────────────┴────────────────────┐
    ↓                                         ↓
build immutable MapStaticGrid          build StructureGrid
calculate map SHA-256 version          restore collision revision
    └────────────────────┬────────────────────┘
                         ↓
                  start GameWorld
                         ↓
       serve map + current structure snapshots
```

- Collision world должен быть полностью создан до запуска game loop; `GameWorld` получает готовую зависимость в конструкторе.
- Map version/hash вычислять по canonical binary sequence `campaignId,generatorVersion,colliderId,minX,minY,maxX,maxY,renderKind`, отсортированной по collider ID. Порядок строк PostgreSQL не должен менять version.
- `structureCollisionRevision` не входит в immutable map hash. Это отдельный монотонный номер актуального structure snapshot.
- Если map collider table пуста, сервер может стартовать с пустым MapStaticGrid; world bounds всё равно продолжают работать как препятствия.
- До допуска игрока в session клиент должен получить совпадающие `campaignId`, `mapVersion` и текущий structure snapshot revision.

### Основные server-side типы и контракты

Collision-код разместить в отдельном `internal/collision` package, не смешивая его с WebSocket protocol или client-facing JSON.

```go
const (
    GridCellSize         int32 = 64
    MaxMoveSteps         int32 = 64
    MaxMapColliders            = 100_000
    MaxMapMemberships          = 2_000_000
    MaxStructureColliders      = 100_000
    MaxStructureMemberships    = 2_000_000
)

type PlayerFootprint struct {
    RadiusX int32
    RadiusY int32
}

type MapAABB struct {
    ID            uint64
    MinX, MinY    int32
    MaxX, MaxY    int32
    RenderKind    string
}

type StructureAABB struct {
    StructureID uint64
    PartID      uint32
    MinX, MinY  int32
    MaxX, MaxY  int32
    RenderKind  string
}

type StructureMutation struct {
    Operation StructureMutationOperation
    Key       StructureColliderKey
    Collider  StructureAABB
}

type StructureMutationBatch struct {
    Revision      uint64
    EffectiveTick uint64
    Mutations     []StructureMutation
}

type MoveResult struct {
    X, Y                    uint16
    EffectiveDX             int8
    EffectiveDY             int8
    BlockedX                bool
    BlockedY                bool
    MapCandidateCount       uint32
    StructureCandidateCount uint32
    NarrowPhaseChecks       uint32
}

type DynamicBody struct {
    ID               uint32
    Kind             DynamicBodyKind
    X, Y             uint16
    RadiusX, RadiusY uint16
}
```

Минимальные методы:

```go
BuildMapStaticGrid(worldWidth, worldHeight int32, colliders []MapAABB) (*MapStaticGrid, error)
BuildStructureGrid(worldWidth, worldHeight int32, colliders []StructureAABB, revision uint64) (*StructureGrid, error)
(*MapStaticGrid).QueryAABB(bounds AABB, scratch *QueryScratch) []uint32
(*StructureGrid).QueryAABB(bounds AABB, scratch *QueryScratch) []uint32
(*StructureGrid).ApplyMutationBatch(batch StructureMutationBatch) error
(*CollisionWorld).MoveFootprint(x, y uint16, dx, dy int8, distance int32, footprint PlayerFootprint, scratch *MoveScratch) MoveResult
(*CollisionWorld).FindNearestFreeFootprint(x, y uint16, footprint PlayerFootprint, searchLimit int32, scratch *MoveScratch) (uint16, uint16, bool)

(*DynamicGrid).Rebuild(players []PlayerSnapshot)
(*DynamicGrid).QueryCircle(...)
(*DynamicGrid).QueryAABB(...)
(*DynamicGrid).QuerySegment(...)
```

- `MoveResult` возвращает счётчики для агрегации metrics без дополнительного обхода.
- `QueryScratch` и `MoveScratch` создаются по одному экземпляру на tick worker; методы не сохраняют ссылки на scratch после возврата.
- MapStaticGrid владеет immutable slice colliders. После публикации grid изменение этого slice запрещено до завершения кампании.
- StructureGrid владеет runtime structure colliders и меняет buckets только в dedicated mutation phase, когда movement workers не запущены.
- `CollisionWorld` содержит `mapStatic *MapStaticGrid`, `structures *StructureGrid` и `dynamic *DynamicGrid`; `MoveFootprint` читает map и structure layers, но намеренно не читает DynamicGrid.
- `GameWorld` создаёт worker scratch вместе с `tickWorkerChs`, поэтому количество scratch равно `GOMAXPROCS` и не зависит от числа игроков.

### Архитектура CollisionWorld

```text
CollisionWorld
├── MapStaticGrid
│   ├── terrain/map objects
│   └── постоянные части базовых замков
├── StructureGrid
│   ├── стены, ворота и башни
│   └── шахты, лесопилки и другие постройки
└── DynamicGrid
    ├── игроки (реализуется сейчас)
    └── movable objects (будущий этап)
```

Все три grid используют одинаковое разбиение мира на cells размером `64×64 world units`, но имеют разный жизненный цикл. Они не объединяются в одну изменяемую структуру: map layer остаётся immutable, structure layer обновляется только редкими событиями, а dynamic layer дешёво пересобирается каждый tick.

### Spatial Grid: размеры и адресация

Для мира `32000×32000` (`3.2×3.2 км`) и `cellSize=64`:

```text
columns = 32000 / 64 = 500
rows    = 32000 / 64 = 500
cells   = 500 × 500 = 250000
```

Размер мира кратен размеру cell, поэтому крайние неполные cells отсутствуют. Общая формула integer ceiling ниже всё равно обязательна для тестов, локальных simulation regions и возможных карт другого размера.

При 64-bit Go один slice header занимает 24 bytes. Плотный массив из `250000` bucket headers требует примерно `5.7 MiB` на grid buffer без учёта содержимого. MapStaticGrid + StructureGrid + два DynamicGrid buffers занимают около `22.9 MiB` базовых headers — приемлемо для одного мира, но полный обход всех cells каждый tick запрещён.

Расчёт должен использовать integer ceiling:

```text
columns = (worldWidth  + cellSize - 1) / cellSize
rows    = (worldHeight + cellSize - 1) / cellSize
```

Координата переводится в cell так:

```text
cellX = clamp(x / cellSize, 0, columns - 1)
cellY = clamp(y / cellSize, 0, rows - 1)
flatIndex = cellY * columns + cellX
```

Для AABB/query bounds вычисляются inclusive диапазоны cells:

```text
minCellX = clamp(minX / cellSize, 0, columns - 1)
maxCellX = clamp(maxX / cellSize, 0, columns - 1)
minCellY = clamp(minY / cellSize, 0, rows - 1)
maxCellY = clamp(maxY / cellSize, 0, rows - 1)
```

Включение пограничной cell может дать лишнего broad-phase кандидата, но не может пропустить столкновение; narrow phase отфильтрует лишнее. Такая консервативность предпочтительнее специальных `max-1` правил и одинаково работает для касаний.

Хранение:

```text
MapStaticGrid
  cellSize: int32 = 64
  columns: int32
  rows: int32
  buckets: [][]uint32       // dense collider indices
  colliders: []MapAABB      // sorted by database ID

StructureGrid
  cellSize: int32 = 64
  columns: int32
  rows: int32
  revision: uint64
  buckets: [][]uint32       // active structure-collider handles
  colliders: []StructureAABB
  lookup: map[(structureId, partId)]handle // mutation phase only

DynamicGridBuffer
  buckets: [][]uint32       // dense dynamic-body indices
  bodies: []DynamicBody
  touchedCells: []uint32    // только занятые cells этого buffer
```

- MapStaticGrid buckets создаются один раз. StructureGrid изменяет только buckets затронутых cells. DynamicGrid очищает через `bucket = bucket[:0]` только индексы из собственного `touchedCells`, а затем сбрасывает `touchedCells[:0]`, сохраняя capacity.
- Большой AABB регистрируется во всех cells от `(minCellX,minCellY)` до `(maxCellX,maxCellY)` включительно.
- Query обходит cells в фиксированном порядке: Y сверху вниз, внутри строки X слева направо.
- Один collider/body может встретиться в нескольких buckets. Для deduplication использовать `seenGeneration []uint32` и счётчик `generation`, а не map allocation.
- При переполнении `generation` очистить весь `seenGeneration` и начать с 1. Значение 0 означает «не посещался».
- Каждый tick worker получает собственный `CollisionScratch`: отдельные candidate slices и `seenGeneration` для map/structure layers, а также generation counters. Общего mutable scratch между workers нет.
- Broad phase никогда не определяет сам collision; он только формирует superset кандидатов для narrow phase.

### MapStaticGrid

- Создать immutable плоскую uniform grid, индексируемую как одномерный массив buckets по координатам cell.
- Каждый объект зарегистрировать во всех затронутых cells.
- Построить grid один раз после загрузки и валидации snapshot активной кампании; после старта simulation не изменять.
- Разрешить всем tick workers конкурентное чтение без mutex.
- Для каждого movement строить swept bounds пути игрока, получать map candidates только из соответствующих cells и удалять дубликаты через reusable per-worker scratch buffers.
- Swept query bounds рассчитывать один раз на весь tick movement:

```text
intendedX = startX + desiredDX * distance
intendedY = startY + desiredDY * distance

queryMinX = min(startX, intendedX) - footprint.radiusX
queryMaxX = max(startX, intendedX) + footprint.radiusX
queryMinY = min(startY, intendedY) - footprint.radiusY
queryMaxY = max(startY, intendedY) + footprint.radiusY
```

- Query bounds раздельно расширяются `radiusX/radiusY`, затем clamp к world bounds и переводятся в cell range. Все варианты sliding X/Y остаются внутри этого rectangle, поэтому повторно обращаться к grid на каждом micro-step не требуется.
- Ellipse-vs-AABB считать целочисленно:
  
```text
closestX = clamp(centerX, box.minX, box.maxX)
closestY = clamp(centerY, box.minY, box.maxY)
deltaX = centerX - closestX
deltaY = centerY - closestY

overlap = deltaX²*radiusY² + deltaY²*radiusX²
          < radiusX²*radiusY²
```

  Все квадраты и произведения считать в `int64` с startup validation максимальных radii/coordinates против overflow. Если центр находится внутри AABB, обе delta равны нулю и overlap гарантирован. Равенство означает касание и разрешено.

### StructureGrid

StructureGrid содержит неподвижную геометрию, способную измениться в течение кампании. Он не перестраивается каждый tick и не смешивается с игроками.

- Collider становится участником movement collision только при `solid=true`. Blueprint, passable ruins и отключённая створка ворот могут существовать как gameplay/render entity, не присутствуя в grid buckets.
- `upsert` новой solid-части валидирует bounds и отсутствие duplicate composite key, добавляет запись во все затронутые cells и повышает revision.
- `remove/disable` удаляет handle из всех ранее занятых cells, затем помечает slot свободным. Event-driven mutation может выделять память при росте capacity, но allocations запрещены внутри movement query.
- Освобождённые slots можно переиспользовать только после удаления handle из всех buckets. После batch, увеличившего максимальный handle, mutation phase заранее расширяет `seenGeneration` каждого worker scratch; movement query никогда не делает это лениво.
- Для повторяемого порядка affected buckets хранят handles, отсортированные по `(structureId,partId)`. Вставка/удаление выполняется binary search; стоимость допустима, потому что structure events редки относительно ticks.
- Mutation batches обрабатываются по `revision`, а operations внутри одного batch сортируются по `(structureId,partId)`. Следующая revision обязана равняться `currentRevision + 1`; пропуск или повтор является ошибкой и инициирует восстановление из authoritative snapshot.
- Все mutations применяет только game-loop goroutine в выделенной фазе до запуска movement workers. Во время parallel movement StructureGrid логически immutable, поэтому query не использует mutex/atomic.
- Перед изменением buckets валидировать весь batch, вычислить старые/новые cell memberships и проверить limits. Только после этого применять operations. Одно gameplay-событие может атомарно менять несколько collider parts; новая revision публикуется после успешного применения всего batch, а частично изменённая составная постройка недопустима.
- Для placement validation запрашивать одновременно MapStaticGrid и StructureGrid. Запрещать пересечение существующих solid colliders, выход за world bounds и нарушение разрешённых зон GDD.
- Изменение HP без смены геометрии не трогает grid. Grid mutation создаётся только при появлении/исчезновении collider или изменении его solid-state/AABB.

#### Collision lifecycle постройки

```text
Blueprint                    -> gameplay entity, collision disabled
UnderConstruction            -> collision disabled в первой версии
CompletionPendingClearance   -> готова, но footprint занят игроком
Completed                    -> collision enabled
Damaged                      -> collision enabled
DestroyedFoundation / Ruins  -> collision disabled; через breach можно проходить
Rebuilding                   -> collision disabled
Completed                    -> collision enabled после clearance check
```

- При переходе в solid-state проверить footprint через DynamicGrid и точный ellipse-vs-AABB. Если внутри находится игрок, не создавать collider поверх него и не телепортировать игрока: оставить постройку в `CompletionPendingClearance` и повторять проверку в следующих ticks.
- Игрок внутри footprint считается препятствием только для активации collider, но не для placement blueprint. Правила территории/дистанции и доступности blueprint проверяются отдельной building system.
- Такое ожидание позволяет защитникам удерживать breach и исключает серверное вытеснение/телепортацию. UI должен показать причину остановки завершения.
- Закрытие ворот использует ту же clearance-проверку. Открытие ворот немедленно планирует disable mutation на ближайшую structure mutation phase.
- Если составная постройка включает несколько новых solid parts, clearance проверяет union всех их AABB, а активация выполняется одним batch. Нельзя активировать только свободную половину здания.
- Если в будущем дизайн потребует solid foundation с начала строительства, меняется только mapping state→solid; StructureGrid API остаётся тем же.

### Anti-tunneling и sliding algorithm

#### Зафиксированное решение

В первой версии используется **discrete swept movement**: MapStaticGrid и StructureGrid один раз выбирают все AABB вдоль полного предполагаемого пути, после чего narrow phase проверяет каждое последовательное смещение центра на 1 world unit. Continuous collision detection с вычислением time-of-impact не требуется: при целочисленных координатах, минимальной толщине стены 1 и шаге 1 выбранный алгоритм даёт ту же обязательную гарантию «не перескочить стену», проще сохраняет Go/TypeScript parity и имеет жёстко ограниченную стоимость.

Защита от чрезмерной нагрузки состоит из трёх уровней:

1. MapStaticGrid и StructureGrid ограничивают narrow-phase candidates только swept-областью пути.
2. Один tick выполняет не более `MaxMoveSteps = 64` micro-steps на игрока.
3. Startup/live unit-config validation отклоняет параметры, способные дать `distance > 64`; runtime обнаружение такого состояния логирует invariant violation и пропускает движение этого игрока в tick, а не clamp-ит его и не запускает неограниченный цикл.

Текущая максимальная фактическая скорость без collision limit:

```text
Rogue: 33.4 м/с
Sprint: ×1.5
Tick rate: 20 Hz

33.4 × 1.5 / 20 = 2.505 м/tick
2.505 м × 10 world units/m = 25.05 world units/tick
```

Проверка только конечной точки позволила бы пройти через стену тоньше примерно 25 world units. Поэтому distance разбивается на micro-steps длиной ровно 1 world unit. При сохранении инварианта «стартовая позиция свободна», положительных `radiusX/radiusY` и минимальной толщине AABB 1 такой шаг не может перескочить через solid interval ни по X, ни по Y.

Перед циклом solver делает по одному broad-phase query в MapStaticGrid и StructureGrid по одинаковым swept bounds полного пути. Даже если X заблокируется и движение продолжится только по Y, либо X снова освободится после края стены, фактическая траектория остаётся внутри этих bounds. Поэтому повторный grid query для каждого micro-step не нужен; на шагах повторяется только дешёвый integer narrow phase по двум уже собранным спискам candidates.

Полный solver:

```text
MoveFootprint(start, desiredDX, desiredDY, distance, footprint,
           mapCandidates, structureCandidates):
    x = start.x
    y = start.y
    lastStepMovedX = false
    lastStepMovedY = false

    repeat distance times:
        movedX = false
        movedY = false

        if desiredDX != 0:
            candidateX = x + desiredDX
            if center (candidateX, y) is inside world bounds
               and ellipse(candidateX, y, footprint) overlaps no map/structure AABB:
                x = candidateX
                movedX = true

        if desiredDY != 0:
            candidateY = y + desiredDY
            if center (x, candidateY) is inside world bounds
               and ellipse(x, candidateY, footprint) overlaps no map/structure AABB:
                y = candidateY
                movedY = true

        lastStepMovedX = movedX
        lastStepMovedY = movedY

        if not movedX and not movedY:
            break

    effectiveVX = desiredDX if lastStepMovedX else 0
    effectiveVY = desiredDY if lastStepMovedY else 0
    return x, y, effectiveVX, effectiveVY
```

Свойства алгоритма:

- движение прямо в стену останавливается на точном integer contact;
- при диагональном input заблокированная ось остаётся на месте, свободная продолжает движение — это sliding;
- X проверяется до Y всегда, поэтому результат на углах повторяем;
- если X был временно заблокирован, solver пробует его снова после каждого успешного Y-step и автоматически продолжает X после выхода за край стены;
- effective velocity отражает возможность продолжить движение после последнего micro-step, а не факт любого перемещения ранее в tick; remote dead reckoning поэтому не продолжает двигать игрока сквозь стену;
- если обе оси не двинулись, дальнейшие одинаковые micro-steps ничего не изменят и loop безопасно завершается раньше;
- solver никогда не меняет map/structure collider, другого игрока или исходный desired input.

Расчёт `distance` до solver остаётся совместим с текущей fixed-point remainder моделью:

```text
milliRate = round(moveSpeed * unitsPerMeter * 1000 / tickRate)
milliRate = round(milliRate * sprintMultiplier) when sprinting
milliRate = round(milliRate / sqrt(2)) for diagonal input
total = previousRemainderMilli + milliRate
distance = total / 1000
nextRemainderMilli = total % 1000
```

Collision не возвращает неиспользованную дистанцию в remainder: blocked movement теряется в текущем tick. Только дробный остаток меньше одного world unit переносится дальше.

- До запуска solver проверять `0 <= distance <= 64`. Значение выше означает нарушение unit/config validation и не должно молча приводиться к 64; отрицательное значение является программной ошибкой.
- Unit validation проверяет максимальный sprint distance для каждого unit относительно текущих `tickRate` и `unitsPerMeter`; worst case — движение по одной оси, потому что diagonal multiplier уменьшает distance.
- World bounds применять к центру ellipse раздельно: `X ∈ [radiusX, width−radiusX]`, `Y ∈ [radiusY, height−radiusY]`.

### Восстановление невалидной позиции и spawn

- Обычный movement solver принимает только свободную стартовую позицию. Он не выполняет depenetration и не «выталкивает» игрока из стены во время tick.
- Перед добавлением игрока в мир проверить случайную spawn position против MapStaticGrid, StructureGrid и world bounds.
- Если позиция занята, выполнить deterministic expanding-ring search по integer positions:
  1. проверить origin;
  2. для `radius = 1..128 world units` обходить квадратное кольцо по часовой стрелке, начиная с верхней левой точки;
  3. выбрать первую позицию, на которой player foot ellipse свободен;
  4. если свободная позиция не найдена, отклонить spawn с контролируемой ошибкой и не добавлять игрока в `playersMap`.
- Тот же поиск применять при загрузке сохранённой позиции и при смене размеров footprint в будущих версиях.
- Player-player overlap при spawn разрешён и не участвует в поиске.

### DynamicGrid

- Реализовать Dynamic Grid сразу, даже несмотря на отключённую player-player movement collision.
- На первом этапе хранить в нём snapshot всех игроков:

```text
DynamicBody
  id: uint32
  kind: player
  centerX: uint16
  centerY: uint16
  radiusX: uint16
  radiusY: uint16
```

- Регистрировать игрока во всех cells, которых касается AABB его foot ellipse; query обязан удалять повторяющиеся ID.
- Использовать два заранее выделенных буфера:
  - `front` — завершённый immutable snapshot, доступный для spatial queries;
  - `back` — очищает только buckets из собственного `touchedCells` и заполняется итоговыми позициями нового tick.
- При первой вставке body в пустой bucket добавить flat cell index в `touchedCells`. Повторные вставки в уже непустой bucket не добавляют индекс повторно.
- Никогда не очищать DynamicGrid циклом по всем `250000` cells: стоимость rebuild должна зависеть от количества игроков и реально занятых cells, а не от площади пустой карты.
- После завершения parallel movement workers последовательно заполнить `back` из итоговых позиций игроков и поменять `front/back` местами под контролем единственной game-loop goroutine. Atomic pointer и mutex для swap не нужны, пока queries не выходят за границы game-loop phase.
- Для 1700–12 000 локальных вставок отдельная последовательная rebuild-фаза дешевле и безопаснее mutex или worker-local merge.
- Не выделять память на каждый tick: buckets сохраняют capacity, query принимает destination buffer вызывающей стороны, дедупликация использует generation counters/reusable scratch.
- Перед rebuild получить игроков из уже собранного стабильного slice и отсортировать по `playerId`; `playersMap` напрямую не обходить. `bodies[index]` и порядок ID остаются повторяемыми.
- `QueryCircle(center,radius)` как broad-phase helper для будущей combat shape:
  - получает candidates из cells AABB запроса;
  - удаляет дубликаты;
  - не трактует movement ellipse как окончательный combat hurtbox; exact combat narrow phase получает отдельную hurtbox unit definition;
  - возвращает IDs по возрастанию.
- `QueryAABB(bounds)`:
  - получает candidates из затронутых cells;
  - проверяет фактическое пересечение foot ellipse body с query AABB;
  - возвращает IDs по возрастанию.
- `QuerySegment(start,end,radius)` на этом этапе является broad-phase запросом для будущих projectiles/melee:
  - строит AABB сегмента, расширенный на query radius и максимальный X/Y extent dynamic body;
  - возвращает дедуплицированный отсортированный список потенциальных целей;
  - точный segment-vs-circle hit и выбор первой цели относятся к combat phase, а не к grid.
- Все query methods принимают caller-owned `dst []uint32` и scratch; возвращаемый slice валиден только до следующего использования того же scratch.
- Сейчас queries покрыть тестами, но не подключать к attack gameplay до реализации отдельного server-side damage pipeline.
- Dynamic Grid не используется внутри `MoveFootprint`: игроки проходят друг сквозь друга, не останавливают, не выталкивают и не меняют позиции друг друга.
- Не передавать grid по сети: клиент уже получает позиции игроков существующим binary protocol и при необходимости строит собственный presentation/query index.

### Порядок simulation tick

```text
1. Зафиксировать номер tick и принять gameplay/input commands
2. Сформировать ordered StructureMutation batch для этого effective tick
3. Проверить clearance для colliders, переходящих в solid-state
4. Атомарно применить допустимый batch к StructureGrid и повысить revision
5. Параллельно рассчитать player movement против MapStaticGrid + StructureGrid
6. Дождаться завершения всех movement workers
7. Перестроить DynamicGrid.back из итоговых player positions
8. Поменять DynamicGrid.front и DynamicGrid.back местами
9. В будущем: выполнить combat queries по новому front snapshot
10. Собрать state/collision deltas и выполнить replication
```

- На текущем этапе пункт 9 пустой, но граница фаз должна существовать сразу.
- Clearance шага 3 читает предыдущий завершённый `DynamicGrid.front`. Это сознательно означает, что collider активируется до movement текущего tick только тогда, когда его footprint был свободен в конце предыдущего tick.
- После шага 4 StructureGrid не меняется до окончания всех movement workers. Structure event, пришедший позже cut-off текущего tick, получает `effectiveTick = currentTick + 1`.
- При реализации combat нельзя выполнять hit detection внутри текущего per-player movement worker: все атаки одного tick должны видеть один согласованный post-movement Dynamic Grid snapshot.
- Результат не должен зависеть от порядка обхода `playersMap`; перед rebuild использовать уже собранный snapshot игроков или стабильную сортировку по `playerId`.

### Будущие movable objects

- Добавлять телеги, тараны и другие действительно movable bodies только в Dynamic Grid, не перестраивая MapStaticGrid или StructureGrid.
- Хранить для записи dynamic body его ID, тип (`player`/`movable_object`), shape и итоговую authoritative transform текущего tick.
- Само движение movable object рассчитывать отдельной системой; столкновение игрока не передаёт объекту impulse и никогда не двигает его неявно.
- Когда movable objects начнут блокировать игроков, movement phase читает их immutable позиции из `DynamicGrid.front`, а новые позиции публикуются только при следующем swap. Это сохраняет отсутствие data races и единый snapshot на tick. Обычные неподвижные постройки при этом остаются в StructureGrid.

### Player movement state

- Разделить movement intent и фактическое движение:
  - `DesiredDX/DesiredDY` сохраняют удерживаемый input;
  - реплицируемые `VX/VY` отражают реально выполненное движение после collision resolution.
- Каждый tick повторно пытаться двигаться по desired vector, даже если предыдущий tick был полностью заблокирован.
- При полном блокировании выставлять фактические `VX/VY = 0`; при sliding передавать только разрешённую ось.
- Sprint stamina списывать только если игрок действительно переместился; упор в стену stamina не расходует.
- Любое изменение фактической velocity автоматически попадает в существующую delta replication и корректирует remote prediction.
- Другие игроки не участвуют в movement query и никогда не получают displacement/impulse.

Интеграция в текущий `Player` и replication:

- добавить атомарные `DesiredVX`/`DesiredVY`; существующие `VX`/`VY` оставить фактической реплицируемой velocity;
- при новом accepted movement input обновлять desired vector и desired sprint flag;
- `updatePlayerPosition` читает desired vector, рассчитывает distance/remainder, вызывает `MoveFootprint`, затем записывает итоговые X/Y и effective VX/VY;
- movement ACK содержит фактическую позицию после collision;
- если input не менялся, desired vector сохраняется между ticks независимо от effective velocity;
- sprint stamina списывается только после результата solver и только если итоговая позиция отличается от начальной;
- при block/attack state desired input сохраняется, но solver получает нулевой разрешённый vector; после окончания состояния удерживаемое направление снова применяется по существующим input rules;
- `classifyDelta` продолжает предсказывать по фактическим `VX/VY`: вход в collision или смена sliding axis отправляет delta, а стоящий у стены игрок с `VX/VY=0` не создаёт correction каждый tick;
- служебные данные collision (`BlockedX`, число candidates/checks) не входят в wire state и сами по себе не помечают игрока changed.

### Клиент и API

- Добавить public `GET /api/map-colliders` для immutable collision snapshot активной кампании:

```json
{
  "campaignId": 42,
  "generatorVersion": "mapgen-v1",
  "mapVersion": "sha256-<hex>",
  "cellSize": 64,
  "colliders": [
    {
      "colliderId": 1,
      "minX": 3300,
      "minY": 700,
      "maxX": 3340,
      "maxY": 1800,
      "renderKind": "stone_wall"
    }
  ]
}
```

- Ответ формируется из того же immutable slice, из которого построен серверный MapStaticGrid; повторный запрос не обращается к PostgreSQL.
- Добавить `ETag` со значением mapVersion и поддержать `If-None-Match`/`304 Not Modified`. Snapshot можно кэшировать по `(campaignId,mapVersion)` до конца кампании.
- После WebSocket handshake, но до появления игрока в мире, сервер отправляет полный structure collision snapshot:

```json
{
  "type": "structure_collision_snapshot",
  "campaignId": 42,
  "mapVersion": "sha256-<hex>",
  "revision": 815,
  "structures": [
    {
      "structureId": 9001,
      "structureKind": "wood_wall",
      "state": "completed",
      "solid": true,
      "colliders": [
        { "partId": 1, "minX": 3300, "minY": 700, "maxX": 3340, "maxY": 1800 }
      ]
    }
  ]
}
```

- Последующие изменения приходят отдельными reliable ordered messages:

```json
{
  "type": "structure_collision_delta",
  "campaignId": 42,
  "revision": 816,
  "effectiveTick": 120044,
  "operation": "upsert|remove|set_solid",
  "structureId": 9001,
  "colliders": []
}
```

- Реальная wire-реализация остаётся binary, JSON выше задаёт логическую schema. Для structure snapshot/delta потребуются новые protocol message types; прежнее правило «не менять binary protocol» больше неприменимо из-за runtime-построек.
- Snapshot и последующие deltas ставятся в outgoing queue соединения одной game-loop фазой: сначала snapshot с revision `R`, затем только deltas `R+1...`. Это закрывает race, когда постройка меняется во время подключения клиента.
- Клиент применяет deltas строго по revision. Duplicate revision игнорируется; gap, несовпавший campaignId/mapVersion или delta для неизвестной сущности запускает запрос полного structure resync и временно отключает local prediction, но не разрывает session автоматически.
- Resync выполняется сообщениями `structure_collision_resync_request(lastRevision)` → новый `structure_collision_snapshot`; отдельный HTTP-запрос к PostgreSQL не нужен.
- Delta ставится в очередь и применяется перед prediction указанного `effectiveTick`. Если она пришла после этого tick, клиент применяет её немедленно и принимает последующую authoritative reconciliation.
- Загружать map snapshot до выбора юнита и WebSocket connection; structure snapshot получить до spawn. При ошибке показывать loading error и не начинать session.
- Валидировать на клиенте `cellSize`, уникальность IDs, revisions, bounds и shape каждого collider; `radiusX/radiusY` каждого unit type валидировать в общей unit configuration. Невалидный payload считается synchronization error.
- Построить на клиенте такие же MapStaticGrid и StructureGrid и выполнить тот же integer X-then-Y solver в local prediction, reconciliation replay и remote dead reckoning.
- Вынести movement/collision solver в чистый TypeScript-модуль без Pixi dependencies. Rendering получает уже готовые collider records и не участвует в расчёте.
- Client prediction использует собственный desired input; server state `VX/VY` используется только для remote players. Authoritative ACK/state всегда может скорректировать local player.
- При reconciliation установить server position и server remainder, затем переиграть pending local movement через тот же collision solver. Нельзя сначала проигрывать движение без collision, а затем clamp конечную точку.
- Remote dead reckoning применяет MapStaticGrid и актуальную revision StructureGrid на каждом симулируемом tick. Когда сервер передаёт `VX/VY=0` у стены, remote entity остаётся в контакте и не накапливает drift.
- Отрисовать тестовые стены через Pixi `Graphics` как простые stone-colored rectangles; это debug-представление ground collider, а не окончательный visual bounds объекта.
- Production sprite игрока привязать к `groundPoint` между ступнями; ellipse находится у ног, тогда как голова/торс/оружие могут визуально выступать за него. Объекты и игроки сортируются по ground baseline согласно `viewport_camera_plan.md`, а не по центру texture или collision AABB.
- Render map objects и structures раздельно: map snapshot создаёт постоянные display objects, structure snapshot/deltas создают, обновляют или удаляют runtime display objects.
- Добавить development overlay с layer (`map|structure|dynamic`), ID, AABB, revision и подсветкой затронутых cells; overlay выключен по умолчанию и не входит в production bundle behavior.
- Player movement messages не меняются: итоговая позиция и фактические `VX/VY` передаются существующим способом.

### Наблюдаемость и документация

- Добавить metrics:
  - `game_collision_move_duration_seconds` — histogram суммарного времени map+structure movement queries одного tick;
  - `game_collision_candidates_total{layer="map|structure"}` — counter broad-phase candidates;
  - `game_collision_narrow_checks_total` — counter ellipse-vs-AABB checks;
  - `game_collision_contacts_total{axis="x|y|bounds"}` — counter контактов;
  - `game_collision_blocked_players` — gauge игроков, закончивших tick с заблокированной осью;
  - `game_map_grid_max_bucket_size` — gauge после startup build;
  - `game_structure_grid_max_bucket_size` — gauge после mutation batch;
  - `game_structure_grid_mutation_duration_seconds` — histogram mutation phase;
  - `game_structure_collision_revision` — gauge опубликованной revision;
  - `game_structure_collision_resync_total{reason}` — counter client resync requests;
  - `game_dynamic_grid_rebuild_duration_seconds` — histogram rebuild;
  - `game_dynamic_grid_max_bucket_size` — gauge после каждого rebuild;
  - `game_collision_spawn_relocations_total` и `game_collision_spawn_failures_total`.
- Не допускать allocations на игрока в tick hot path; candidate buffers принадлежат tick workers и переиспользуются.
- Логировать campaign ID, количество map/structure colliders, grid dimensions, mapVersion, structure revision и максимальную заполненность cell при запуске.
- При `dynamic_grid_max_bucket_size > 256` писать rate-limited warning не чаще одного раза в 30 секунд. Высокая плотность не влияет на ghost movement, но станет hotspot для combat queries.
- Performance complexity:

```text
Map build:          O(total map collider→cell memberships)
Structure restore:  O(total solid structure collider→cell memberships)
Structure mutation: O(cells touched × local bucket shift), только по событиям
Player movement:    O(map+structure visited cells + distance × local candidates), distance <= 64
Dynamic clear:      O(previously touched cells), не O(250000)
Dynamic rebuild:    O(players × cells touched by player footprint), обычно 1–4 cells
Dynamic query:      O(visited cells + local bodies), worst case O(all players in one cell)
```

- Не ограничивать результаты spatial query ради производительности: grid возвращает всех кандидатов, а будущий combat layer отдельно определяет max targets и правила cleave/AoE.
- После реализации обновить README/GDD: map/structure collisions реализованы, runtime structures синхронизируются по revision, player-player movement collision отключён осознанно.

## Test Plan

- MapStaticGrid construction tests: rows/columns для делящихся и неделящихся размеров мира, flat index, объект в одной/нескольких cells, объект на границе cell, deterministic ordering и startup limits.
- MapStaticGrid query tests: bounds за краем мира, collider в нескольких cells, пограничное касание и отсутствие duplicate results.
- StructureGrid tests: restore, upsert/remove/set-solid, составная постройка, изменение только затронутых cells, monotonically increasing revision, duplicate/gap rejection и отсутствие частичных изменений при невалидном batch.
- Structure lifecycle tests: blueprint/under-construction не solid, completed/damaged solid, ruins passable, rebuild activation, ворота open/closed и `CompletionPendingClearance` при игроке внутри footprint.
- Narrow-phase tests: ellipse далеко от AABB, касается стороны/угла, пересекает сторону/угол, центр внутри AABB; отдельные `radiusX/radiusY`, равенство как допустимое касание и отсутствие integer overflow.
- Movement tests: прямое столкновение со всех четырёх сторон, X/Y sliding, внутренний и внешний угол, L-shaped corner, несколько стен, движение от поверхности наружу.
- Anti-tunneling tests: стены толщиной `1`, `2` и `40 world units`; distance `1`, текущий sprint максимум `26` после округления и hard limit `64`; обычное, diagonal и sprint движение не заканчивается на противоположной стороне стены.
- Dynamic Grid tests: rebuild/swap, очистка только `touchedCells` старого back snapshot, отсутствие duplicate touched indices, переход игрока между cells, ellipse на границе нескольких cells, дедупликация ID и корректность `QueryCircle`/`QueryAABB`/`QuerySegment`.
- Movement-state tests: desired input сохраняется при effective velocity 0, sliding velocity отражает разрешённую ось, ACK содержит collision position, blocked sprint не расходует stamina.
- Boundary tests: четыре стороны мира, diagonal movement в world corner, допустимые центры `(radiusX,radiusY)` и `(width−radiusX,height−radiusY)` для каждого unit footprint.
- Spawn tests: занятый origin перемещается в ближайшую свободную integer position; отсутствие свободного места в радиусе 128 возвращает контролируемую ошибку.
- Determinism tests: одинаковые input/map/structure snapshots дают идентичные координаты; порядок SQL rows и Go map iteration не влияет на результат.
- Client/server parity: общий набор golden movement scenarios выполняется Go- и TypeScript-тестами с одинаковыми expected positions.
- Multiplayer test: пересечение двух игроков разрешено, движение одного никогда не меняет второго; оба после tick присутствуют в Dynamic Grid в итоговых позициях.
- API/protocol tests: canonical map hash, ETag/304, initial structure snapshot, ordered deltas, late delta, duplicate/gap revision, resync и campaign/map-version mismatch.
- Performance benchmarks: 1700 движущихся игроков на типичной карте с map+structure colliders, 1700 игроков вдоль одной стены, 12 000 игроков без nearby colliders, mutation большого compound castle и 12 000 dynamic bodies в одной cell. Movement hot path должен иметь `0 allocs/op`, типичный тест на 1700 игроков — укладываться в 5 ms на reference development machine.
- Полный набор запускать через `go test ./...`, `go test -race ./...`, TypeScript tests, client build и существующие protocol probes.

## Порядок реализации

1. Добавить geometry types, integer ellipse-vs-AABB и unit tests.
2. Реализовать общую spatial-grid адресацию, MapStaticGrid build/query и reusable dedup scratch.
3. Добавить campaign map schema/snapshot loader, validation, canonical map version и `/api/map-colliders`.
4. Реализовать StructureGrid, mutation batch/revision, persistence restore и три completed test walls.
5. Реализовать `MoveFootprint` против map+structure layers, micro-step anti-tunneling, sliding, раздельные X/Y world bounds и spawn relocation.
6. Разделить desired/effective movement state и подключить solver к parallel tick workers.
7. Реализовать DynamicGrid front/back, deterministic post-movement rebuild, clearance query и dynamic query APIs.
8. Добавить structure lifecycle mapping и tick-boundary mutation phase; подключение полноценной building gameplay может использовать этот контракт позже.
9. Реализовать map HTTP snapshot и binary structure snapshot/delta/resync messages.
10. Реализовать TypeScript MapStaticGrid/StructureGrid/solver, revision queue, prediction, reconciliation и golden parity tests.
11. Добавить Pixi rendering map/structure layers, тестовых стен и development collision overlay.
12. Добавить metrics, benchmarks, race tests и protocol regression run.
13. Обновить README и GDD после прохождения acceptance criteria.

## Assumptions

- Player coordinates обозначают ground point между ступнями и центр foot ellipse.
- `radiusX/radiusY` задаются по unit type, не зависят от animation/facing и не меняются у существующего игрока при live-update unit stats без controlled respawn/revalidation.
- Стены не вращаются; сложный объект составляется из нескольких AABB.
- Объекты MapStaticGrid нельзя создавать, удалять или двигать до конца кампании.
- Постройки не перемещаются, но их collider parts могут event-driven появляться, исчезать и менять solid-state через StructureGrid mutation phase.
- Dynamic Grid входит в первую реализацию, но используется только как spatial index; movement остаётся ghost-mode.
- Combat hit detection будет читать post-movement `DynamicGrid.front`, поэтому все атаки одного tick видят согласованный snapshot.
- Movable objects в будущем попадут в существующий DynamicGrid; MapStaticGrid и StructureGrid для этого перестраивать не потребуется.
