# План исправления world scale, viewport, камеры и размера персонажей

## Статус документа

Документ описывает замену текущего подхода «весь мир растягивается на экран» на ортографическую 2D-камеру с фиксированным игровым обзором. План учитывает world `32000×32000 units` (`3.2×3.2 км`), server-authoritative движение, client prediction, коллизии, текущие PixiJS sprite sheets и требование одинакового объёма видимой игровой информации на разных разрешениях.

Это план реализации, а не описание уже готовой функциональности.

## Диагноз текущей реализации

Сейчас `CoordinateConverter` рассчитывает независимые коэффициенты:

```text
scaleX = screenWidth  / worldWidth
scaleY = screenHeight / worldHeight

screenX = worldX × scaleX
screenY = worldY × scaleY
```

Для мира `32000×32000` и экрана `1920×1080`:

```text
scaleX = 1920 / 32000 = 0.06 px/world unit
scaleY = 1080 / 32000 = 0.03375 px/world unit
```

Из этого следуют архитектурные ошибки:

- весь мир 3.2×3.2 км всегда помещается на одном экране;
- X и Y имеют разный масштаб, поэтому круги визуально становятся эллипсами, а квадратные объекты — прямоугольниками;
- изменение размера окна меняет экранную скорость игрока и визуальный размер метра;
- position игроков переводится через world→screen, но sprite scale задаётся отдельно как фиксированный экранный размер;
- `TARGET_ONSCREEN_SIZE = 64` не связан ни с метрами, ни с world units;
- один и тот же персонаж имеет разный физический размер относительно карты на разных экранах;
- `StructureRenderer` умножает размеры стен на независимые `scale.x/scale.y` и наследует искажение;
- debug collider круга принудительно увеличивается минимум до 6 px, поэтому overlay не соответствует визуальному масштабу;
- resize пересчитывает положение каждой entity, хотя изменение экрана не должно менять её world transform;
- mouse input хранится в координатах DOM/canvas без отдельного преобразования через letterbox и camera;
- client attack message содержит screen-space floats, хотя сервер их сейчас игнорирует;
- `MessageViewportUpdate` распознаётся сервером, но viewport data не используется.

Server simulation при этом не зависит от разрешения и продолжает корректно считать координаты в world units. Проблема находится в presentation layer. Однако честное отображение метров выявит отдельную balance-проблему: текущие `22.6–33.4 м/с` действительно означают физические метры в секунду и после исправления камеры будут выглядеть чрезмерно быстро.

## Цели

- Мир остаётся `32000×32000 world units`, но камера показывает только локальную область вокруг игрока.
- `1 м = 10 world units` во всех simulation/collision расчётах.
- Визуальная высота тела персонажа равна `1.8 м = 18 world units`.
- Authoritative player position обозначает ground point между ногами, а не геометрический центр sprite.
- Movement collider является axis-aligned овалом/ellipse у ног: по ширине покрывает полную видимую ширину персонажа в базовом neutral/movement sprite с небольшим padding, по глубине покрывает только место на земле.
- Sprite и объекты корректно перекрывают друг друга через Y-sorting по ground baseline.
- X и Y всегда используют один uniform scale; геометрия не растягивается.
- Все клиенты видят одинаковую область мира независимо от разрешения и aspect ratio.
- Более широкий монитор не даёт дополнительного обзора.
- Более высокое разрешение даёт только более чёткую картинку, но не больше world information.
- Resize, DPR и browser zoom не меняют camera world bounds.
- Client prediction/interpolation продолжают работать в world units и не зависят от Pixi pixels.
- Камера следует за плавной render-position локального игрока, а не за дискретной server position.
- Рендер не обходит и не обновляет объекты всей карты, если они вне камеры.
- Collision geometry, sprite visuals, HUD и input используют явно разделённые coordinate spaces.

## Не цели первой версии

- Свободный gameplay zoom колесом мыши.
- Поворот камеры.
- Истинная isometric coordinate projection с поворотом world axes. Текущие assets трактуются как pseudo-isometric/three-quarter art внутри обычной ортографической top-down world plane.
- Server-side AOI и замена текущей all-to-all replication в том же изменении.
- Камера spectator/replay/death mode.
- Разный физический рост отдельных классов.
- Автоматическое изменение collider radius из визуального роста.

## Зафиксированные coordinate spaces

Нужно явно разделить пять пространств:

```text
Server World Space
  integer world units, 10 units = 1 meter
            ↓
Render World Space
  float world units для interpolation, без screen scaling
            ↓ Camera2D
Reference View Space
  1280×720 logical pixels, одинаковый обзор для всех
            ↓ PresentationViewport
Canvas CSS Space
  фактический размер canvas в CSS pixels + letterbox
            ↓ renderer resolution / DPR
Device Pixel Space
  физический framebuffer, влияет только на чёткость
```

Simulation никогда не получает reference/CSS/device pixels. UI никогда не должен менять authoritative world coordinates.

## Базовые размеры и точный расчёт

### World scale

```text
WORLD_UNITS_PER_METER = 10
PLAYER_VISUAL_HEIGHT_METERS = 1.8
PLAYER_VISUAL_HEIGHT_WORLD_UNITS = 1.8 × 10 = 18
```

Movement collider переносится с круга в axis-aligned ellipse у ног:

```text
radiusX = половина полной видимой ширины базового character sprite + padding
radiusY = половина глубины, занимаемой ногами на ground plane
```

`1.8 м` описывает визуальную высоту billboard-спрайта, а ellipse — занимаемое им место на земле. Collider не покрывает голову, торс, плащ, копьё, меч или attack effect.

Мир и камера при этом сохраняют uniform X/Y scale. Овал получается из собственной формы footprint, а не из сжатия всей вертикальной оси: world circle обязан продолжать выглядеть кругом, квадрат — квадратом.

### Reference viewport и zoom

Первая версия использует фиксированный reference viewport:

```text
REFERENCE_WIDTH  = 1280 logical px
REFERENCE_HEIGHT = 720 logical px
REFERENCE_ASPECT = 16:9
PIXELS_PER_METER = 32 logical px/m
PIXELS_PER_WORLD_UNIT = 32 / 10 = 3.2 logical px/unit
GAMEPLAY_ZOOM = 1.0, пользователь не может его уменьшить
```

Камера показывает:

```text
visibleWorldWidth  = 1280 / 3.2 = 400 world units = 40 м
visibleWorldHeight = 720  / 3.2 = 225 world units = 22.5 м
```

Персонаж ростом 1.8 м занимает:

```text
18 world units × 3.2 px/unit = 57.6 logical px
```

Размер movement ellipse определяется visual metrics конкретного unit и фиксируется в server unit data. Для ориентира footprint шириной `0.8 м` и глубиной `0.6 м` занимает:

```text
width  = 0.8 × 32 = 25.6 logical px
depth  = 0.6 × 32 = 19.2 logical px
radiusX = 4 world units
radiusY = 3 world units
```

Authoritative collision использует целые world units. При текущем разрешении `1 world unit = 3.2 logical px`, поэтому размер после asset calibration округляется наружу до ближайшего world unit. Если нужен padding примерно `1–2 logical px`, ближайший безопасный authoritative padding равен `1 world unit = 0.1 м = 3.2 logical px`; повышать coordinate resolution только ради разницы в один экранный пиксель не нужно.

Примеры gameplay distances:

| World distance | World units | Reference pixels |
|---|---:|---:|
| Player visual height | 18 | 57.6 px |
| Пример player footprint width | 8 | 25.6 px |
| Пример player footprint depth | 6 | 19.2 px |
| Melee range 0.9 м | 9 | 28.8 px |
| Spear range 1.7 м | 17 | 54.4 px |
| Archer range 9 м | 90 | 288 px |
| Spatial Grid cell 6.4 м | 64 | 204.8 px |

`PIXELS_PER_METER` является camera zoom, а не частью simulation. Его можно изменить после playtest, но одновременно для всех клиентов и без изменения world coordinates, коллизий или скорости в м/с.

## Политика одинакового обзора и разрешений

Нельзя одновременно заполнить любой aspect ratio, сохранить пропорции и показать одинаковую область мира. Поэтому используется fixed 16:9 gameplay viewport с letterbox/pillarbox.

Для фактического canvas `screenWidth×screenHeight`:

```text
presentationScale = min(screenWidth / 1280, screenHeight / 720)

viewportPixelWidth  = 1280 × presentationScale
viewportPixelHeight = 720  × presentationScale

offsetX = (screenWidth  - viewportPixelWidth)  / 2
offsetY = (screenHeight - viewportPixelHeight) / 2
```

Примеры:

- `1920×1080`: scale `1.5`, bars отсутствуют;
- `2560×1080`: scale `1.5`, pillarbox по `320 px` слева и справа, дополнительный мир не показывается;
- `1024×768`: scale `0.8`, letterbox по `96 px` сверху и снизу;
- `3840×2160`: scale `3`, видимая world-area остаётся 40×22.5 м, растёт только детализация.

Правила:

- gameplay world и HUD находятся внутри маски reference viewport;
- pointer events за пределами viewport игнорируются;
- bars заполняются нейтральным цветом/декоративной рамкой и не содержат world information;
- minimap, если появится, имеет одинаковый logical размер и данные для всех игроков;
- browser DPR используется только как renderer resolution и не участвует в camera math;
- UI scale/accessibility может масштабировать текст и controls, но не world viewport.

## Новая Pixi scene graph

```text
app.stage                              // actual canvas CSS space
├── canvasBackground                   // letterbox/pillarbox color
└── presentationRoot                   // offsetX/offsetY + presentationScale
    ├── gameplayMask                   // 0,0,1280,720
    ├── gameViewport                   // masked reference 1280×720
    │   ├── worldRoot                  // Camera2D transform
    │   │   ├── terrain/map chunks
    │   │   ├── structure chunks
    │   │   ├── ground effects/projectiles
    │   │   ├── player entity roots
    │   │   └── world debug geometry
    │   ├── entityOverlayRoot          // status bars/nameplates in logical px
    │   └── hudRoot                    // fixed reference-space UI
    └── optionalViewportFrame
```

`worldRoot` локальные координаты равны world units. Entity больше не получает screen position от converter:

```text
playerEntity.position.x = interpolatedWorldX
playerEntity.position.y = interpolatedWorldY
```

Camera transform задаётся один раз на общем container:

```text
worldRoot.pivot.set(cameraCenterX, cameraCenterY)
worldRoot.position.set(640, 360)
worldRoot.scale.set(3.2 × zoom)
```

Это автоматически одинаково преобразует terrain, стены, игроков, эффекты и collision overlay. Не нужно пересчитывать position каждой entity при resize или движении камеры.

## Camera2D

### Первая версия

- Камера имеет фиксированный zoom `1.0`.
- Target — интерполированная render world-position локального игрока.
- Камера следует за target без spring/dead-zone в первой реализации.
- Обновление выполняется каждый render frame после `MovementController.render(alpha)`.
- Server/prediction position не меняется от камеры.

Мгновенное следование за уже интерполированной позицией не создаёт tick jitter и не добавляет camera lag к управлению. Smoothing можно добавить позже как чистый presentation effect.

### Clamp у границ карты

```text
halfWidthWorld  = 400 / 2 = 200 units
halfHeightWorld = 225 / 2 = 112.5 units

cameraX = clamp(targetX, 200, worldWidth  - 200)
cameraY = clamp(targetY, 112.5, worldHeight - 112.5)
```

Camera center может быть float. На краю мира игрок смещается от центра экрана, а за world bounds не показывается пустая область.

Если playable world по одной оси меньше viewport, камера центрирует эту ось и показывает одинаковые поля по обе стороны.

### Camera API

```ts
interface CameraConfig {
    referenceWidth: 1280;
    referenceHeight: 720;
    pixelsPerWorldUnit: 3.2;
    minZoom: 1;
    maxZoom: 1;
}

class Camera2D {
    setTargetWorld(x: number, y: number): void;
    update(): void;
    worldToView(x: number, y: number): Point;
    viewToWorld(x: number, y: number): Point;
    visibleWorldBounds(marginWorldUnits?: number): AABB;
}
```

`worldToView/viewToWorld` нужны input, screen-space overlays и tests. Обычные world entities используют transform `worldRoot`, а не вызывают conversion каждый frame.

## PresentationViewport

Отдельный класс отвечает только за actual screen ↔ reference view:

```ts
class PresentationViewport {
    resize(screenWidth: number, screenHeight: number, dpr: number): void;
    clientToView(clientX: number, clientY: number, canvasRect: DOMRect): Point | null;
    viewToClient(viewX: number, viewY: number): Point;
    containsClientPoint(clientX: number, clientY: number): boolean;
}
```

На resize меняются только:

- `presentationRoot.position`;
- `presentationRoot.scale`;
- canvas/background;
- renderer resolution при изменении DPR;
- HUD layout внутри фиксированных `1280×720`, если оно использует anchors.

World positions, snapshot buffers, prediction state и sprite world scale не пересчитываются.

## Размер и anchor персонажа

### Проблема текущих assets

Текущие обычные movement sheets имеют cells `16×16 px`; Heavy Knight и Paladin используют cells `24×24 px`. Реальное непрозрачное тело внутри cell занимает примерно `10–16 source pixels` по высоте. Сейчас `recommendedScale()` приводит effective cell к `64 screen pixels`, а не тело к физической высоте.

Нельзя пересчитывать scale по trim bounds каждого animation frame: idle/run/attack имеют разный силуэт, оружие выходит за тело, и персонаж начнёт менять размер между кадрами.

### Visual metadata

Добавить клиентский manifest для каждого visual set:

```ts
interface CharacterVisualMetrics {
    visualHeightWorldUnits: 18;
    referenceBodyHeightPx: number;
    referenceBodyWidthPx: number;
    groundAnchorPx: { x: number; y: number };
    collisionPaddingSourcePx: 1 | 2;
    footprintDepthWorldUnits: number;
    sourceCellSizePx: 16 | 24 | 64;
}
```

- `referenceBodyHeightPx` измеряется один раз по выбранному neutral idle frame без оружия/effect;
- `referenceBodyWidthPx` измеряет полную видимую ширину персонажа в том же neutral/movement frame; прозрачные поля cell, отдельно выступающее длинное оружие, cape flare и attack trail не входят;
- `groundAnchorPx` указывает точку контакта ног с землёй в source cell;
- `collisionPaddingSourcePx` добавляет 1–2 source pixels с каждой стороны body silhouette до округления в world units;
- `footprintDepthWorldUnits` задаёт глубину между передней/задней границей области ног на земле и не выводится из визуальной высоты;
- одна метрика применяется ко всем movement/combat frames этого visual set;
- attack weapon/effect может выходить за 1.8 м и не влияет на body scale;
- manifest валидируется при загрузке assets;
- fallback sprite получает собственную явную запись.

Sprite scale в world space:

```text
sourcePixelToWorldUnit = 18 / referenceBodyHeightPx

sprite.scale.x = ±sourcePixelToWorldUnit
sprite.scale.y =  sourcePixelToWorldUnit
```

Например, при body height `16 source px`:

```text
world sprite scale = 18 / 16 = 1.125 world units/source px
reference screen body height = 16 × 1.125 × 3.2 = 57.6 px
```

При body height `13 source px`:

```text
world sprite scale = 18 / 13 ≈ 1.384615
reference screen body height = 13 × 1.384615 × 3.2 = 57.6 px
```

Предварительный `radiusX` можно получить из reference sprite:

```text
sourcePixelToWorldUnit = 18 / referenceBodyHeightPx
visualBodyWidthWorld = referenceBodyWidthPx × sourcePixelToWorldUnit
paddingWorld = collisionPaddingSourcePx × sourcePixelToWorldUnit
radiusXWorldUnits = ceil(visualBodyWidthWorld / 2 + paddingWorld)
```

После вычисления значение вручную проверяется на всех idle/walk/run направлениях и фиксируется в authoritative unit data. Оно не пересчитывается в runtime от текущего animation frame, иначе collider пульсировал бы при ходьбе и менялся бы из-за оружия.

`radiusYWorldUnits` задаётся отдельно как половина ground depth и обычно меньше `radiusX`. Оба радиуса целые, больше нуля и не зависят от camera zoom/resolution.

### PlayerView root

Создать стабильный entity root:

```text
PlayerView(Container) at authoritative/interpolated ground position
├── shadow/selection ellipse centered at (0,0)
└── AnimatedSprite child with visual offset and scale
```

- Root position — центр movement ellipse между ногами; прежнюю формулировку «центр всего sprite/circle» больше не использовать.
- AnimatedSprite больше не является самой entity transform.
- Child sprite offset вычисляется из `groundAnchorPx`.
- Смена texture/animation не меняет root position.
- Left/right flip меняет только знак child `scale.x` и корректирует anchor/offset, но не entity root.
- Status bar не становится child `worldRoot`: он живёт в `entityOverlayRoot` и получает `Camera2D.worldToView(headWorldPosition)`.

Sprite child использует `anchor.set(0)` и располагается так, чтобы source ground anchor всегда совпадал с `(0,0)` root:

```text
sprite.x = -groundAnchorPx.x × sourcePixelToWorldUnit
sprite.y = -groundAnchorPx.y × sourcePixelToWorldUnit
```

Для texture/anchor API Pixi конкретная формула может быть выражена через pivot, но invariant остаётся тем же: смена кадра не двигает ground point.

Это устраняет прыжки pivot между 16px и 24px assets и отделяет gameplay point от прозрачных полей texture.

## Изменение movement collision: circle → foot ellipse

Текущая реализованная система использует один `PlayerRadius = 4` и `circle-vs-AABB`. После утверждения foot footprint её необходимо мигрировать на per-unit axis-aligned ellipse. Это изменение затрагивает collision plan и должно быть выполнено до визуальной приёмки камеры, иначе sprite будет стоять ногами в одном месте, а физически сталкиваться невидимым кругом вокруг старого центра.

Authoritative данные:

```go
type PlayerFootprint struct {
    RadiusX int32 // половина ширины тела/ног на ground plane
    RadiusY int32 // половина глубины footprint
}
```

- Footprint выбирается по unit type и не меняется при animation/facing.
- Игроки по-прежнему не блокируют и не толкают других игроков.
- Footprint используется только для player-vs-map/structure movement и spawn clearance.
- Combat hurtbox/hitbox остаются отдельными shapes.

Ellipse-vs-AABB narrow phase без float:

```text
closestX = clamp(centerX, box.minX, box.maxX)
closestY = clamp(centerY, box.minY, box.maxY)
dx = centerX - closestX
dy = centerY - closestY

overlap = dx² × radiusY² + dy² × radiusX²
          < radiusX² × radiusY²
```

Все произведения считать в `int64`. Равенство означает допустимое касание без проникновения, как и в текущем solver.

Broad phase и world bounds:

```text
sweptMinX = min(startX,endX) - radiusX
sweptMaxX = max(startX,endX) + radiusX
sweptMinY = min(startY,endY) - radiusY
sweptMaxY = max(startY,endY) + radiusY

centerX ∈ [radiusX, worldWidth  - radiusX]
centerY ∈ [radiusY, worldHeight - radiusY]
```

Micro-step anti-tunneling и X→Y sliding сохраняются без изменения. Меняется только exact overlap predicate и раздельное расширение swept bounds по осям.

Переименовать API по смыслу:

```text
MoveCircle       → MoveFootprint
FindNearestFree  → FindNearestFreeFootprint
PlayerRadius     → PlayerFootprint per unit
```

Server и TypeScript получают footprint из одной unit definition. Movement ACK не требует новых полей, потому что unit type уже известен обеим сторонам. При live-update footprint существующего unit запрещено менять для уже подключённых игроков без controlled respawn/revalidation.

Визуальный selection/shadow ellipse использует те же `radiusX/radiusY` для совпадения с collision. Collision debug overlay дополнительно может рисовать swept bounds и contact point.

## Ground footprint и порядок перекрытия спрайтов

Collision и визуальное перекрытие решают разные задачи и не должны выводиться друг из друга:

- `groundPoint = (worldX, worldY)` — authoritative точка между ступнями игрока;
- `movementFootprint` — ellipse на земле с центром в `groundPoint`;
- `visualBounds` — полный billboard-спрайт от ног до головы, который не участвует в movement collision;
- `sortBaselineY` — линия глубины, по которой определяется, кто рисуется последним.

Для игрока `sortBaselineY = groundPoint.y`. Для компактного объекта `sortBaselineY` задаётся нижней границей его ground footprint, а не нижним пикселем текстуры:

```text
player.sortBaselineY = player.groundPoint.y
object.sortBaselineY = object.groundFootprint.maxY

меньший sortBaselineY → рисуется раньше/сзади
больший sortBaselineY → рисуется позже/спереди
```

В результате:

- игрок ниже камня, дерева или постройки перекрывает их sprite нижней частью своего sprite;
- игрок выше их baseline оказывается визуально за объектом;
- голова, плечи или оружие могут визуально заходить на объект до физического контакта — это ожидаемо и не означает collision;
- физический контакт наступает только тогда, когда ellipse ног касается ground footprint объекта.

Pixi scene graph:

```text
worldRoot (sortableChildren = true)
├── ground/map layer
├── sortable object roots
├── sortable player roots
└── effects with explicitly authored depth policy
```

Не полагаться на случайный порядок добавления children. Сортировка должна быть детерминированной tuple:

```text
(sortBaselineY, depthLayer, stableEntityId)
```

`depthLayer` нужен только для равных baseline: например, ground decal < structure/player < foreground effect. `stableEntityId` устраняет мерцание при равенстве Y. Если Pixi `zIndex` недостаточно для полного tuple, передавать заранее вычисленный стабильный integer sort key или использовать собственный comparator; ID нельзя превращать в потенциально неточный огромный float.

У объектов обязаны быть раздельные authored-данные:

```ts
interface WorldObjectVisual {
  groundPoint: WorldPoint;
  groundFootprints: ReadonlyArray<WorldAABB>;
  sortBaselineY: number;
  visualBounds: WorldAABB;
  depthLayer: number;
}
```

- `groundFootprints` используются collision solver и spatial grid;
- `visualBounds` используются render culling и не блокируют движение;
- `sortBaselineY` используется только renderer;
- прозрачные поля texture не меняют ни footprint, ни baseline.

Один высокий компактный объект может иметь один root и один baseline. Длинную стену, ворота или крупное здание нельзя всегда сортировать как один гигантский sprite: их visual необходимо разбивать на depth-сегменты/тайлы с собственными baseline либо выделять foreground-части. Иначе игрок на одном конце стены будет ошибочно перекрыт из-за baseline другого конца.

Это псевдоизометрическая подача внутри ортографической world plane, а не diamond-isometric projection. Камера по-прежнему применяет одинаковый scale по X/Y; перспективный вид создают art, овальный footprint и depth sorting.

## Movement и визуальная скорость

### Архитектурное правило

Movement хранится и считается только в world units:

```text
server moveSpeed m/s
    × 10 world units/m
    / 20 ticks/s
    = world units/tick
```

Pixels никогда не входят в server/client prediction. Экранная скорость является следствием камеры:

```text
logical pixels/second = moveSpeed m/s × 32 px/m
```

Исправление viewport само по себе не должно менять position integration, remainder, ACK или collision solver.

### Выявленная balance-проблема

Текущие скорости дают:

```text
Heavy Knight: 22.6 m/s × 32 = 723.2 logical px/s
Rogue:        33.4 m/s × 32 = 1068.8 logical px/s
Rogue sprint: 50.1 m/s × 32 = 1603.2 logical px/s
```

Rogue без sprint пересекает весь viewport шириной 40 м примерно за `1.2 секунды`. Это не ошибка camera math, а фактическое значение текущего `move_speed`, которое старое сжатие мира визуально скрывало.

### Реалистичный locomotion baseline

Предыдущая идея уменьшить все скорости коэффициентом `0.25` отклонена: она оставляла обычное движение Heavy Knight на `5.65 м/с`, а Rogue на `8.35 м/с`. Это скорости бега, а не обычного шага, и они противоречат текущему animation contract: обычное WASD проигрывает `walk`, а только Shift переключает персонажа на `run`.

Нужно моделировать два независимо понятных значения:

- `move_speed` — обычный шаг/боевое перемещение без расхода stamina;
- `sprint_speed` — кратковременный бег с расходом stamina.

Предлагаемый реалистичный первый baseline:

| Unit | Обычное движение, м/с | Sprint, м/с | Характер движения |
|---|---:|---:|---|
| Citizen | 1.55 | 4.65 | Быстрый шаг, обычный бег |
| Spearman | 1.45 | 4.00 | Осторожное движение с длинным оружием |
| Archer | 1.55 | 4.80 | Лёгкое снаряжение |
| Guard Swordsman | 1.35 | 3.70 | Щит и средняя экипировка |
| Greatsword | 1.30 | 3.50 | Тяжёлое двуручное оружие |
| Axe Warrior | 1.40 | 3.90 | Средне-тяжёлая экипировка |
| Caped Warrior | 1.65 | 5.20 | Лёгкий быстрый боец |
| Rogue | 1.80 | 6.20 | Самый быстрый шаг и короткий sprint |
| Skullcap Warrior | 1.30 | 3.50 | Тяжёлый щит |
| Heavy Knight | 1.15 | 2.90 | Самое медленное движение в тяжёлой броне |
| Paladin | 1.20 | 3.00 | Тяжёлая броня, немного быстрее Heavy Knight |

Rogue `6.2 м/с` — это только ограниченный stamina бег, а не постоянная скорость. Heavy Knight постоянно движется `1.15 м/с`, а его `2.9 м/с` также доступно только при расходе stamina.

При camera scale `32 logical px/m`:

```text
Heavy Knight walk:   1.15 × 32 = 36.8 px/s
Rogue walk:          1.80 × 32 = 57.6 px/s
Heavy Knight sprint: 2.90 × 32 = 92.8 px/s
Rogue sprint:        6.20 × 32 = 198.4 px/s
```

Так персонаж ростом `57.6 px` проходит примерно `0.64–1.0` собственной визуальной высоты в секунду обычным шагом и `1.6–3.44` высоты в секунду во время бега. Это выглядит как шаг/бег человека, а не перемещение игрового маркера по мини-карте.

Время пересечения viewport шириной 40 м:

- обычным шагом: примерно `22–35 секунд`;
- непрерывным sprint: примерно `6.5–14 секунд`.

### Последствия реалистичной скорости для карты 3.2 км

Реалистичная пехота не может одновременно быстро пересекать карту 3.2 км:

```text
3200 м / 1.80 м/с ≈ 29.6 минуты
3200 м / 1.15 м/с ≈ 46.4 минуты
```

Средняя территория размером около 640 м проходится обычным шагом примерно за `6–9 минут`. Даже непрерывный sprint, который фактически ограничен stamina, занял бы около `1.7–3.7 минуты`.

Это не следует исправлять возвратом сверхчеловеческих скоростей. Для требования GDD «быстро находить войну» нужны:

- spawn в Capital/Town Hall/Fort/Rally Point рядом с выбранным фронтом;
- выбор активного battle/respawn point до появления в мире;
- разумное расстояние между локальными objectives внутри территории;
- дороги и более быстрые стратегические способы перемещения как отдельная будущая механика, если playtest покажет необходимость;
- отсутствие обязательного пешего перехода от столицы через всю campaign map после каждой смерти.

Если gameplay потребует регулярно проходить 600–1000 м пешком за одну короткую сессию, необходимо уменьшать расстояния между objectives или пересматривать размер playable landmass, а не обозначать `8–30 м/с` как скорость человека.

### Представление sprint в данных

Для читаемости и безопасного баланса лучше заменить относительный `sprint_speed_multiplier` абсолютным `sprint_speed` в м/с. Тогда source of truth непосредственно отвечает на два вопроса: как быстро юнит идёт и как быстро он бежит.

Если на первом этапе schema сохраняет multiplier, он вычисляется из утверждённых абсолютных значений и не является первичным design-параметром:

```text
sprintSpeedMultiplier = sprintSpeed / moveSpeed
```

Например:

```text
Heavy Knight: 2.90 / 1.15 ≈ 2.52
Rogue:        6.20 / 1.80 ≈ 3.44
```

Sprint stamina следует настроить на ограниченный непрерывный бег, ориентировочно `4–7 секунд`, а не оставлять текущие `7–13.5 секунд` автоматически. Точные stamina cost/regen определяются отдельным playtest после того, как камера покажет правильный масштаб.

Camera и speed balance должны выпускаться отдельными commits/feature flags, чтобы тесты точно показали источник изменения ощущения управления.

Animation playback следует привязать к фактической скорости/режиму walk-run, иначе после rebalance появится foot sliding. Базовая формула:

```text
animationCyclesPerSecond = actualMetersPerSecond / authoredStrideMetersPerCycle
```

До появления точных stride metadata допускается per-unit authored `walkAnimationSpeed`/`runAnimationSpeed`, но не вычисление от screen pixels.

## Изменения MovementController и PlayerManager

### MovementController

- Хранить `_previousWorldPosition` и `_currentWorldPosition`, а не previous/current screen position.
- `advancePosition` остаётся в world units и использует текущий collision solver без screen conversion.
- `render(alpha)` интерполирует world coordinates и записывает их в `PlayerView.position`.
- Удалить `getScreenPosition`; добавить `getRenderWorldPosition`.
- `setInitialPosition`, reconciliation и correction не вызывают world→screen.
- Camera читает render world-position после interpolation.
- Resize ничего не вызывает в MovementController.

### RemotePlayer / PlayerManager

- Snapshot/interpolation остаются в world units.
- `RemotePlayer.update` пишет interpolated position напрямую в `PlayerView.position`.
- Удалить `CoordinateConverter` из constructors.
- Удалить `updateAllPlayerPositions()` на resize.
- Visual root и status bar уничтожаются одной операцией `PlayerView.destroy()`.
- Status bars обновляются через overlay projection только для видимых entities.

## Input, aiming и protocol

Pointer pipeline:

```text
PointerEvent.clientX/clientY
    ↓ canvas.getBoundingClientRect()
Canvas CSS coordinate
    ↓ PresentationViewport.clientToView()
Reference view coordinate
    ↓ Camera2D.viewToWorld()
World aim coordinate
```

- `InputManager` хранит reference view и world pointer, а не необработанный canvas offset.
- Pointer в letterbox/pillarbox возвращает `null` и не инициирует gameplay action.
- Facing вычисляется из `aimWorld - playerRenderWorld`, а не из screen delta старого converter.
- Camera translation не меняет направление aim.
- Mouse position повторно проектируется после resize/camera movement, даже если физически мышь не двигалась.

Текущий ATTACK packet содержит два `float32` screen coordinates, но сервер проверяет только длину и не читает значения. Экранные координаты не должны входить в gameplay protocol:

- в coordinated protocol bump заменить ATTACK packet на `{type}` либо `{type, authoritative aim/facing sequence}`;
- до bump допустимо отправлять нули в legacy 9-byte packet, но не использовать screen position;
- server hit detection в будущем использует сохранённый authoritative facing/world state, а не доверяет viewport клиента;
- удалить/зарезервировать неиспользуемый `MessageViewportUpdate`; AOI не должен принимать физическое разрешение как способ увеличить интересующую область.

## Structures, map и collision debug rendering

### Structures/map

World geometry рисуется непосредственно в world units:

```text
graphics.rect(minX, minY, maxX - minX, maxY - minY)
```

Не использовать `virtualToScreen()` и `getScale()`. Uniform camera transform `worldRoot` масштабирует результат.

Collider прямоугольного объекта описывает только его опору на земле. Texture/Graphics объекта может быть выше и шире collider. Каждый renderable object получает `groundPoint`, `groundFootprints`, `visualBounds` и `sortBaselineY`; collision rectangle нельзя использовать как готовые screen bounds спрайта.

Тестовые solid-color прямоугольники допустимы как debug-представление collider, но production renderer должен разделять ground geometry и visual/depth segments. Map objects и неподвижные player-built structures проходят через одинаковую схему сортировки; различие static/dynamic collision grids не должно быть видно renderer.

Для world `32000×32000` нельзя постоянно перерисовывать всю карту одним Graphics:

- разбить map/structures на render chunks, используя spatial-grid/chunk index;
- создавать или включать только chunks, пересекающие `camera.visibleWorldBounds(renderMargin)`;
- structure delta перерисовывает только затронутый chunk;
- camera movement обновляет visible chunk set, но не geometry каждой стены;
- collision grid остаётся независимым от render culling.

### Debug overlay

- AABB объектов и ellipse игроков рисовать в `worldRoot` в настоящих world units.
- Удалить `MIN_PLAYER_DEBUG_RADIUS_PX`: `radiusX/radiusY` должны масштабироваться камерой как реальная геометрия.
- Показывать `groundPoint`, `sortBaselineY`, `visualBounds` и ground footprint разными цветами, чтобы не спутать collision с визуальным перекрытием.
- ID/labels рисовать в `entityOverlayRoot`, чтобы текст оставался читаемым и не масштабировался как мир.
- Overlay показывает camera bounds, center, `pixelsPerWorldUnit`, mouse world coordinate и visible chunk IDs.

## Render culling

Камера исправляет обзор, но сама по себе не сокращает число созданных/обновляемых sprites. Первая версия должна добавить client-side render culling:

```text
renderBounds = camera.visibleWorldBounds(CULL_MARGIN_WORLD_UNITS)
```

- margin должен покрывать максимальный sprite/effect overhang и interpolation movement; стартовое значение `30 world units = 3 м`;
- entity вне bounds сохраняет network/simulation state, но `PlayerView.renderable=false`;
- animation ticker и status bar для невидимой entity не обновляются; для управляемого обновления массовых анимаций использовать `AnimatedSprite.autoUpdate=false` и общий visible-animation loop;
- при входе в bounds entity восстанавливает правильную animation state из текущего network state;
- broad-phase visible query использует отдельный клиентский `PresentationSpatialIndex`, а не полный обход всех игроков;
- structure/map chunks используют тот же camera bounds;
- culling не является security/AOI и не меняет получаемые от сервера данные на первом этапе.

`PresentationSpatialIndex` не является authoritative collision DynamicGrid сервера:

- хранит только client-known player IDs и их последние render/simulation world positions;
- использует те же cells `64×64 world units` и `touchedCells`-очистку;
- перестраивается один раз после client simulation/network update, а не каждый render frame;
- camera query обычно посещает примерно `7×5` cells плюс margin;
- visible set обновляется после rebuild и при переходе камеры через cell boundary;
- local player всегда включён независимо от query;
- появление/исчезновение `PlayerView` выполняется через pool, без создания/уничтожения Pixi objects при каждом пересечении края камеры.

Server-side AOI реализуется позже. Его bounds должны вычисляться сервером из фиксированных `400×225 world units` плюс replication margin, а не из присланного клиентом screen resolution.

## Config и source of truth

Заменить несемантический `player_base_scale` явными параметрами. Предлагаемые поля `game_settings`:

```text
reference_view_width_px       = 1280
reference_view_height_px      = 720
camera_view_width_units       = 400
camera_view_height_units      = 225
player_visual_height_units    = 18
gameplay_zoom_min_milli       = 1000
gameplay_zoom_max_milli       = 1000
```

`player_visual_height_units` задаёт общий визуальный рост. `collision_radius_x_units` и `collision_radius_y_units` являются параметрами конкретного unit type, потому что полная видимая ширина базового sprite может различаться. Они калибруются из reference frame по формулам выше, округляются наружу и затем становятся фиксированными gameplay-данными; runtime не вычисляет collider из текущего animation frame.

Server startup validation:

```text
referenceWidth  / cameraViewWidth  ==
referenceHeight / cameraViewHeight == 3.2 px/world unit

playerVisualHeightUnits > 0
cameraViewWidth/Height < worldWidth/Height
minZoom == maxZoom == 1.0 в первой версии
```

Из-за дробного `3.2` сравнение выполнять через cross multiplication, а не float equality:

```text
referenceWidth × cameraViewHeight == referenceHeight × cameraViewWidth
```

`GET /api/config` должен отдавать эти поля в `render`/`camera`, после чего клиент вычисляет derived values. Hardcode допускается только в тестах.

`player_base_scale`:

- перестать использовать в `SpriteLoader`;
- оставить deprecated DB column на одну migration для rollback compatibility;
- удалить после перехода всех visual assets на metrics manifest.

## Изменения по файлам

- `src/client/utils/coordinateConverter.ts`: удалить после миграции; временно заменить facade над Camera2D только для поэтапного перехода.
- `src/client/main.ts`: создать scene graph, PresentationViewport, Camera2D, mask и новый resize pipeline.
- `src/client/controllers/movementController.ts`: перейти со screen-position interpolation на world-position interpolation.
- `src/client/game/playerManager.ts`: хранить PlayerView roots в worldRoot, удалить resize-loop по игрокам.
- `src/client/game/playerView.ts`: root в ground point, foot ellipse/shadow, sprite offset по authored ground anchor и стабильный depth key.
- `src/client/utils/spriteLoader.ts`: возвращать textures + CharacterVisualMetrics, а не готовый screen scale.
- `src/client/utils/animationLayout.ts`: удалить `TARGET_ONSCREEN_SIZE`, `CONTENT_CELL_OVERRIDE`, `recommendedScale`; оставить layout parsing.
- `src/client/game/structureRenderer.ts`: разделить ground footprint и visual/depth segments, рисовать world coordinates и chunk только один раз/по delta.
- `src/client/debug/colliderDebugOverlay.ts`: AABB/ellipse/ground baseline в world space + screen-space labels, без искусственного radius floor.
- `src/client/collision/*`: заменить player circle на per-unit foot ellipse и раздельные X/Y extents в swept bounds.
- `src/server/internal/collision/*`: заменить `circle-vs-AABB` на integer `ellipse-vs-AABB`, обновить sliding, spawn clearance и world bounds.
- server/shared unit definitions: добавить фиксированные `collisionRadiusXUnits`/`collisionRadiusYUnits` для каждого unit type.
- `src/client/ui/statusBar.ts`: позиционировать через overlay projection, не через sprite screen position/height.
- `src/client/utils/inputManager.ts`: DOM→reference→world pointer conversion и игнорирование bars.
- `src/shared/gameConfig.ts`: добавить типы camera/render config, удалить использование `player.baseScale`.
- `src/server/internal/config`: загрузить/валидировать новые fairness-critical camera fields и отдать клиенту.
- `docker/postgres/init/001_init.sql`: добавить новые config columns/seed после утверждения baseline.
- client/server protocol: убрать screen coordinates из ATTACK и определить судьбу VIEWPORT_UPDATE.

## Порядок реализации

1. Добавить pure math `PresentationViewport` и `Camera2D` с unit tests, не подключая их к текущей сцене.
2. Добавить camera/render config в PostgreSQL, server config validation и `/api/config`.
3. Создать новый Pixi scene graph с reference viewport mask и letterbox; оставить старые entities временно в compatibility container.
4. Перевести terrain/structure/debug world geometry на прямые world coordinates внутри `worldRoot`.
5. Ввести `PlayerView`, visual metrics manifest, ground anchor и физическую высоту 18 world units.
6. Мигрировать server/client movement collision с общего круга на фиксированный per-unit foot ellipse; обновить spawn clearance, bounds, sliding и anti-tunneling tests.
7. Перевести локального игрока и MovementController на world-position render interpolation.
8. Подключить Camera2D к render-position локального игрока и реализовать world-bound clamp.
9. Перевести RemotePlayer/PlayerManager и удалить resize-reprojection всех entities.
10. Ввести детерминированный Y-sort по ground baseline; разбить длинные стены/крупные visuals на depth segments.
11. Перенести status bars/nameplates в `entityOverlayRoot`.
12. Перевести pointer/facing/attack pipeline на reference/world coordinates.
13. Добавить render chunking и camera culling для structures/map/players.
14. Удалить CoordinateConverter, `TARGET_ONSCREEN_SIZE`, `recommendedScale` и deprecated screen-position APIs.
15. Выполнить protocol bump для ATTACK без screen floats; удалить или формально зарезервировать VIEWPORT_UPDATE.
16. Прогнать visual parity на разных resolutions/aspect ratios, occlusion и performance tests.
17. Отдельным balance change применить реалистичные `move_speed`/`sprint_speed`, обновить PostgreSQL/UNITS/GDD и провести playtest времени локального боя и пути между objectives.
18. После принятия масштаба обновить README и отметить camera/viewport как реализованные.

Каждый шаг должен оставлять build рабочим. Camera refactor и speed rebalance не объединять в один commit.

## Test Plan

### Pure transform tests

- `worldToView`/`viewToWorld` round-trip с допустимой float epsilon.
- Uniform scale: world circle остаётся кругом при `16:9`, ultrawide и `4:3`.
- Center mapping: camera center всегда `(640,360)` до world-edge clamp.
- Corner mapping для visible bounds `400×225 units`.
- Presentation mappings для `1920×1080`, `2560×1080`, `1024×768`, `3840×2160`.
- Pointer за пределами letterbox/pillarbox возвращает `null`.
- DPR `1/1.5/2/3` не меняет visible world bounds.

### Camera tests

- Локальный игрок центрирован вдали от краёв.
- Camera корректно clamp-ится на всех четырёх сторонах и углах мира.
- Resize не меняет camera center/world target.
- Movement interpolation двигает camera каждый render frame без 20 Hz jitter.
- Reconciliation корректирует player/camera без изменения authoritative math.

### Sprite metric tests

- Для каждого unit существует CharacterVisualMetrics.
- Neutral reference frame существует и имеет ожидаемый source body height.
- Непрозрачная reference body height после scale равна `18 world units` с tolerance.
- Regular 16px и Knight 24px sheets дают одинаковый физический рост.
- Смена idle/walk/run/attack не меняет root transform/ground point.
- Left/right flip не сдвигает ноги.
- Weapon/effect overhang не изменяет sprite scale.
- Для каждого unit ellipse покрывает полную видимую ширину authored neutral/movement sprite плюс округлённый наружу padding и не зависит от attack/cape frame.
- Центр ellipse и ground anchor всех animation frames совпадают с entity root между ступнями.

### Foot collision и depth-order tests

- Ellipse далеко от AABB, касается стороны/угла, пересекает сторону/угол и имеет центр внутри AABB; equality остаётся допустимым касанием.
- Разные `radiusX/radiusY` корректно расширяют swept bounds, world bounds и spawn clearance; промежуточные micro-steps не проходят сквозь тонкую стену.
- Player-player movement collision отсутствует после миграции на ellipse.
- Игрок с baseline ниже компактного объекта рисуется поверх него; игрок выше baseline рисуется за объектом.
- При равных Y порядок стабилен между кадрами и не зависит от порядка прихода network entities.
- Смена animation/facing не меняет ground point, collider или sort key.
- Длинная стена/depth-segment корректно перекрывает игроков у обоих концов.
- Голова может визуально пересечь sprite камня до контакта ног; collision начинается только при касании ellipse и ground AABB.

### Resolution fairness visual tests

Playwright screenshots для одного world snapshot:

- `1280×720`;
- `1920×1080`;
- `2560×1080`;
- `1024×768`;
- `3840×2160` с DPR 1/2, где доступно.

После обратного приведения game viewport к `1280×720` world content должен совпадать. На ultrawide/4:3 различаются только bars и физический presentation scale.

### Gameplay regression

- Client/server movement golden tests не меняются от resolution.
- Collision/sliding/tunneling results не меняются после camera refactor.
- Mouse facing одинаков для одной world aim point на всех resolutions.
- Attack не содержит и не использует screen coordinate.
- Structure collider и rendered wall совпадают в world space.
- Status bars остаются над нужной entity при camera movement и edge clamp.
- Resize во время movement/attack не отправляет movement correction и не меняет world position.

### Performance

- Camera movement не вызывает обход/reprojection всех `32000×32000` world objects.
- Resize не проходит циклом по всем players/structures.
- 1700 network entities, из которых видимы 100–300: frame update/animation work выполняется только для visible set.
- Structure delta перерисовывает только affected chunks.
- Не создавать Point/temporary object на каждую entity каждый render frame в hot path.

## Debugging и метрики

Development overlay должен показывать:

```text
camera center world X/Y
visible world min/max
reference viewport 1280×720
pixels per meter / world unit
presentation scale and bars
canvas CSS size and DPR
local player visual height in world/logical pixels
visible/culled players and chunks
mouse reference/world coordinates
```

Client performance counters:

- visible/total player views;
- active/total render chunks;
- camera transform time;
- culling time;
- overlay projection count;
- resize count;
- current presentation scale/DPR.

## Acceptance Criteria

- На `1280×720`, `1920×1080`, ultrawide и `4:3` видны одни и те же `40×22.5 м` мира.
- Ultrawide не показывает дополнительную область слева/справа.
- World circle имеет одинаковый X/Y radius на любом разрешении.
- Reference body каждого player visual имеет высоту `18 world units = 1.8 м`.
- Authoritative position игрока находится между ступнями и совпадает с центром foot ellipse.
- Foot ellipse по X покрывает полную видимую ширину персонажа в authored neutral/movement frame плюс округлённый наружу padding; по Y покрывает только площадь опоры на земле.
- Голова, торс, плащ и оружие не расширяют movement collider.
- Игрок ниже объекта рисуется перед ним, выше его baseline — за ним; порядок не мерцает при равных Y.
- При baseline zoom тело имеет `57.6 logical px`, независимо от world size.
- Изменение world с `6000×3000` на `32000×32000` больше не меняет размер спрайта или экранную скорость одного метра.
- Игрок около центра мира не видит границы карты; виден только camera window.
- Camera следует за render-interpolated local position без заметного 20 Hz рывка.
- Resize не изменяет ни одну world position и не требует `updateAllPlayerPositions()`.
- Стены и collider overlay совпадают при всех aspect ratios.
- Input/facing корректны внутри viewport, bars не принимают gameplay clicks.
- Server/client prediction остаётся согласованным; collision golden tests обновлены на per-unit ellipse и проходят побитно детерминированно.
- Client build и protocol tests проходят после удаления screen-space attack payload.
- Render cost зависит от visible entities/chunks, а не от полной площади карты.

## Зафиксированные решения

- Bounding world остаётся квадратным `3.2×3.2 км`.
- Gameplay projection — ортографическая top-down без rotation.
- Reference viewport — `1280×720`, fixed `16:9`.
- Начальный обзор — `40×22.5 м`, `32 logical px/m`.
- Gameplay zoom первой версии заблокирован на `1.0`.
- Разные aspect ratios используют letterbox/pillarbox, а не расширение обзора и не non-uniform stretch.
- Player visual height — `1.8 м = 18 world units`.
- Movement collider — фиксированный per-unit axis-aligned foot ellipse: X покрывает полную видимую ширину базового neutral/movement sprite с padding, Y описывает только глубину опоры; единый круг `0.4 м` отменяется.
- Entity root хранит world ground point между ступнями; sprite является визуальным child.
- Players и world objects сортируются по authored ground baseline; длинные visuals разбиваются на depth segments.
- Camera следует за интерполированной локальной позицией.
- Resolution/DPR влияют только на presentation quality.
- Текущая скорость требует отдельного rebalance: обычное движение `1.15–1.8 м/с`, кратковременный sprint `2.9–6.2 м/с`; прежний кандидат `move_speed × 0.25` отклонён как всё ещё нереалистичный.
