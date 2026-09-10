**Повторное ревью Go-бекенда — 10 сентября 2026**

Проверен коммит `50da036b2e98ecf10a429e6988eb9139ee3f251e` после исправлений P1 (`e4e2bd9`) и P2 (`50da036`). Рабочее дерево перед проверкой было чистым. Область: Go-сервер, контракт с клиентом, SQL/configctl, Docker, мониторинг и тестовые инструменты. Исходники приложения не изменялись. Аккаунты и распределённая бесшовность, по вашему решению, остаются отдельным этапом.

Сервер стал заметно надёжнее: устранены опасные пути чтения/записи и остановки, ограничена работа SYNC_REQUEST, сериализованы боевые переходы. Однако считать исправления завершёнными нельзя: новые изменения предсказания нарушили контракт с клиентом, а ограничение рассылки может лишить активных клиентов обновлений мира. Зелёные unit/race-тесты эти ошибки не обнаруживают.

Приоритеты: **P1** — исправить до эксплуатации соответствующего режима; **P2** — существенные ошибки и риски надёжности; **P3** — сопровождение и упрощение. Ниже отдельно обозначены воспроизведения, выводы из исходников и ограничения ещё не реализованной функциональности.

**1. P1 — сервер перестал отправлять коррекции, необходимые клиентскому предсказанию**

Код: [classifyDelta](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:702), [deadReckon](/home/qwezert/projects/pixi_node_game/src/client/game/playerManager.ts:115), [unitsPerTick](/home/qwezert/projects/pixi_node_game/src/client/utils/movement.ts:5).

Новый предиктор дельт повторяет серверное движение: дробный остаток, sprint multiplier, диагональ и ограничение координат границами мира. Но клиент удалённых игроков использует округлённый целый шаг, не применяет sprint multiplier и не ограничивает координаты границами. Поэтому совпадение с серверным интегратором **не означает**, что запись безопасно пропустить для клиента.

Воспроизведены два случая при скорости 5 единиц/тик:

- Игрок упирается в X=1000 и продолжает держать движение вправо. Сервер остаётся на 1000 и подавляет запись; клиент предсказывает 1005, затем продолжает уходить за границу.
- Игрок спринтует с множителем 1.5 из X=100. Сервер получает 107 и подавляет запись; клиент получает 105.

Дробные скорости и диагональные движения также имеют разные правила округления. `MoveRemainderMilli` отсутствует в snapshot, поэтому даже простого переноса формулы на клиент недостаточно для точного продолжения произвольного baseline. Keyframes/full sync периодически исправляют координаты, но между ними ошибка снова растёт. При divisor=100 и 20 Hz период отдельного keyframe приблизительно 5 секунд без TiDi.

Исправление: специфицировать одну модель предсказания для получателя и серверного suppression. Передавать достаточное состояние интегратора либо отправлять абсолютные коррекции, когда именно клиентская модель отклоняется. До этого безопаснее сохранить коррекции для sprint/fractional/clamp, чем подавлять их на основании серверной формулы. Проверять реальный клиентский алгоритм на сотнях тиков, разных baseline, границах и TiDi.

**2. P1 при включённом byte budget — ACK могут полностью вытеснить состояние мира**

Код: [ACK перед fanout](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:766), [usedBytes и проверка бюджета](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:861), [enqueueAuthoritativeMovementAcks](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:980).

ACK ставятся в очереди до проверки бюджета. Теперь `usedBytes` начинается с их размера. Условие, ранее допускавшее хотя бы первый слишком большой state frame при нулевом расходе, перестаёт срабатывать после любого ACK. Если остатка не хватает на full recovery, все такие получатели пропускаются; `needsFullState` остаётся установленным. Последовательность мира и общий baseline при этом могут продолжать продвигаться.

Воспроизведение: для одного игрока полный кадр с roster занимает 41 байт. Бюджет установлен ровно в 41 — отдельный snapshot в него помещается. При одном ACK на каждом тике за десять тиков не отправлено ни одного world state. ACK продолжают отправляться. Это не требует невалидного конфига. Для большой зоны аналогичный случай — сумма ACK оставляет меньше места, чем один необходимый full snapshot. При бюджете 0 проблема не активируется; отсутствие ACK на следующих тиках также может восстановить прогресс.

Кроме того, ACK сами способны превысить этот бюджет: они уже поставлены в очередь до сравнения.

Исправление: определить отдельный резерв для ACK и гарантированный прогресс snapshot/recovery. Если бюджет меньше минимально необходимого кадра, выбрать явное поведение: разрешённое ограниченное превышение, накопление бюджета между тиками или отказ от такого режима. Число выбранных получателей нельзя рассчитывать только по маленькой общей дельте, когда фактически им нужны полные снимки.

**3. P2 — сортировка snapshot подменяет дробные остатки игроков в baseline**

Код: [параллельные scratch-массивы](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:620), [сохранение baseline](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:680), [сортировка encoder](/home/qwezert/projects/pixi_node_game/src/server/internal/protocol/binary.go:79).

`scratchStates` и `scratchRemainders` заполняются в одинаковом порядке обхода map. Encoder сортирует переданный `scratchStates` на месте по ID при full sync или ленивом recovery. Остатки не сортируются. После возврата из broadcaster сохранение по индексу связывает ID одного игрока с остатком другого.

В тесте через настоящий `gw.tick()` и `AppendGameState` получено: у ID=1 фактический остаток 100, в `prevMoveRemainder[1]` записано 800. Ошибка повторяется при снимках с непорядковым входным массивом. Она влияет на подавление/добавление дельт, а не меняет непосредственно authoritative координаты; поэтому это отдельный P2, а не доказательство большого скачка позиции сервера.

Исправление: хранить остаток вместе с состоянием либо сохранять baseline по ID/исходным Player pointers; encoder не должен неожиданно менять массив вызывающего кода. Нужен тест полного тика с разными остатками и последующим recovery, а не только тест формулы `classifyDelta`.

**4. P2 — AttackStartTick добавлен в пакет, но новое действие всё ещё теряется**

Код: [выбор дельт](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:707), [завершение и buffered attack](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:890), [клиентская обработка атаки](/home/qwezert/projects/pixi_node_game/src/client/network/networkManager.ts:438).

`AttackStartTick` не входит в `unpredictable`. Когда предыдущая атака заканчивается и buffered attack начинается в том же тике, State остаётся Attacking. При одном combo step ComboStep также остаётся 1. Если игрок неподвижен, запись подавляется. Тест подтверждает: замена start tick с 10 на 20 при одинаковых остальных полях не создаёт дельту.

Даже пришедший keyframe не решает вторую половину проблемы: decoder сохраняет `attackStartTick`, но потребители не используют его. Callback атаки запускается только при изменении attacking/comboStep. Входящий посреди атаки игрок также не восстанавливает фазу по времени начала; анимация запускается с начала либо зависит от порядка создания visual.

Исправление: использовать start tick/action sequence как идентичность действия и критерий репликации. Клиент должен различать новое действие и повторный snapshot старого, рассчитывать текущую фазу по worldTick/TiDi. Только увеличение wire record на 4 байта этого не обеспечивает.

**5. P2 — PATCH не различает null и отсутствующее поле**

Код: [request DTO](/home/qwezert/projects/pixi_node_game/src/server/internal/server/admin.go:22), [mergeUnitStatsPatch](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/settings.go:298), [редактор отправляет null](/home/qwezert/projects/pixi_node_game/src/client/debug/unitViewerPanel.ts:424).

Пропущенные обязательные поля теперь действительно сохраняются — прежнее обнуление исправлено. Но для optional fields и abilities `null` декодируется в такой же nil, как отсутствие поля. Merge применяет только non-nil. Отключение Block в редакторе отправляет `{"block":null}`, сервер сохраняет старый Block и может вернуть успех. Аналогично нельзя очистить optional стоимость атаки и другие nullable значения.

Тест JSON-декодирования подтвердил одинаковый результат для `{}` и `{"block":null,"attackStaminaCost":null}`. Дальнейшее сохранение старого значения видно в merge. Вложенные объекты заменяются целиком: частичный `block` обнуляет его непереданные обязательные поля; это надо явно закрепить в API либо реализовать вложенный merge.

Исправление: трёхсостоянийное поле «отсутствует / null / значение», например собственный Optional с presence flag. Проверять на полном пути HTTP → merge → БД → read back включение, изменение и удаление способности.

**6. P2 — два PATCH могут молча потерять изменения друг друга**

Код: [чтение текущего unit](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/settings.go:443), [UPDATE всех колонок](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/settings.go:516).

Операция состоит из SELECT, merge в памяти и полного UPDATE без транзакционной блокировки или revision. Два запроса читают старые HP=100/Damage=10. Один сохраняет HP=120, другой — Damage=20 вместе со старым HP=100. Оба успешно завершаются, первое изменение потеряно. Валидация итогового объекта это не предотвращает.

Это вывод из последовательности SQL, конкурентный тест с настоящим Postgres не проводился. Исправление: транзакция с блокировкой строки или optimistic locking по revision и конфликт 409. Если обновлять только переданные колонки, всё равно нужно атомарно проверять межполевые инварианты.

**7. P2 — уведомления о конфиге не гарантируют сходимость**

Код: [SetKey/Watch](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:121), [WatchUnits](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:201), [startup load → subscribe](/home/qwezert/projects/pixi_node_game/src/server/cmd/server/main.go:102).

После записи в Postgres отдельно выполняется Publish. Сбой между операциями оставляет новую БД и старый runtime. После ошибки чтения уведомлённого ключа watcher только пишет лог; нет retry/reconciliation. Между начальной загрузкой и подпиской также есть окно пропуска. Последующая подписка не перечитывает всё состояние. Это видно из кода независимо от конкретной причины недоставленного уведомления.

Итог — разные инстансы могут продолжать использовать разные настройки до следующего удачного изменения/рестарта. Ответ admin и текст configctl о применении не подтверждают фактическую applied revision.

Исправление: revision в БД, загрузка целого проверенного снимка и периодическая сверка runtime revision. Уведомление использовать для ускорения сверки, а не как единственное доказательство изменения. Для статуса различать persisted и applied. Отказ Redis сейчас блокирует даже запуск/чтение через configctl, хотя исходные данные находятся в Postgres; эту зависимость стоит обосновать отдельно.

**8. P2 — configctl не может исправить невалидный конфиг; изменения нескольких ключей неатомарны**

Код: [Store.SetKey](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:121), [LoadInto](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:150).

SetKey сначала загружает и валидирует все уже сохранённые значения, а только затем применяет исправляющий ключ. Если в БД уже лежит неверный `max_connections`, ошибка LoadInto не позволяет заменить его правильным значением штатной командой. Аналогично неизвестный старый ключ способен заблокировать все изменения. В сочетании с fail-fast startup это затрудняет восстановление.

Два одновременных SetKey валидируют изменения относительно одной старой версии, а записывают разные ключи без общей транзакции. Например, стартовые min/max recipients=256/1000; отдельно допустимы min=800 и max=500, но вместе получается невалидный конфиг. Watcher применяет ключи по одному и может отвергнуть часть изменений.

Исправление: сначала загрузить сырые значения, наложить весь patch, затем валидировать результирующий snapshot и атомарно сохранить с revision. Это позволит и чинить неверную строку, и менять связанные ключи одним действием. Валидацию привязать к конфигу нужного инстанса: сейчас configctl использует собственный env, который может отличаться от env игрового процесса.

**9. P2 — hot reload не синхронизирует баланс существующих игроков и клиентов**

Код: [reload callback](/home/qwezert/projects/pixi_node_game/src/server/cmd/server/main.go:113), [RecomputeUnitTables](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:160), [одноразовая загрузка клиента](/home/qwezert/projects/pixi_node_game/src/shared/units.ts:119).

Хорошее изменение: Recompute теперь берёт stepMu, поэтому tables не меняются посреди тика. Но definitions, tables и HTTP JSON всё ещё публикуются отдельными операциями. Главное: подключённые клиенты не получают новый баланс, а сервер уже использует новую скорость/стоимость/длительность. Обновление `/api/units` не меняет загруженный модуль клиента.

Нет явной миграции состояния: уменьшение максимальной stamina не ограничивает уже имеющуюся (RegenStamina просто возвращает при current ≥ max), изменение HP не меняет существующих Player.HP, длительность активной атаки начинает определяться новой таблицей. Политика может быть любой, но должна быть единой и явной.

MessageRateLimit/BurstLimit и WriteBatchSize захватываются при создании соединения/writer. IP limiters сохраняют параметры до удаления. Изменение live-конфига не перенастраивает эти объекты.

Исправление: versioned bundle баланса с активацией на границе тика, уведомление клиентов о revision и правила миграции текущих действий/ресурсов. Для сетевых настроек указать, какие применяются сразу, какие только при reconnect; либо реализовать обновление действующих limiters.

**10. P2 — длительный shedding не запускает предусмотренное отключение**

Код: [enqueueBroadcastJob](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:184).

Ветви queue-depth shed и pending-broadcast теперь увеличивают fanoutDrops, но не проверяют порог. Проверка осталась только в ветви отказа enqueue и использует `==`, а не `>=`. После пересечения порога через shedding последующий enqueue failure также способен его пропустить.

Воспроизведение: порог 2, пять попыток при pendingBroadcast=1 — счётчик 5, cleanup не запрошен. Это не означает вечный TCP-сокет: deadline writer/read по-прежнему может его закрыть. Но конкретная политика `FanoutDropStreak` не работает для основных путей пропуска.

Исправление: единая обработка всех действительно неуспешных state enqueue с порогом `>=`; ещё лучше задавать предел возрастом последнего успешно отправленного состояния. Решить, входят ли в этот счётчик пропуски по общему бюджету: сейчас это отдельный путь.

**11. P2 — ограничение fanout может превращать каждую отправку в полный мир**

Код: [needsFullState после пропуска](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:889), [full frame вместе с roster](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:935), [membershipChanged](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:552).

Исправление baseline корректно требует full snapshot после любого пропущенного sequence. Но при recipient cap ниже N клиент систематически пропускает кадры и почти каждый следующий получает full recovery. Общая стоимость перестаёт соответствовать маленькому `changed` массиву, на котором рассчитывается selection budget. Любой join/leave также делает следующий tick полным для всех получателей. Под reconnect churn это может свести на нет экономию дельт.

Это ограничение выбранной схемы, а не возврат старой ошибки «после gap продолжаем обычную дельту». Общий recovery encode один на тик — полезное улучшение, но сетевой объём всё равно умножается на число получателей. Fair debt уменьшается уже для выбранного получателя, даже если позже его отсекает реальный byte budget; метрика выбранных тоже не равна успешным enqueue.

Исправление: измерять full/recovery/delta bytes отдельно, обновлять fairness по фактическому обслуживанию. Для устойчивого throttling нужны независимые baseline/версии сущностей на получателя или репликационную группу, либо самодостаточные снимки AOI. Проверять cap<N и churn, а не только стабильную популяцию.

**12. P2 — roster клиента продолжает накапливать ушедшие ID; ресурсы приходят редко**

Код: [Object.assign roster](/home/qwezert/projects/pixi_node_game/src/client/network/networkManager.ts:327), [full state replacement](/home/qwezert/projects/pixi_node_game/src/client/network/networkManager.ts:452), [server roster](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:939).

В серверном full snapshot теперь согласованы состав мира и roster. Однако клиент по-прежнему дополняет `playerAttributes` через Object.assign. Удаление ID из `players` по full state не удаляет его атрибуты. Сессия с длительным churn растит эту таблицу до disconnect. Это оставшаяся часть прежнего пункта 15.

Stamina меняется на сервере каждый тик, но authoritative значение передаётся через roster при full snapshot. Movement ACK содержит только позицию/sequence. Предсказание stamina помогает отображению, однако отказ атаки из-за ресурса/изменения баланса не получает отдельной быстрой коррекции ресурса. HP пока фактически не участвует в серверном нанесении урона.

Исправление: полный roster должен заменять полный набор атрибутов согласованно со state; ресурсы — реплицироваться при значимых изменениях с понятной точностью и частотой. Не отправлять весь roster каждому только ради изменения stamina одного игрока.

**13. P2 — TiDi не восстанавливается без рассылки**

Код: [ранний выход пустого мира](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:644), [ранние возвраты broadcastTick](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:739), [вызов tuneTimeDilation](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:913).

Текущий computeDur теперь измеряется до fanout текущего тика — прежний двойной учёт исправлен. Но регулирование по-прежнему вызывается только на пути фактической рассылки. После ухода всех игроков зона сохраняет замедление. При `VelocityReplication=false` и неподвижном мире восстановление зависит от редких full sync. При появлении следующего игрока он начинает в старом замедленном мире.

Исправление: вызывать регулятор один раз на тик независимо от наличия state packet, передавая нулевую/реальную стоимость fanout и отдельно текущую вычислительную нагрузку.

**14. P2 — средства protocol testing не соответствуют текущему серверу**

Код: [сохранённый decoder](/home/qwezert/projects/pixi_node_game/utils/testing/protocol/lib/proto.mjs:240), [run.sh](/home/qwezert/projects/pixi_node_game/utils/testing/protocol/run.sh:25), [config.mjs](/home/qwezert/projects/pixi_node_game/utils/testing/protocol/lib/config.mjs:9).

В Go и основном TS decoder протокол v13 содержит 11 фиксированных байтов после varint ID. Сохранённый `proto.mjs` всё ещё читает 7. На пакете, сгенерированном текущим Go encoder для ID 1001 и 1002, probe decoder получил ID **1001 и 1124**, вторую позицию (0,256) вместо (300,400). Также его flags/encodeMove отстали от sprint/combo/block.

Обычный runner должен пересобрать decoder, но сам устарел: staging и config.mjs читают отсутствующий `src/shared/gameConfig.json`; свежая конфигурация живёт в HTTP/БД. Нет изолированных Postgres/Redis, подтверждения PID/revision процесса на health endpoint и `set -e`. Поэтому этот runner не запускался против ваших работающих сервисов и не может считаться пройденной интеграционной проверкой.

Исправление: запускать выделенное окружение с fixture-конфигом, случайными портами и проверкой собственного процесса; генерировать decoder в `/tmp`, не перезаписывать исходное дерево. Добавить общий corpus бинарных пакетов Go→реальный TS decoder, плюс проверки конечных координат/roster/действий. Header-only latency probe и Go smoke полезны, но не обнаруживают неверное содержимое игрока.

**15. P2 — конфигурация по умолчанию не соответствует заявленному cap 1200**

Код: [SQL defaults](/home/qwezert/projects/pixi_node_game/docker/postgres/init/001_init.sql:24), [Build](/home/qwezert/projects/pixi_node_game/src/server/internal/config/config.go:189), [IP limiter](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:582).

В репозитории max_connections=12000, SQL rate_limit_msg_sec=12000 и burst=20000; IP connection rate в SQL отключён. Это не утверждение о вашей действующей БД: её значения не запрашивались. Но новый стандартный deploy получает лимиты, существенно отличающиеся от описанного soft cap и обычного ввода 20 Hz. Сохраняемые SQL-значения перекрывают env при LoadInto, а стартовый лог показывает cfg до этого переопределения.

RemoteAddr за reverse proxy может быть одним адресом для всех игроков. Включение IP limiter без trusted-proxy policy тогда ограничивает общий proxy. Полная очистка map каждые пять минут сбрасывает активные buckets; ограничения cardinality внутри окна нет. Stale MOVE логируется поштучно, а серия accepted/stale позволяет регулярно сбрасывать streak до отключения — при высоких лимитах это лишняя нагрузка на логирование.

Исправление: согласовать production defaults с реальной admission policy, показывать effective config/revision после LoadInto, разделять ingress и per-player ограничения. Для proxy применять только проверенную цепочку адресов; stale-логи агрегировать или семплировать. Сам reservation cap теперь строгий — прежний TOCTOU закрыт.

**16. P2 — health и жизненный цикл служебных задач ещё не полны**

Код: [handleHealth](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:596), [main](/home/qwezert/projects/pixi_node_game/src/server/cmd/server/main.go:28), [Start](/home/qwezert/projects/pixi_node_game/src/server/internal/server/server.go:235).

`/health` проверяет stopping и возможность прочитать число игроков; не проверяет продвижение worldTick/время последнего завершённого тика. Зависший worker может оставить HTTP «healthy». Это важно для автоматического вывода инстанса из маршрутизации.

WebSocket lifecycle существенно исправлен и покрыт тестами, но watchers живут вне Server.WaitGroup. Отмена liveConfigCtx отложена через defer, а закрытие store зарегистрировано позднее и выполняется раньше отмены; завершение callback не ожидается. Большинство ошибок main заканчивает os.Exit, обходя cleanup. Для отдельного процесса ОС освобождает ресурсы, но такой lifecycle неудобен для повторного запуска компонента в тесте/встраивании. Start после ошибки bind также оставляет New-запущенный мир до явного Shutdown/выхода процесса.

Исправление: разделить liveness/readiness, учитывать прогресс симуляции и draining; обернуть main в run(ctx) с единым порядком cancel → wait watchers → close store, гарантированным Shutdown при ошибках запуска. Не объявлять весь сервер unhealthy на любой краткий сбой Redis, если мир способен продолжать безопасную симуляцию.

**17. P2 — пространственный индекс по-прежнему не используется рассылкой**

Код: [VisibilityManager](/home/qwezert/projects/pixi_node_game/src/server/internal/systems/visibility.go:17), [updatePlayerPosition](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:854).

Вызовы Add/Move/Remove есть, query нет. Индекс выполняет sync.Map lookup и операции с ячейками, но не уменьшает список получателей/сущностей. Это живой код без функционального потребителя. Переполнение размеров исправлено расширением арифметики до uint32; прежняя гонка Remove с tick в штатном пути устранена stepMu. Эти старые ошибки повторно не заявляю.

Если инстанс сознательно all-to-all, индекс можно временно убрать из горячего пути. Если нужен AOI, реализовать запросы и интерес клиента с граничным запасом, spawn/despawn и корректной сменой baseline. AOI не уменьшит стоимость самого вашего worst case, где все 1200 видят друг друга.

**18. P2 — метрики recovery не показывают реальную цену восстановления**

Код: [наблюдение payload/records](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:798), [ленивый recovery](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:868), [fairness](/home/qwezert/projects/pixi_node_game/src/server/internal/server/broadcast.go:536).

BroadcastPayloadBytes/BroadcastRecords наблюдают исходный f до создания recovery. Если `changed` пустой, а получателям нужны полные миры, график records показывает ноль при отправке тысяч записей. BytesSent отражает фактическую запись и остаётся полезным, но по текущим метрикам нельзя объяснить её рост.

Добавленный result=success/error у age-at-write-end — правильное изменение. Dashboard-запрос не фильтрует result и теперь выдаёт отдельные серии; их следует явно подписать/разделить, особенно при сравнении со старыми графиками. `range` и `world_step` всё ещё наблюдают один и тот же интервал, включая ожидание workers; это не независимые фазы.

Исправление: packet kind, фактические records/bytes по типу кадра и результат queued/shed/budget-trimmed, количество full recovery и возраст успешно обслуженного клиента. Чётко отличать завершение socket Write от доставки/применения браузером.

**19. P2/P3 — оставшиеся границы валидации, runtime и хранения**

Код: [ValidateDefinition](/home/qwezert/projects/pixi_node_game/src/server/internal/units/validate.go:9), [SQL smallint](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/settings.go:528), [DSN](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:218), [optimizeRuntime](/home/qwezert/projects/pixi_node_game/src/server/cmd/server/main.go:160).

Основные опасные преобразования теперь защищены. Остаются конкретные несовпадения: вложенные int допускают 65535, но positional_min_nearby_allies, rogue_quiver_charges и fire_arrow_wood_cost_per_shot приводятся к SQL SMALLINT. Значения 32768..65535 проходят Go Validate и отвергаются БД. Группы nullable-колонок при загрузке определяются одной колонкой; неполная ручная SQL-запись может быть прочитана как способность с нулевыми остальными значениями. Нужны согласованные CHECK/типы и ограничения целостности групп.

DSN собирается конкатенацией URI: специальные символы пароля вроде `@`, `/`, `?`, `#` способны изменить разбор строки. Использовать структурированный pgx config или корректное URL-экранирование. Не включать строку с паролем в диагностику. `sslmode=disable` зафиксирован — для межхостового DB connection нужен настраиваемый transport policy.

GOMAXPROCS принудительно устанавливается в NumCPU при отсутствии env, что мешает делегировать выбор runtime для ограниченного контейнера; workers создаются в этом количестве. GOGC=400 выбран без измерения memory headroom, GOGC=off/0 не сохраняется, потому что принимается только положительное число. В Compose нет CPU/memory limits, часть образов latest. Настраивать это по измерениям RSS/GC/CPU throttling, фиксировать проверенные версии образов. Публикация DB/Redis/мониторинга на host loopback теперь исправлена; management game-порт отдельно не опубликован.

**Остатки старого кода и fallback**

| Место | Текущий статус / действие |
|---|---|
| `GameWorld.GetAllPlayers`, `GetTickDuration` | Нет вызовов в текущем Go-дереве. Первый всё ещё не гарантирует согласованный tick snapshot при конкурентном вызове. Удалить неиспользуемый API либо изменить контракт до появления нового потребителя. |
| `Player.LastActivity` | Не используется. Heartbeat читает отдельное поле Connection.lastActivity. |
| `MessageJoin`, `MessageLeave`, `EncodePlayerLeft` | В текущем серверном потоке не используются. Резервировать номера в спецификации допустимо; код не должен создавать впечатление работающего отдельного join/leave протокола. |
| `MessageAttackEnd`, `MessageViewportUpdate` | Decoder принимает, processMessage не обрабатывает. Viewport не создаёт AOI. Удалять совместно с отправителями/версией протокола или явно обозначить compatibility no-op. |
| 9-байтный ATTACK | После type сервер игнорирует 8 байт позиции. Оставить только нужные поля при следующем согласованном изменении протокола. Клиентской позиции нельзя доверять как authority атаки. |
| `WORKERS` в validateEnv | Настройки workers уже нет, но её env всё ещё валидируется. Невалидное значение неиспользуемой переменной способно сорвать startup. |
| `units.All()` / `LoadDefinitions()` | Возвращают/хранят исходный slice и вложенные pointers без копии. В текущих caller нет обнаруженной записи после публикации, поэтому это не подтверждённая гонка; контракт immutable нужно защитить или документировать. |
| `units.Get` fallback | Полезен для отсутствующего unit. Явно неверный unit теперь отклоняется на HTTP; не возвращать молчаливый fallback на этом пути. `IsValid` теперь реально используется — удалять его нельзя. |
| Client fallback выбора собственного ID из последних players | Welcome теперь обязателен и ставится первым. Старое угадывание ID в networkManager стоит убрать при закреплении строгого handshake. |
| `VelocityReplication=false`, full recovery | Нужны как диагностический режим и восстановление. Их удаление скроет проблемы, а не исправит контракт. |

Отдельно про epoll: прежние файлы **использовались Linux-сервером**, они не были мёртвыми. Их удаление было заменой модели чтения на goroutine-per-connection в [read.go](/home/qwezert/projects/pixi_node_game/src/server/internal/server/read.go:15). Это устраняет блокировку общего read pool на частичных кадрах. Go runtime на Linux сам использует epoll, что проверено в локальном `/usr/local/go/src/runtime/netpoll_epoll.go`. Возвращать старый общий блокирующий пул не требуется; доказательства превосходства новой схемы по CPU/RSS без сопоставимого A/B также нет.

**Что закрыто из предыдущего ревью**

| Старый пункт | Результат повторной проверки |
|---|---|
| 1: дельта после пропуска | Основной дефект исправлен: needsFull, общий recovery, sequence на соединение. Цена при throttling и новая ошибка бюджета — пункты 2/11 этого отчёта. |
| 2: partial write | Исправлен: первая ошибка/short write закрывает поток; очередь освобождается. |
| 3: боевые переходы | Сетевой слой ставит bounded actions, игровой worker применяет их. Старый конкурентный bypass устранён. |
| 4: shutdown | Producer/workers/socket cleanup и повторный Stop исправлены. Служебные watchers/readiness — оставшиеся отдельные замечания. |
| 5: public diagnostics/admin | Отдельный management listener, pprof opt-in, token auth и body limit. Исправлено на уровне репозитория. |
| 6: числовая валидация | Основные диапазоны/finiteness/конверсии исправлены. Остатки БД — пункт 19. |
| 7: PATCH | Оmitted поля сохраняются; null и конкуренция ещё не исправлены. |
| 8: аккаунты | Отдельный согласованный этап. Неизвестный unit теперь отвергается. |
| 9: cap TOCTOU | Исправлен reservation перед upgrade. |
| 10–11: epoll pool/fd reuse | Старый путь удалён при смене модели чтения. Проверка частичных кадров есть. |
| 12: SYNC_REQUEST | Отдельный limiter и coalescing; encode выполняется в tick и разделяется. Исправлено. |
| 13: control/IP | Control limiter исправлен. Proxy/effective limits остаются. |
| 14: внеочередной snapshot | Сетевой resync теперь выполняется в tick, старый несогласованный путь не используется. |
| 15: membership/roster | Серверное упорядочивание исправлено, клиентский attributes merge и частота ресурсов — нет. |
| 16: grid | Арифметика и штатный remove race исправлены; потребителя индекса нет. |
| 17: бесшовность | Отдельный согласованный этап. |
| 18–19: reload | Отказ от невалидного snapshot улучшен; доставка revision/клиентское применение не завершены. |
| 20: регулятор | Возврат результата shedding и computeDur улучшены; threshold/idle TiDi и budget требуют исправлений. |
| 21: предиктор | Серверная формула уточнена, но породила клиентскую регрессию и ошибку remainder association. |
| 22: боевой timing | Поле добавлено; его end-to-end использование не реализовано. |
| 23: бой/persistence | Объём реализованной симуляции по-прежнему ограничен. |
| 24: эксплуатация | Закрыты публичные host-порты вспомогательных сервисов; readiness/runtime defaults остаются. |
| 25: метрики | Разделён outcome записи; recovery/fairness/фазы требуют уточнения. |

**Архитектура и реальная граница масштабирования**

Подход с отдельным сервером участка остаётся разумной единицей симуляции и отказа. Сначала стоит завершить контракты существующего инстанса: команды → tick → неизменяемый снимок с revision → доставка. `world.go` сейчас соединяет scheduler, gameplay, baseline/delta policy и связь с transport callback; `broadcast.go` — codec, pools, writer, выбор получателей и регуляторы. Разделять лучше по этим обязанностям, постепенно, без тотального переписывания.

Параллельные workers сейчас безопасны главным образом потому, что каждый меняет своего игрока. Нельзя просто добавить в них прямое уменьшение HP чужой цели: тогда два worker начнут менять один игровой переход. Для боя нужны фазы расчёта намерений/попаданий и детерминированного применения либо явное владение целями. Сейчас в игровом Go-коде нет полноценного pipeline попаданий/урона/смерти, projectiles, persistent economy. Поля Damage/Range в definitions не означают, что эта нагрузка уже включена в тест 1200 игроков.

Новый плотный world record занимает обычно около 12 байт вместо 8: varint delta ID + 11 фиксированных байтов. Если все 1200 сущностей меняются для всех 1200 получателей 20 раз/с, только world payload составляет приблизительно `1200 × 1200 × 12 × 20 = 345.6 MB/s = 2.7648 Gbit/s`, без roster/TCP/TLS. Это расчёт сценария, не измерение текущего среднего трафика. Shared encoding уменьшает CPU, но не этот сетевой множитель. Полный roster добавляет ещё объём.

Для десятков тысяч игроков важны распределение плотности, резерв для handoff и горячие точки, а не только сумма cap. На следующем этапе нужны: global identity; owner/epoch и fencing; идемпотентный handoff с резервом target; ghost entities у границ; разрешение межзонного боя и TiDi; долговечные события/сохранение. Отсутствие этих подсистем здесь отмечено как граница текущей архитектуры, а не как невыполненная согласованная задача исправить P1 инстанса.

**Проверки и их пределы**

| Проверка текущего коммита | Результат |
|---|---|
| `GOCACHE=/tmp/pixi-go-review-cache go test -race ./...` | PASS всех пакетов. Опциональный load test без GAME_LOAD_CLIENTS пропускается. |
| `GOCACHE=/tmp/pixi-go-review-cache go vet ./...` | PASS. |
| `./node_modules/.bin/tsc --noEmit` | PASS. |
| `node --test utils/testing/artillery/latency-probe.test.cjs` | PASS. |
| Изолированные review-тесты Go с `-race` | Подтверждены шесть дефектных сценариев: predictor mismatch, attack suppression, remainder association, ACK starvation, shed threshold, PATCH null. PASS этих тестов означает воспроизведение ошибки. |
| Текущий Go wire → сохранённый probe decoder | Подтверждены неправильные ID/позиция второго игрока. |
| `GAME_LOAD_CLIENTS=1200 GAME_LOAD_SECONDS=15 go test ./internal/server -run '^TestLoadConnections$' -count=1 -v` | PASS: плато 15.019 s; 360000 state frames; 36000 ACK; gaps=0; errors=0; dilation=100%; полученный payload 38.61 MB/s. Shutdown завершён. |

Load smoke использовал fixture-unit spearman, 20 Hz, мир 1000×1000, 1200 локальных соединений, синхронные смены движения раз в 500 ms. Клиенты и сервер работали на одном хосте. Он не читает весь world record, не исполняет браузерный dead reckoning, не измеряет p99 end-to-end latency и не включает полноценный бой. Это короткая транспортная проверка текущего коммита, не повтор вашего длительного capacity test и не сравнение epoll/reader goroutines. Результат совместим с найденными ошибками.

Тестовые исходники и результаты находятся в изолированной копии: [game/review_test.go](/tmp/pixi-review-20260910-tnIssp/internal/game/review_test.go), [server/review_test.go](/tmp/pixi-review-20260910-tnIssp/internal/server/review_test.go), [protocol/review_test.go](/tmp/pixi-review-20260910-tnIssp/internal/protocol/review_test.go), [review-results.txt](/tmp/pixi-review-20260910-tnIssp/review-results.txt), [load log](/tmp/pixi-review-20260910-load.log). Это временные файлы. Повторный запуск воспроизведений из той копии: `GOCACHE=/tmp/pixi-go-review-cache go test -race ./internal/game ./internal/server ./internal/protocol -run TestReview -v`.

Действующие БД/Redis, `.env`, пользовательские процессы и серверные исходники не изменялись. Postgres-конкуренция, перебои Redis, Docker deployment и браузерный визуальный тест не выполнялись; соответствующие замечания основаны на коде и отдельно описанных interleavings. Аудит известных уязвимостей всех зависимостей в эту проверку не входил.

Практический порядок исправлений: сначала пункты 1–4 вместе с Go→TS тестами; затем PATCH/null/concurrency и revision reload; затем восстановление под cap/budget/churn, метрики и эксплуатационные defaults. Только после этого сравнивать производительность протокола и модели чтения на одинаковых сценариях.
