**Ревью Go-бекенда и архитектуры бесшовного мультиплеера — 9 сентября 2026**

Проверено рабочее дерево на основе `fb523c9c3613a7c7e81967f1e1f5595c58d7dbbc`: весь Go-бекенд, связанные участки клиента, SQL-инициализация, Docker и средства тестирования. Код приложения не изменялся. Это ревью исходников с локальными проверками, а не новый capacity test и не аудит работающего production-окружения.

Главный вывод: здесь уже есть компактный, оптимизированный сервер одного игрового мира. Но распределённая архитектура бесшовной карты пока не реализована. Следующий приоритет — корректность доставки, владение состоянием и lifecycle, затем межсерверные границы. Дополнительные микроптимизации до этого будут закреплять сложность вокруг неустойчивых контрактов.

Ваш результат с 1200 клиентами принимаю как подтверждение работоспособности конкретного сценария одного инстанса. Он не противоречит найденным ошибкам: большинство проявляется при пропусках сообщений, медленных соединениях, рестартах, изменении настроек или одновременных действиях. Эти условия нельзя проверить одним благополучным all-to-all прогоном.

Обозначения: **P1** — исправить до публичной эксплуатации или следующего этапа масштабирования; **P2** — существенный риск надёжности/развития; **P3** — упрощение и сопровождение. Отсутствующие игровые возможности отдельно обозначены как архитектурные пробелы, а не ошибки готовой реализации.

**Что уже сделано хорошо**

Сервер считает движение сам; клиент передаёт ограниченный вектор и sequence. Есть проверки формата MOVE, маскирования WebSocket, размера и фрагментации. Очередь движения ограничена одним актуальным образцом, а не растёт бесконечно. Состояние кодируется один раз на рассылку; кадры разделяются через reference counting. Есть один writer на соединение, ограниченная очередь, batching, отдельные ACK и измерение возраста состояния. Монотонные часы уже вынесены в `internal/clock`; управление TiDi уже учитывает долю медленных клиентов, а не максимум одного writer. Эти последние два пункта из старого анализа нагрузки уже исправлены — повторно выдавать их за актуальные дефекты неправильно.

**1. P1 — отбрасывание дельт конфликтует с общей базой репликации**

Код: [enqueueBroadcastJob](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:186), [broadcastTick](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:738), [обновление baseline](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:630), [клиентская проверка sequence](/home/qwezert/projects/pixi_node_game/src/client/network/networkManager.ts:379).

`prevStates` общий для всех клиентов. После broadcast он обновляется для всего мира, хотя часть получателей могла не попасть в выборку или потерять кадр из-за `pendingBroadcast`/глубины очереди. Клиент, пропустивший поворот или остановку игрока, не может восстановить их из следующих дельт относительно чужой базы. Клиент это обнаруживает: при gap отвергает дельту и запрашивает полный мир.

Получается петля: перегрузка → пропуск дельты → SYNC_REQUEST → индивидуальное копирование/сортировка/кодирование всего мира → дополнительная нагрузка. При recipient cap ниже числа клиентов пропуски становятся штатными, а не исключительными. Keyframes отдельных сущностей это не исправляют: клиент отвергает пакет с разрывом sequence ещё до применения записей.

Рекомендация: определить явный контракт baseline. Практичный первый шаг — при любом пропуске помечать соединение `needsFullState` и отправлять согласованный snapshot с новым baseline, объединяя повторные запросы. Для дальнейшего развития — версии сущностей, dirty-набор относительно конкретного получателя/группы либо self-contained снимки AOI. Простое разрешение клиенту игнорировать gaps ошибку не устраняет.

Проверка исправления: два клиента, намеренно пропущенный STOP только у одного; оба должны сойтись без повторяющегося потока полных миров. Повторить с cap, queue shedding и медленным writer.

**2. P1 — после частичной записи WebSocket writer продолжает повреждённый поток**

Код: [startWriteLoop](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:230), особенно обработка ошибки около строки 298.

При ошибке `Write`/`WriteTo` текущие jobs освобождаются, а соединение продолжает принимать новые кадры до 150 последовательных ошибок. Но ошибка может сопровождаться `n > 0`: часть заголовка или payload уже ушла. Следующий кадр клиент прочитает как остаток предыдущего. Кроме повреждения framing, успешно записанный следующий batch сбрасывает счётчик ошибок, поэтому поток может оставаться открытым.

В изолированной копии добавлен тест с `net.Conn`, возвращающим два записанных байта и ошибку для первого кадра. Он проверяет, что следующая запись начинается уже с нового кадра, а хвост первого потерян. Сам факт возможности частичной записи при timeout описан в [контракте net.Conn](https://pkg.go.dev/net#Conn).

Рекомендация: закрывать соединение при ошибке записи. Если когда-нибудь потребуется продолжение после временной ошибки, необходимо сохранять точный неотправленный остаток и порядок всего batch; для этого сервера закрытие значительно проще и надёжнее.

**3. P1 — atomics защищают отдельные поля, но не боевые переходы**

Код: [TryAttack/executeAttack](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:401), [блок](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:452), [tick worker](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:855).

Сетевой worker вызывает атаку/блок непосредственно, одновременно с tick worker этого игрока. Проверка состояния, списание stamina, установка `State`, `AttackStartTick`, `ComboStep`, сброс pending — разные операции. Например, tick worker уже решил завершить старую атаку, сетевой worker запускает новую, затем tick worker записывает Idle и нулевой start поверх новой атаки. Stamina списана, действие потеряно. Возможен и запуск buffered combo одновременно с новой сетевой атакой.

`-race` такие логические гонки не обнаружит: атомарные обращения корректны на уровне памяти. Нужен единый владелец изменяемого состояния. Сетевой слой должен валидировать и ставить команды в bounded inbox, а игровая фаза — применять движение, блок, атаку, join/leave в определённом порядке. Если используются параллельные игровые workers, один игрок не должен изменяться одновременно двумя из них или сетевой горутиной.

Не стоит смешивать контракты: MOVE может быть latest-value mailbox, а дискретные боевые действия требуют явно заданного порядка и политики переполнения.

**4. P1 — Shutdown не завершает ресурсы и может конфликтовать с tick**

Код: [Server.Shutdown](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:225), [GameWorld.Stop](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:846), [epoll waitLoop](/home/qwezert/projects/pixi_node_game/src/server/internal/server/epoll_linux.go:120), [readHandler](/home/qwezert/projects/pixi_node_game/src/server/internal/server/readhandler.go:3).

`Shutdown` отменяет контексты соединений, но не вызывает их cleanup и не закрывает raw sockets. В Linux writer выходит, а отмена контекста сама по себе не закрывает сокет; в `processRead` отменённое соединение просто игнорируется. `http.Server.Shutdown` не управляет hijacked WebSocket-соединениями — это прямо оговорено в [документации net/http](https://pkg.go.dev/net/http#Server.Shutdown).

У epoll нет Stop: `efd` не закрывается, `waitLoop` не проверяет контекст, канал jobs не закрывается, workers остаются ждать. `GameWorld.Stop` сразу закрывает worker channels, не дожидаясь остановки producer `gameLoop`: активный tick может отправить в закрытый канал. Повторный Stop тоже вызывает panic.

Рекомендация: draining/admission stop → остановить producer и дождаться его → завершить соединения через cleanup → остановить poller/readers/writers → закрыть worker channels и дождаться работников. Порядок должен исключать новых производителей после финального drain очередей. Добавить `sync.Once`, `WaitGroup`/done channels и deadline на весь shutdown. Проверять повторные Start/Stop и SIGTERM под churn-нагрузкой.

**5. P1 — общедоступны диагностические обработчики; при флаге — запись баланса**

Код: [HTTP routes](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:160), [admin](/home/qwezert/projects/pixi_node_game/src/server/internal/server/admin.go:111), [публикация порта](/home/qwezert/projects/pixi_node_game/docker/docker-compose.yml:6).

На одном listener находятся `/ws`, pprof, metrics и metrics/json. pprof включён без отдельного флага: `PPROF_BLOCK_RATE` управляет лишь дополнительным профилированием. Если этот порт доступен игрокам без внешних ограничений, они получают диагностические данные и возможность запускать дорогостоящие операции профилирования. `/metrics/json` также делает `runtime.ReadMemStats` на каждый запрос.

`ENABLE_UNIT_ADMIN_API=true` открывает запись баланса без аутентификации. Код честно предупреждает об этом в логах, но предупреждение не ограничивает доступ.

Рекомендация: отдельный приватный management listener; admin — аутентификация/авторизация и сетевое ограничение. Для JSON — ограничение body и времени чтения. Это условный production-риск: внешний reverse proxy/firewall может уже закрывать маршруты, но в репозитории такой границы нет.

**6. P1 — игровой конфиг и баланс почти не валидируются**

Код: [config.Build](/home/qwezert/projects/pixi_node_game/src/server/internal/config/config.go:126), [LoadDefinitions](/home/qwezert/projects/pixi_node_game/src/server/internal/units/units.go:124), [buildUnitTables](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:105), [clampLiveNetConfig](/home/qwezert/projects/pixi_node_game/src/server/internal/config/live.go:76).

TickRate=0 приводит к делению на ноль; отрицательные/слишком большие размеры сужаются до uint16; отрицательные скорости и чрезмерная stamina преобразуются в беззнаковые целые без проверки. При stamina >655.35 значение в сотых уже не помещается в uint16. ComboSteps не ограничен вместимостью wire-формата — четыре шага. `LoadDefinitions` проверяет только наличие spearman: дубликаты ID при программной загрузке перезаписываются, числовые границы не проверяются. SQL содержит практически только структурные ограничения, не ограничения игровых диапазонов.

В сетевом конфиге часть ошибок молча исправляется clamp-ом, часть проходит без проверки. Spawn не проверяется на принадлежность миру. Исправление `SpawnMax = SpawnMin + 1` само переполняется при Min=65535.

Рекомендация: единый Validate с диапазонами, finite-проверкой float и межполевыми инвариантами перед любой публикацией конфига. Проверять диапазоны до преобразования типов. Неверные env и строки БД должны давать понятную ошибку, а не неожиданный fallback. Для hot reload сохранять предыдущую целиком проверенную версию.

**7. P2 — PATCH на деле полностью перезаписывает запись**

Код: [unitStatsPatchRequest](/home/qwezert/projects/pixi_node_game/src/server/internal/server/admin.go:17), [UPDATE units](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/settings.go:324).

Большинство полей JSON — обычные float/bool, не optional. Запрос `{"hp":100}` записывает нули во все отсутствующие обязательные числовые поля, false в флаги, NULL в отсутствующие вложенные возможности. При отсутствии валидации это может обнулить скорость и stamina целого класса.

Либо реализовать настоящий PATCH с различением «не передано»/«передано null»/значение и проверкой итогового объекта, либо назвать операцию PUT и требовать полное определение. Добавить revision для защиты от потерянных обновлений двух редакторов.

**8. P1 — нет аутентификации игровой сессии и проверки выбора юнита**

Код: [handleWebSocket](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:298), [AddPlayer](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:227).

Каждый upgrade создаёт нового игрока; `?unit=` принимается от клиента и разрешается через `units.Get`. Поля стоимости и `RequiresRoyalGuard` при входе не проверяются. Для sandbox-прототипа это допустимо, для persistent war — обход прогрессии и отсутствие устойчивой идентичности.

До публичной игры нужны короткоживущий join ticket, стабильный account/character ID, проверка прав на выбранный класс, защита от параллельных сессий и привязка ticket к миру/инстансу. Origin allowlist полезен против подключений со сторонних сайтов, но не заменяет ticket и не останавливает произвольный небраузерный клиент.

**9. P2 — лимит соединений проверяется с TOCTOU**

Код: [handleWebSocket](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:300).

Число соединений читается под RLock, затем выполняются upgrade, создание игрока и начальные snapshots, и лишь потом запись в map. Несколько одновременных запросов видят свободное последнее место. Поэтому cap — не строгое ограничение ресурсов, особенно при reconnect storm.

Рекомендация: резервировать слот до upgrade через semaphore/CAS, освобождать на всех ошибках и cleanup. Отдельно ограничить число handshake in flight. Проверить соответствие production-конфига вашему soft cap: дефолт `MAX_CONNECTIONS` в коде — 12000, live-значение может переопределяться БД. Фактическую БД в этом ревью я не менял и не проверял.

**10. P1 — общие epoll workers могут блокироваться на неполном frame**

Код: [newEpollPoller](/home/qwezert/projects/pixi_node_game/src/server/internal/server/epoll_linux.go:41), [processRead](/home/qwezert/projects/pixi_node_game/src/server/internal/server/epoll_linux.go:166).

Готовность fd означает наличие некоторых байтов, не полного WebSocket-сообщения. После EPOLLIN worker делает `ws.ReadHeader` и `io.ReadFull` через блокирующий API `net.Conn`, ожидая до 100 ms. Достаточно потока соединений с неполными заголовками/телами, чтобы занять небольшой общий пул и задержать ввод остальных игроков. Даже честный клиент может попасть в timeout при задержке продолжения TCP-потока; это ограничение времени сборки кадра, не ограничение RTT.

Рекомендация: сравнить с обычным reader-per-connection на Go netpoll. Если кастомный epoll действительно нужен, реализовать неблокирующий инкрементальный парсер с состоянием на соединение, лимитами работы за проход и отдельной политикой incomplete frame. Нужен A/B профиль; меньший goroutine count сам по себе не доказывает выигрыш.

**11. P2 — epoll lifecycle не защищён поколением соединения**

Код: [remove/rearm](/home/qwezert/projects/pixi_node_game/src/server/internal/server/epoll_linux.go:105).

Удаление и rearm адресуют голый fd; ошибки `EpollCtl` игнорируются. Между cleanup и завершением уже выполняющегося read job ОС может переиспользовать fd для нового соединения, а старый worker вызовет MOD по тому же номеру. В ветке HUP также есть remove до асинхронного cleanup, который вызывает remove повторно.

Это вывод из возможного interleaving, не воспроизведённый массовым churn-тестом инцидент. Нужны generation/token в регистрации, проверка `fds[fd] == c`, синхронизация rearm/remove/close и наблюдаемая обработка неожиданных ошибок. Совместить исправление с пересмотром epoll, а не добавлять отдельные проверки наугад.

**12. P1 — дорогой SYNC_REQUEST имеет только общий message limiter**

Код: [processMessage](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:367), [sendInitialState](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:911).

Один байт запроса запускает обход всех игроков, копирование snapshot, сортировку/кодирование и ещё копирование кадра. Это выполняется до проверки свободного места в writeCh. Лимит по умолчанию 120 сообщений/с не различает MOVE и snapshot; один клиент способен постоянно заказывать существенно более дорогую операцию, в том числе забивая epoll worker.

Рекомендация: отдельный низкий лимит и single-flight resync на соединение, общий immutable full snapshot на tick, постановка запроса на обслуживание игровой фазой. Даже если клиентская библиотека ограничивает частоту, сервер обязан ограничивать её самостоятельно.

**13. P2 — control frames обходят лимит; IP policy не готова к proxy**

Код: [processRead](/home/qwezert/projects/pixi_node_game/src/server/internal/server/epoll_linux.go:214), [IP limiter](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:490).

Rate limiter применяется только к binary. Поток Ping/Pong расходует парсинг, очередь jobs, syscalls, а Ping ещё и создаёт ответы. `lastActivity` обновляется до проверки прикладной валидности. Ошибки decode логируются на каждый разрешённый пакет — возможен большой логовый поток.

IP определяется по RemoteAddr. За reverse proxy это может быть один адрес для всех игроков; лимит станет общим. Просто доверять любому X-Forwarded-For нельзя: нужен список доверенных proxy. Полная очистка map каждые пять минут сбрасывает активные token buckets и не ограничивает cardinality внутри окна.

Рекомендация: frame/byte rate limit с разумным исключением для нормального heartbeat; отдельные лимиты дорогих операций; sampling decode-ошибок; TTL/LRU для IP limiter и документированная схема trusted proxy.

**14. P2 — начальные и внеочередные snapshots не являются согласованным состоянием tick**

Код: [GetAllPlayers](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:302), [sendInitialState](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:911).

RLock защищает состав map, а не совместное состояние игроков. Пока GetAllPlayers читает X/Y/State по одному, tick workers двигают игроков. Затем отдельно читаются `worldStateSeq` и `tickCount`. Snapshot может объединять части двух тиков и иметь sequence, не соответствующий содержимому. Параллельный broadcast может уже поставить кадр того же или более нового sequence в очередь, и клиент отбросит пришедший full state как не новый.

Рекомендация: публиковать immutable `WorldSnapshot{tick, sequence, states, rosterVersion}` после игровой фазы и использовать его для join/resync. Присвоение sequence и порядок помещения в поток должны иметь одного владельца.

**15. P2 — join/leave и roster не согласованы с репликацией**

Код: [handleWebSocket](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:326), [cleanupConnection](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:465), [broadcastUnitRoster](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:994), [SQL defaults](/home/qwezert/projects/pixi_node_game/docker/postgres/init/001_init.sql:75).

Новому клиенту roster отправляется, существующим — только на периодическом full sync. Новый игрок уже появляется в delta, но его UnitType/HP/Stamina ещё неизвестны остальным; дефолт full sync — 30 секунд. `notifyPlayerJoined` существует, но не вызывается, и его старый формат всё равно не содержит UnitType.

При выходе сначала рассылается PlayerLeft, а RemovePlayer выполняется после этого. Tick мог сохранить указатель игрока и отправить его снова после сообщения ухода. Сам PlayerLeft best-effort и может быть потерян при полной очереди. Полный snapshot в итоге способен исправить состав, но до него возможны фантомы. Кроме того, клиент удаляет игрока из `players`, но не из `playerAttributes`, а roster применяется через Object.assign: метаданные ушедших накапливаются до сброса сессии.

Рекомендация: join/leave применять на границе tick; передавать spawn/despawn и атрибуты с версиями в согласованной репликации. Full snapshot должен заменять состав соответствующих таблиц. HP/stamina, когда они важны для боя, должны иметь собственные timely deltas; сейчас их обновление привязано к редкому roster.

**16. P2 — пространственная сетка выполняет работу, но не обслуживает видимость**

Код: [VisibilityManager](/home/qwezert/projects/pixi_node_game/src/server/internal/systems/visibility.go:17), [updatePlayerPosition](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:744).

У менеджера вообще нет query-метода: только Add/Move/Remove. Рассылка не использует ячейки. Это не реализованный AOI, а поддерживаемый индекс без потребителя: sync.Map lookup на каждом движении, locks и переносы между cells не дают функционального результата.

Есть две отдельные ошибки. Уже захваченный tick-ом Player может вызвать MovePlayer после RemovePlayer; ветка отсутствующего ID добавляет его обратно в индекс. Также `(worldWidth + gridSize - 1)` вычисляется в uint16: при width=65535 и cellSize=100 gridWidth становится нулём. Обе ситуации проверяются небольшими тестами в изолированной копии.

Если на текущем этапе весь инстанс намеренно видит всех — убрать индекс из горячего пути до появления потребителя. Если вводить AOI — query по серверной позиции/радиусу, enter/leave interest, halo соседних инстансов и обновление индекса тем же владельцем, что изменяет мир. Расчёт размеров выполнять в int/uint32.

**17. P1 для бесшовной карты — распределённого владения миром пока нет**

Код: [Server.New](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:91), [GameWorld.New](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:173), [integrateMovement](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:800).

Каждый процесс создаёт отдельную map игроков и локальный счётчик ID от 1000; движение упирается в clamp границ. Нет region ID, directory владельцев, маршрутизации к участку, handoff, ghost entities, межсерверных игровых сообщений, persistence персонажей или восстановления мира. Postgres/Redis сейчас обслуживают конфиг и баланс.

Запуск нескольких копий даст несколько независимых миров. Локальные ID разных процессов столкнутся при объединении данных. uint16-координаты подходят для локальных координат участка, но требуют явного region/origin при глобальной карте.

Это не повод переписывать всё в микросервисы. Стоит сохранить автономный zone server и добавить минимальный межзонный контракт, описанный ниже. Именно это отделяет capacity одного процесса от общей архитектуры на десятки тысяч игроков.

**18. P2 — Redis Pub/Sub допускает вечное расхождение конфигов между инстансами**

Код: [SetKey/Watch](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:114), [WatchUnits](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:176), [UpdateUnitStats](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/settings.go:324).

Сначала коммитится UPDATE в Postgres, потом отдельно PUBLISH. Сбой между ними оставляет БД обновлённой без уведомления; вызывающий код получает ошибку, хотя запись уже изменилась. Подписчик после временного отсутствия/ошибки reload не перечитывает периодически актуальную revision. Начальная загрузка и подписка тоже разделены окном. Redis документирует [at-most-once delivery Pub/Sub](https://redis.io/docs/latest/develop/pubsub/): потерянное уведомление не будет воспроизведено.

Рекомендация: хранить revision в БД и периодически сверять её, делать resync после reconnect; уведомления считать только ускорителем. Для строгой гарантии — transactional outbox и подтверждаемая доставка. Для config обычно достаточно revision + reconciliation; не требуется сложный брокер на каждый tick.

**19. P2 — live reload не означает согласованное применение на сервере и клиенте**

Код: [main reload callback](/home/qwezert/projects/pixi_node_game/src/server/cmd/server/main.go:90), [createConnection](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:346), [startWriteLoop](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:230), [загрузка клиентских units](/home/qwezert/projects/pixi_node_game/src/shared/units.ts:119).

Definitions, производные таблицы и JSON публикуются тремя отдельными шагами. Worker загружает tables, но вызываемые функции повторно читают global pointer; один tick может использовать две версии баланса. Клиент загружает units один раз на старте: изменение серверной скорости или stamina не обновляет уже загруженное клиентское предсказание. Ответ admin «applied live» возвращается после Publish, не после подтверждённого применения.

MessageRateLimit/BurstLimit и WriteBatchSize захватываются при создании соединения/writer; изменение live config их существующим клиентам не обновляет. IP limiter тоже живёт со старым rate до удаления. Название live создаёт неверное ожидание.

Рекомендация: immutable bundle `revision + definitions + derived tables`, активация на границе tick, передача config revision клиентам. Явно разделить runtime-live и new-connections-only настройки. Статус admin должен различать persisted, published и applied revision. Все числовые настройки нескольких регионов не должны случайно управляться одной глобальной строкой без instance/world scope.

**20. P2 — регулятор нагрузки имеет слепые зоны**

Код: [enqueueBroadcastJob](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:186), [tuneTimeDilation](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:646), [broadcastTick](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:738).

При уже pending broadcast функция освобождает новый кадр, инкрементирует shed, но возвращает true. Регулятор видит как будто успешную отправку. При queue shed не растёт fanoutDrops; при обычных defaults порог глубины очереди 6 наступает значительно раньше заполнения канала 32, поэтому disconnect по fanoutDrops плохо отражает длительное отставание. ACK, roster и full resync обходят byte budget, предназначенный только для tick frame.

`computeDur` берётся из предыдущего полного TickDuration, уже включающего fanout, а не из текущей вычислительной фазы. Если velocity replication отключена и мир неподвижен, ранний выход без кадра не вызывает восстановление TiDi. После перегруженной пустой зоны dilation также сохраняется до следующей активной рассылки.

Рекомендация: отдельные результаты enqueue — queued/coalesced/shed/closed; метрики и решения по каждому. Измерять simulation compute отдельно, считать общий egress, контролировать длительность отставания конкретного клиента. TiDi должен оцениваться независимо от наличия delta. Для соседних зон отдельно определить, допустимо ли различное течение simulation time.

**21. P2 — dead reckoning сервера расходится с реальной интеграцией**

Код: [classifyDelta](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:666), [updatePlayerPosition](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:744).

Предиктор delta использует целый `avgUnitsPerTick × VX/VY`; реальное движение учитывает fractional remainder, 1/√2 по диагонали и sprint multiplier. Поэтому нормальное диагональное/спринтерское/дробное движение часто классифицируется как diverged и снова отправляет позиции. Корректность сохраняется благодаря коррекциям, но обещанная экономия velocity replication зависит от направления и класса.

Рекомендация: общая спецификация интегратора для server/client/delta predictor либо явный velocity в подходящей fixed-point шкале. Сначала измерить records/bytes отдельно для straight/diagonal/sprint/fractional speed. Не усложнять предиктор, если в выбранном бюджете проще регулярно отправлять абсолютные позиции.

**22. P2 — боевой wire state не содержит время начала действия**

Код: [PlayerState](/home/qwezert/projects/pixi_node_game/src/server/internal/types/types.go:111), [appendWorldState](/home/qwezert/projects/pixi_node_game/src/server/internal/protocol/binary.go:83).

Передаются State и ComboStep, но не AttackStartTick/action ID. Подключившийся или восстановившийся в середине атаки клиент видит «атакует», но не может определить оставшуюся фазу windup/active/recovery. Для будущего боевого взаимодействия и переходов между зонами это существенно.

Рекомендация: реплицировать action ID/startTick или нормализованный phase progress с определённой временной шкалой. Не выводить начало действия только из момента получения delta.

**23. P2 — текущая нагрузка ещё не включает полноценный бой и persistence**

Код: [executeAttack](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:424).

Атака сейчас меняет состояние/комбо и списывает stamina. Нет поиска целей, hit detection, нанесения damage, смертей, projectile simulation, terrain collision, экономических транзакций и сохранения персонажа. Поля Definition для этих механик сами по себе не означают исполнение механик.

Это функциональная граница прототипа, не требование срочно реализовать весь GDD. Но capacity budget должен резервировать стоимость этих систем. После появления AoE/столкновений нельзя автоматически переносить число 1200 из теста репликации в тест боевой симуляции.

**24. P2 — readiness и runtime defaults слабо отражают эксплуатацию нескольких инстансов**

Код: [health](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:524), [optimizeRuntime](/home/qwezert/projects/pixi_node_game/src/server/cmd/server/main.go:141), [Compose](/home/qwezert/projects/pixi_node_game/docker/docker-compose.yml:1).

Health всегда healthy, даже если tick перестал продвигаться; нет draining, heartbeat tick, config revision и build revision. Это затрудняет безопасный rolling restart и связывание результатов теста с конкретным бинарником. Runtime принудительно ставит NumCPU при отсутствии GOMAXPROCS, обходя container-aware default современного Go; [Go 1.25 описывает учёт CPU quota и автообновление](https://go.dev/doc/go1.25). Дефолт GOGC=400 без явного memory budget нужно проверять при размещении нескольких процессов на одном хосте.

Compose публикует БД, Redis и мониторинг на host ports; без bind к loopback они потенциально доступны через все интерфейсы, если это не ограничено хостом. Redis healthcheck не использует пароль, хотя запуск Redis поддерживает requirepass: он не проверяет успешный authenticated PONG. SQL в init выполняется для новой БД; стратегии версионированных миграций для существующих volumes нет. Многие monitoring images используют latest.

Рекомендация: separate live/ready, tick watchdog, revision endpoints, resource requests/limits и memory budget на зону; приватные инфраструктурные порты; согласованный authenticated healthcheck; воспроизводимые image versions и миграции. Не требуется добавлять БД в обязательную проверку каждого tick: краткий сбой config store не должен останавливать уже работающий мир.

**25. P2 — часть метрик измеряет не то, что следует из названия**

Код: [tick phases](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:610), [queue depth](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:186), [metrics](/home/qwezert/projects/pixi_node_game/src/server/internal/metrics/metrics.go:79).

Фаза `delta` измеряется между двумя соседними time.Now уже после вычисления дельт; реальная работа попала в `range/world_step`. WSWriteQueueDepth наблюдается только при превышении shed threshold, поэтому её histogram не описывает обычную глубину очереди. AgeAtWriteEnd включает и неуспешные записи, тогда как BytesSent не учитывает уже отправленные байты ошибочного batch. MovementInputsRejected всё ещё описывает client tick/full ring, которых в актуальном mailbox нет.

Это не причина задержки сама по себе, но источник ошибочных выводов о ней. Исправить границы фаз, разделить write success/error, явно указать слой byte counters, добавить причины disconnect/resync. Частые вызовы Histogram/WithLabelValues в горячем пути оптимизировать только по профилю; метки здесь ограничены константами, доказанного cardinality explosion нет.

**Мёртвый код, старые API и фоллбеки**

Ниже кандидаты подтверждены поиском по Go-исходникам; это не автоматическое разрешение удалять публичные wire IDs без проверки клиента.

| Кандидат | Что с ним делать |
|---|---|
| `performanceMonitor` → пустой `logPerformanceStats`, server.go:505 | Удалить пустую фоновую горутину и методы. |
| `ServerConfig.Workers`, config.go:20 | Сейчас читается/логируется, но реальные pools используют GOMAXPROCS. Удалить настройку либо действительно подключить к конкретному пулу. |
| `GameWorld.attackDurationTicks`, `.comboSteps`, `.comboWindowTicks`, world.go:53 | Старые поля, заменены `unitTables`; удалить. Не путать с используемыми одноимёнными полями внутри unitTables. |
| `playerCountEstimate`, `lastFullSync`, world.go:59 | Записываются, но не читаются в рабочем коде. Удалить после выбора единственного источника счётчика/времени. |
| `Player.LastActivity`, types.go:81 | Не используется; активность уже хранится в Connection. |
| MessageCount/IncrementMessageCount/GetMessageCount | На каждый пакет выполняется atomic increment, но полученный счётчик не используется production-кодом. Общие Prometheus counters уже есть. |
| `notifyPlayerJoined` + `EncodePlayerJoined` | Недостижимая server-ветка; заменить на полноценный spawn с атрибутами, а не оживлять старое событие. |
| `EventMove`, `EventAttack` в `handleEvent` | Текущий network path их не использует. Старый EventAttack обходит стоимость/комбо-проверки; EventMove обходит QueueMovementInput validation. Удалить альтернативные пути или свести к общей реализации. |
| `MessageJoin`, `MessageLeave` | Константы без обработки; оставить зарезервированные номера в спецификации, убрать иллюзию поддерживаемых команд. |
| `MessageAttackEnd`, `MessageViewportUpdate` | Валидируются и игнорируются. Документировать временный compatibility no-op либо удалить отправку и поддержку при версии протокола. Viewport update сейчас не включает AOI. |
| Attack payload 9 bytes | Восемь байтов после type не используются сервером. Пересмотреть в следующей версии протокола. |
| `EncodeMovementAck` | В production writer кодирует вручную; helper используется тестом. Есть риск тестировать не тот encoder. Общий codec либо parity-тест между обоими путями. |
| `units.GetByTypeID`, `units.IsValid` | По Go-коду нет потребителей; убрать до появления использования. |
| `contains/indexStr` в epoll_linux.go | Ручной strings.Contains; заменить стандартными errors.Is/net.ErrClosed и нужными syscall errors, строковую проверку оставить только при обосновании. |
| `VisibilityManager` | Живой по вызовам, функционально бесполезен до query/AOI. Решение описано в пункте 16. |
| `MovementTransitionsLate`, `MovementTransitionLatenessTicks`, `MovementCorrectionDistance`, `MovementTimelineRebases` | Зарегистрированы, но нигде не обновляются; остатки старой модели input timeline. Удалить либо реализовать актуальное измерение с новым контрактом. |

Не рекомендую механически удалять все fallback. Non-Linux reader обеспечивает разработку на macOS/Windows; он полезен, если эти платформы поддерживаются, и может стать базой сравнения с epoll. VelocityReplication=false полезен для A/B, но должен иметь тесты и чёткий статус диагностической опции. Fallback неизвестного unit на spearman допустим для отсутствующего параметра, но неверный явно запрошенный unit лучше отклонять. Восстановление полным snapshot необходимо — исправлять нужно его контракт и стоимость.

Существующий fallback при ошибке EnsureSchema/Seed/LoadInto в main тоже стоит сделать явным: процесс продолжает работу на env/defaults и не запускает повторное восстановление net-config watcher. Несколько инстансов могут незаметно работать с разными настройками. Нужна выбранная политика: fail startup для обязательного конфига либо degraded status + retry/reconciliation.

**Какая архитектура нужна для бесшовной карты**

Сохранить zone server как единицу симуляции и отказа. Разделить внутри него приём команд, игровую фазу, публикацию snapshot и доставку. Имеющийся код протокола/движения/пулов можно использовать дальше; переписывание на другой язык или тотальная смена транспорта из этого ревью не следует.

Минимальные контракты:

1. **Идентичность.** Глобально устойчивый character/entity ID; wire ID может оставаться компактным локальным handle, но его область действия и epoch должны быть явными. Instance ID и region ID — разные сущности: регион переживает рестарт процесса.
2. **Владение.** Directory `region → current owner + epoch`, lease/fencing против двух одновременно активных владельцев. Нельзя полагаться только на «старый процесс, вероятно, умер».
3. **Переход.** Target резервирует место; source передаёт полный transfer state и последний input sequence; target подтверждает готовность; фиксируется новая ownership epoch; source прекращает authoritative изменения. Повторы transfer ID идемпотентны, потеря ACK не создаёт второго персонажа. Клиентские команды маршрутизируются по действующей epoch.
4. **Соседи.** Halo/ghost entities для видимости через границу, включая диапазон дальних атак и возможное перемещение между обновлениями. У каждого объекта остаётся один authoritative owner. Урон через границу — валидированная команда владельцу цели, с event ID и дедупликацией; нельзя независимо менять копии HP на двух серверах.
5. **Время.** Локальные тики не синхронизируются сами собой. При разных TiDi нужно явно определить время атаки, cooldown и transfer remaining duration. Межзонный протокол должен ограничивать возраст ghost state и иметь правила разрешения запоздалых действий.
6. **Сохранение.** Разделить эфемерную позиционную репликацию и долговечные события: инвентарь/экономика/владение/награды. Для важных изменений — идемпотентные операции и durable log/outbox; checkpoint позиции с допустимым rollback budget. Не сохранять каждое движение синхронно в Postgres.
7. **Маршрут клиента.** Выбрать стабильный gateway WebSocket либо прямое переключение между zone servers с transfer ticket. Gateway упрощает непрерывность сессии, но добавляет hop и собственный capacity budget. Выбор следует проверить измерениями и требованиями UX.
8. **Переполнение участка.** Если target достиг cap, соседний source не может просто протолкнуть игрока. Нужны admission policy, резерв на handoff и план горячих точек. Фиксированный участок не делится на две независимые копии без изменения смысла общего боя; spatial split требует новых границ владения и halo.

Начать с двух зон и нескольких клиентов, а не с оркестрации сотен процессов. Сначала доказать отсутствие двойного владения и потери персонажа при убийстве source/target на каждом шаге handoff, потом масштабировать.

**Как интерпретировать 1200 и оценивать общий бюджет**

При полной видимости стоимость отправки приблизительно `N получателей × K записей × B байт × F рассылок/с`. Когда все N объектов меняются и K=N, это O(N²) по сетевому объёму. Однократное кодирование экономит CPU, но не число отправленных байтов.

Для плотных ID типичная запись текущего протокола — около 8 байт: delta ID + X/Y + VX/VY + flags. Иллюстративный верхний активный сценарий 1200×1200×8×20 ≈230 MB/s ≈1.84 Gbit/s только world-state payload. Это **не измерение текущего сервера**: velocity replication уменьшает K, TiDi меняет F, а varint и заголовки меняют точный размер. Но такой расчёт нужен для массовых поворотов, sprint, боя и reconnect/resync, когда K резко растёт.

В сохранённом [анализе 5 сентября](/home/qwezert/projects/pixi_node_game/docs/load_analysis_20260905.md:1) плато ≥1200 длилось около 54 секунд; указаны p99 tick 17.77 ms, age-at-write-end 33.80 ms и egress около 194 Mbit/s. Это полезные исторические данные, не доказательство именно текущего бинарника и не end-to-end latency. Возраст при записи в сокет не включает доставку/рендер. Более новых ваших внешних прогонов этот вывод не отменяет.

30 тысяч игроков при cap 1200 требуют не менее 25 одновременно заполненных игровых процессов арифметически; это не означает 25 физических машин и не включает запас, соседние ghost entities, неравномерность населения и maintenance. Выбирать число зон нужно по плотности интереса и боевому бюджету, а не только делением общей аудитории на 1200.

**Тесты и подтверждение выводов**

В исходном дереве выполнены `go test ./...`, `go vet ./...`, `go test -race ./...` на Go 1.26.2 linux/amd64 — успешно. Это подтверждает текущие покрытые сценарии, но не отсутствие логических гонок или корректный shutdown.

Отдельные воспроизводящие проверки размещены в копии Go-модуля `/tmp/pixi-go-review-NEDLue`: partial write, восстановление удалённого ID в spatial index и overflow grid width. Все три успешно воспроизвели описанное поведение при запуске с `-race`; их PASS означает воспроизведение проблемы, а не её исправление. Код проверок: [writer test](/tmp/pixi-go-review-NEDLue/internal/server/review_test.go:1), [spatial tests](/tmp/pixi-go-review-NEDLue/internal/systems/review_test.go:1). Повторный запуск из этой копии: `GOCACHE=/tmp/pixi-go-review-cache go test -race ./internal/server ./internal/systems -run TestReview -v`. Основные файлы приложения не редактировались. Также успешно выполнен `node --test utils/testing/artillery/latency-probe.test.cjs`.

Интеграционный `utils/testing/protocol/run.sh` сейчас устарел: копирует отсутствующий `src/shared/gameConfig.json`, а `lib/config.mjs` читает этот же отсутствующий файл. Скрипт также не создаёт изолированные БД/Redis, хотя сервер теперь требует их. Нет `set -e`, поэтому ошибка cp не сразу останавливает запуск. Health probe по умолчанию идёт на 8108 и не проверяет PID/revision собственного процесса: уже работающий сервер может создать ложную готовность. Этот runner я не запускал против существующей инфраструктуры.

При этом актуальный Artillery processor уже подключает latency probe с nonce/ACK и resync — замечание старого отчёта об их полном отсутствии больше не актуально. Старый YAML по-прежнему делает цикл с суммой think=8 секунд; новый `latency-config.yml` задаёт решения о движении раз в 500 ms и примерно минутное плато 1200. Probe проверяет заголовки/sequence, но не равен полноценной клиентской модели мира: нужны assertions конечного состояния, roster и action phases.

Первоочередной набор проверок после исправлений:

| Сценарий | Что должно быть доказано |
|---|---|
| Partial write/timeout в середине header и payload, в batch | Соединение закрывается; повреждённый поток не продолжается; frame refs освобождены. |
| Gap/queue shed/recipient cap + STOP и разворот | Все наблюдатели сходятся; нет resync amplification. |
| Join/leave во время tick, медленный initial snapshot | Нет фантомов, неверного roster и несогласованной sequence. |
| Attack/block/queued combo на границе завершения | Нет потерянных атак и двойного списания; порядок воспроизводим. |
| SIGTERM и повторный Stop под churn | Нет panic, зависших goroutines/fd и новых enqueue после drain. |
| Неполные frames и Ping flood | Ввод честных клиентов не блокируется общим read pool. |
| Redis outage, потеря publish, reconnect watcher | Все регионы сходятся на одной revision. |
| Изменение speed/stamina при активных клиентах | Сервер и предсказание клиента используют согласованную версию. |
| Два региона, overflow target, сбой source/target на handoff | Ровно один authoritative персонаж; повторы безопасны. |

Для нагрузки: длительное стабильное плато 1200, отдельный генератор, смешанный трафик straight/diagonal/sprint/attack/block, reconnect storm, доля медленных клиентов и задержки/потери сети. Собирать p50/p95/p99/p99.9 MOVE→ACK, arrival gaps, resync rate, correction magnitude, tick lateness, TiDi, egress, CPU throttling, RSS, GC и goroutine/fd count. В браузере отдельно измерять decode/render и реальную задержку отображения. Это превращает «держит подключения» в проверяемый SLO качества игры.

**Рекомендуемый порядок работы**

Сначала исправить partial writes, shutdown, целостность команд и репликации; закрыть diagnostic/admin perimeter и добавить validation. Затем восстановить изолированные end-to-end тесты, устранить roster/resync проблемы и закрепить нагрузочный baseline с revision и конфигом. После этого убрать мёртвые пути и проверить необходимость epoll/AOI по профилю. Следующий архитектурный этап — две зоны с ownership/handoff/ghosts и испытаниями отказов. Оптимизации heap fanout, GC и расширение числа workers имеют смысл после этих контрактов и измерений.
