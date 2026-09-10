**Повторное ревью после исправлений — коммит d4a62d7, 10 сентября 2026**

Проверен `d4a62d72c58fed153ad4e71c6a2d1b639fe725d9`, включая изменения `33f3039` относительно предыдущего ревью `50da036`. Рабочее дерево перед проверкой было чистым. Проверены изменения сервера, связанные клиентские потребители, конфиг и тестовые инструменты; оставшиеся замечания прошлого отчёта повторно сверены с кодом. Исходники приложения не менялись. Аккаунты и распределённая бесшовность остаются отдельным согласованным этапом.

**Вывод:** исправления действительно закрыли несколько проблем, включая ACK starvation, соответствие remainder/ID, PATCH null, конкурентные PATCH unit, idle TiDi и tick watchdog. Однако P1 предсказания движения закрыт не полностью. Новая схема валидации configctl допускает сохранение невалидного конфига, а `set-many` не обеспечивает согласованное применение на работающем сервере. Подробности и статус каждого прежнего пункта ниже.

**1. P1 — допуск в предсказании накапливает ошибку вместо ограничения её одной единицей**

Код: [epsilon](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:699), [клиентская оценка](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:733), [обновление baseline](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:675), [реальный deadReckon](/home/qwezert/projects/pixi_node_game/src/client/game/playerManager.ts:116).

Предиктор теперь сравнивает серверное движение с приблизительной клиентской формулой — это правильное направление. Но отклонение `<=1` разрешается на каждом тике. После отправки пустой дельты сервер сохраняет **свою** новую позицию в prevStates. Клиент продолжает от **своей предсказанной**, уже неточной позиции. Накопленную разницу сервер в следующем сравнении больше не видит.

Воспроизведение на текущем `classifyDelta`: скорость 3.5 единицы/тик, клиент округляет до 4. За 90 тиков сервер доходит из X=100 до **415**, клиент — до **460**. Все 90 записей подавлены, ошибка **45 единиц**, а не 1. Для длительного движения между keyframes ошибка будет накапливаться заново. Тест изолирует suppression без keyframes; обычные keyframes ограничивают время накопления, но не устраняют дефект между ними. При шаге 1 та же проблема сохраняется у границы мира: ожидаемый клиентский выход за границу ровно на 1 каждый тик разрешается.

Есть и второе несовпадение: clientUnitsPerTick вычисляется из уже округлённого server milliRate. Настоящий клиент округляет исходный `moveSpeed * unitsPerMeter / tickRate`. Двойное округление около половинной границы может дать другую скорость.

Исправление: для точного контракта не разрешать пропуск даже при разнице 1, пока baseline остаётся authoritative. Если нужен ненулевой error budget, хранить и продвигать именно клиентское предсказанное состояние/накопленную ошибку между коррекциями. Самую клиентскую формулу получать из одной спецификации; проверять цепочки из сотен тиков, разные скорости, диагональ, sprint и границы. Одно сравнение на трёх тиках не проверяет этот инвариант.

**2. P2 — configctl проверяет другой конфиг, чем сохраняет в БД**

Код: [resolveLiveNetPatch](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:189), [проверка и запись только patch](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:148).

Вместо `raw → наложить patch → Validate всей комбинации` новый resolver сначала валидирует patch относительно env/defaults, затем пробует добавлять raw-ключи по одному и пропускает те, которые не проходят проверку. Возвращённый «исправленный» snapshot вообще не сохраняется: SetKeys записывает только patch. Поэтому validator и persisted config могут описывать разные значения.

Подтверждены оба направления ошибки:

- При baseline min/max=256/1000, сохранённых min/max=256/500 и patch min=800 resolver возвращает допустимые **800/1000**, отбросив сохранённый max=500. Но в БД SetKeys оставит **800/500**. Watcher отвергнет такое сочетание, а строгий startup LoadInto не сможет его загрузить. То же возможно при baseline max=0, то есть без ограничения.
- При сохранённых min/max=100/500 корректное изменение max=200 отвергается: patch проверяется против default min=256 раньше, чем загружается сохранённый min=100.

Это воспроизведено на чистой функции resolver и проверке получающейся комбинации; запись в настоящую БД не выполнялась. Новый тест `TestResolveLiveNetPatchAllowsUnrelatedKeyDespiteInvalidLegacyRow` закрепляет пропуск неверной строки, но не проверяет, что итоговая БД потом загружается через LoadInto.

Исправление: собрать полную сырую карту, наложить patch, преобразовать все поля и один раз проверить результат. Неверный ключ можно исправить, заменив его до проверки. Нельзя молча игнорировать неизменяемое неверное значение и утверждать, что сохранённый конфиг валиден. Если нужен специальный recovery mode с удалением старых ключей, он должен явно сохранять именно проверенный результат.

**3. P2 — транзакция SetKeys начинается после чтения и проверки: гонка между writers осталась**

Код: [LoadAll до Begin](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:148), [Begin](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:156).

Транзакция теперь атомарно записывает несколько ключей одного запроса. Однако чтение общей конфигурации и проверка выполняются **до** неё, без блокировки общей revision. Два запроса, меняющие разные связанные ключи, могут проверить одну старую версию и успешно записать вместе несовместимые значения. Транзакции на разных строках этого не предотвращают. Исправление resolver из пункта 2 само по себе не устраняет эту гонку.

Исправление: чтение → merge → validate → запись должны быть одной сериализованной операцией над конфигом нужного scope. Подойдёт блокировка строки конфигурационной revision/документа либо serializable transaction с обработкой retry. Зафиксировать порядок блокировок при нескольких строках. Нужен интеграционный тест двух независимых DB-соединений; текущие тесты проверяют только in-memory resolver.

Для `UpdateUnitStats` аналогичная прежняя проблема **исправлена**: SELECT FOR UPDATE и UPDATE выполняются в одной транзакции. Эти два пути нельзя считать одинаково защищёнными.

**4. P2 — set-many атомарен в БД, но не в runtime**

Код: [публикация каждого ключа](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:174), [Watch применяет по одному](/home/qwezert/projects/pixi_node_game/src/server/internal/liveconfig/store.go:231), [сообщение configctl](/home/qwezert/projects/pixi_node_game/src/server/cmd/configctl/main.go:130).

После commit отправляется несколько уведомлений в порядке обхода Go map. Watcher перечитывает и валидирует только отдельный ключ. Переход с min/max=100/200 на 800/1000 допустим целиком, но если первым приходит min, runtime отвергает 800 относительно старого max=200. Затем принимает max=1000 и остаётся на **100/1000**, хотя БД содержит **800/1000**. Обратный порядок работает. Повторной сверки нет.

Такой сценарий воспроизведён через те же `LiveNet.Update`, которыми пользуется watcher, в обоих порядках уведомлений. Redis для воспроизведения не нужен; это ошибка контракта применения, а не предположение о потере пакетов.

Исправление: одно уведомление о revision и атомарная загрузка **целого** проверенного snapshot. Добавить периодическую сверку, чтобы пропуск уведомления или ошибка чтения не оставляли старый runtime навсегда. Прежний разрыв между DB commit и Publish тоже остаётся. Текст «applied atomically (all connected servers notified)» не означает подтверждённое применение на всех процессах.

**5. P2 — повторная атака всё ещё может не попасть в дельту**

Код: [unpredictable](/home/qwezert/projects/pixi_node_game/src/server/internal/game/world.go:722), [новая клиентская обработка](/home/qwezert/projects/pixi_node_game/src/client/network/networkManager.ts:447).

Клиент теперь учитывает изменение attackStartTick и умеет передать elapsedMs в анимацию. Но на сервере критерии `classifyDelta` по-прежнему сравнивают только State/Direction/Sprinting/ComboStep и движение. `AttackStartTick` в сравнении отсутствует.

Повторное воспроизведение: Attacking/ComboStep=1 и одинаковая позиция, start tick меняется с 10 на 20 — запись подавляется. Buffered attack того же combo step в тике завершения предыдущей атаки остаётся невидимой до иной причины отправить запись. Теперь keyframe с новым start может восстановить событие на клиенте, но это запоздалое восстановление, не своевременная репликация.

Исправление: включить идентичность действия в критерий delta; проверить последовательность «конец старой атаки + начало новой в одном тике», а не только изменение Player в gameplay-тесте.

Оставшееся ограничение анимации: elapsedMs привязан к simulation ticks, а AnimatedSprite продолжает воспроизведение с постоянным animationSpeed; TiDi не управляет скоростью remote animation. Длительность по числу sprite frames также не обязана совпадать с серверным windup+active+recovery. Для точной боевой фазы нужен общий simulation-time контракт. Визуальный браузерный тест этого изменения не проводился.

**6. P2 — создание remote player не инициализирует состояние для подавляемых дельт**

Код: [constructor и пустые snapshots](/home/qwezert/projects/pixi_node_game/src/client/game/playerManager.ts:40), [createRemotePlayer](/home/qwezert/projects/pixi_node_game/src/client/game/playerManager.ts:442), [decoder](/home/qwezert/projects/pixi_node_game/src/client/network/protocol/binaryProtocol.ts:266).

Это обнаруженная при повторной проверке ошибка существующего клиентского потребителя, а не новая ошибка серверного encoder. World decoder отдаёт `vx`/`vy`. Создание RemotePlayer читает только старое optional `movementVector`, которого в world record нет. Вектор остаётся (0,0). Кроме того, constructor/createRemotePlayer не добавляют начальный snapshot, а `deadReckon` возвращает при пустом snapshots.

Если новый наблюдатель получил full state уже движущегося игрока и следующие записи подавлены как предсказуемые, remote может стоять на месте до следующей явной записи/keyframe. Это отдельный дефект от накопления epsilon: даже полностью совпадающие интеграторы его не исправят.

Во время `await loadVisualFor` новые updates не применяются к ещё отсутствующему remote; после await используется исходный playerState. Новое восстановление атаки также использует worldTickAtJoin, захваченный до загрузки. За это время игрок мог остановиться или закончить атаку.

Исправление: после загрузки перечитать актуальный state/sequence/tick, установить `vx`/`vy`, добавить первый snapshot и только затем включить объект в обновление. Для позиции, которая за время загрузки существовала лишь как предсказание, нужен актуальный клиентский baseline или запрос коррекции. Проверить наблюдателя, подключающегося во время постоянного движения, и отложенную загрузку visual. Вывод основан на коде и форме результата действующего decoder; полноценный Pixi-сценарий не запускался.

**7. P2 — protocol probes всё ещё не проверяют реальное предсказание клиента**

Код: [SPEED и интеграция harness](/home/qwezert/projects/pixi_node_game/utils/testing/protocol/lib/harness.mjs:34), [проверка после STOP](/home/qwezert/projects/pixi_node_game/utils/testing/protocol/probes/dead-reckoning.mjs:24), [determinism](/home/qwezert/projects/pixi_node_game/utils/testing/protocol/probes/determinism.mjs:28).

Runner теперь загружает HTTP-конфиг и пересобирает актуальный decoder. Проверка собранного TS decoder на двух Go wire records успешно прочитала ID и AttackStartTick — прежний баг layout при корректной пересборке закрыт.

Но harness предсказывает `position += v * SPEED * elapsed`, где SPEED — дробная серверная скорость. Реальный клиент использует округлённую скорость и отдельное диагональное округление. Поэтому probe может скрыть именно проблему пункта 1 или показать ложную ошибку на диагонали. Нет проверки той же модели по разным типам unit/sprint.

Сам `dead-reckoning.mjs` проверяет координату только после STOP и ожидания 900 ms. STOP меняет velocity и принудительно присылает абсолютную позицию; ошибку, которая росла всё движение, такая конечная проверка стирает. `determinism.mjs` всё ещё требует точное STEPS*SPEED по wall-clock sleep и жёстким 20 Hz, хотя ввод не содержит requested simulation tick; результат зависит от фазы тика, задержек и округления.

Исправление: сравнивать authoritative и восстановленные позиции на одинаковом worldTick **во время движения**. Использовать общий чистый модуль клиентского predictor; сопоставлять ACK со временем применения, не со временем sleep. Корпус Go→TS проверяет формат, но не заменяет проверку правильности поведения клиента.

**8. P2/P3 — изоляция нового protocol runner неполная**

Код: [fallback при отсутствии Docker](/home/qwezert/projects/pixi_node_game/utils/testing/protocol/run.sh:54), [DB_ENV](/home/qwezert/projects/pixi_node_game/utils/testing/protocol/run.sh:107), [фиксированный WORK](/home/qwezert/projects/pixi_node_game/utils/testing/protocol/run.sh:16).

Случайные порты, временные контейнеры, `set -e` и проверка listener PID — улучшения. Остались конкретные проблемы:

- При отсутствии Docker runner автоматически переходит к существующим POSTGRES/REDIS. Запускаемый сервер выполняет EnsureSchema/Seed и подписывается на реальные обновления, то есть это не гарантированно read-only тест рабочего окружения. Такой переход должен требовать явного `GAME_TEST_ISOLATED_DB=off`, а default — завершаться с ошибкой.
- Scratch Redis без пароля, но `REDIS_PASSWORD` из окружения не очищается. При заданном dev/CI пароле scratch-запуск попытается аутентифицироваться там, где пароль не настроен. Также наследуются игровые env overrides; fixture не полностью фиксирован.
- Все запуски используют `/tmp/pixi-protocol-probes` и перезаписывают `lib/proto.mjs` в исходном дереве. Параллельные запуски конфликтуют по бинарнику/логам/decoder. Использовать mktemp и генерировать decoder внутри каталога запуска.
- `redis_ready` не инициализирован перед циклом: при всех неуспешных ping с `set -u` сработает unbound variable вместо предусмотренной диагностики.

Полный Docker runner не запускался: его результат здесь не заявляется. Bash syntax check прошёл. Сохранённый в репозитории proto.mjs всё ещё старый — прямой запуск probe без предварительной пересборки не следует считать поддержанным.

**Статус всех 19 пунктов предыдущего отчёта**

| Прежний пункт | Сейчас |
|---|---|
| 1. Клиентское предсказание | Частично исправлен; накопление ошибки остаётся, см. пункт 1. |
| 2. ACK вытесняют state | Прежний starvation исправлен: первый state может превысить выделенный stateBudget. Это soft budget: ACK не ограничены фактически, а их charge ограничен половиной бюджета. Не трактовать настройку как строгий общий egress cap. |
| 3. Remainder/ID после sort | Исправлен map по ID и тестом полного тика/encoder. |
| 4. AttackStartTick | Исправлен клиентский callback; серверное подавление по-прежнему не учитывает поле. |
| 5. PATCH null | Исправлен NullableField; явный null очищает optional поле/способность, отсутствие сохраняет. |
| 6. Конкурентные PATCH unit | Исправлен SELECT FOR UPDATE внутри той же транзакции. Настоящий конкурентный DB-тест не выполнялся. |
| 7. Доставка конфигурации | Не исправлена: нет revision/reconciliation; commit→Publish остаётся. |
| 8. Валидация/атомарность configctl | Появился set-many, но resolver сохраняет неверный контракт; см. пункты 2–4. |
| 9. Hot reload баланса/limiters | Остаётся: definitions/tables/JSON отдельны, клиентские units загружаются один раз, существующие ресурсы не мигрируют; limiter и batch size захватываются при создании. |
| 10. Shed threshold | Исправлен общей recordFanoutDrop с `>=`. |
| 11. Full recovery при cap/churn | Остаётся. Любой gap требует полного мира, membershipChanged форсирует full. Fair debt уменьшается по выбору, а не успешной отправке. |
| 12. Roster/ресурсы | Утечка attributes для ушедших исправлена фильтрацией при full state. Частота authoritative stamina/HP по-прежнему связана с roster/full sync. |
| 13. Idle TiDi | Исправлен вызовом broadcaster для пустого мира и deferred regulator для ранних возвратов. |
| 14. Protocol tools | Загрузка конфига и decoder улучшены; модель predictor и качество assertions всё ещё недостаточны. |
| 15. Effective limits/proxy | Не изменён: SQL cap=12000, rate=12000, burst=20000, IP rate=0; это значения репозитория, не проверка вашей БД. |
| 16. Health/lifecycle | Tick watchdog добавлен и протестирован. Watchers и startup-error cleanup по-прежнему вне полного общего lifecycle; main не изменялся. |
| 17. Spatial index без потребителя | Удалён из текущего all-to-all hot path; прежняя лишняя работа устранена. AOI не реализован, что ожидаемо для этого изменения. |
| 18. Метрики | Range/world_step разделены, lazy recovery учитывается и имеет counter. Packet kind/фактические egress bytes по типу и fairness по успешному enqueue остаются направлением улучшения. |
| 19. SQL/DSN/runtime | SMALLINT-границы и URL-экранирование DSN исправлены. Runtime defaults, nullable SQL groups, sslmode и version pinning не изменены. |

Серверный lifecycle, partial writes, read-per-connection, admission reservation, management listener и coalesced resync не получили новых обнаруженных регрессий в просмотренных изменениях и штатных проверках. Это не утверждение об отсутствии ошибок при любом расписании/нагрузке.

**Остатки кода и дальнейшее упрощение**

После перехода на generic PATCH больше не используются семь DTO `blockPatchRequest` … `dashThrustPatchRequest` в начале admin.go. Старый `Store.getUnitRow` тоже остался без вызовов после введения getUnitRowForUpdate. Они действительно кандидаты на удаление.

По-прежнему присутствуют `Player.LastActivity`, неиспользуемая валидация env WORKERS, EncodePlayerLeft и старые protocol no-op. Удалять no-op надо согласованно с отправителями и версией протокола. `units.All()` по-прежнему отдаёт опубликованный slice без защиты от внешней записи; новых mutating callers не найдено.

GetAllPlayers/GetTickDuration и spatial index удалены; повторно рекомендовать их удаление не нужно. Угадывание локального player ID по последнему объекту full state также удалено. Epoll-файлы возвращать ради этого ревью оснований нет: они заменены другой моделью чтения, а не забыты при сборке.

Добавлено много описательных комментариев, хотя CLAUDE.md ограничивает их doc/tooling comments. Большинство новых блоков формально doc comments, но отдельный rationale внутри ValidateDefinition не соответствует правилу. Это P3, не эксплуатационная ошибка. Приоритетнее поправить вводящие в заблуждение утверждения об atomicity SetKeys и точном соответствии harness клиенту.

**Проверки**

- `go test -race ./...` — PASS. Первый запуск упёрся в запрет sandbox на создание socket; повтор с разрешёнными локальными сокетами прошёл, это не дефект кода.
- `go vet ./...`, `tsc --noEmit`, `node --test utils/testing/artillery/latency-probe.test.cjs`, `bash -n utils/testing/protocol/run.sh` — PASS.
- Пять целевых воспроизведений в изолированной копии с `-race` — подтверждены: накопление drift, подавление AttackStartTick, ложное принятие persisted config, ложное отклонение правильного patch, зависимость runtime от порядка уведомлений. PASS этих тестов означает наличие описанного дефектного поведения.
- Собранный текущий TS decoder прочитал Go wire corpus с двумя ID и AttackStartTick. Использован сохранённый Go v13 packet из предыдущего ревью; Go wire encoder в этих коммитах не менялся. Это небольшая format-проверка, не полный corpus совместимости.

Короткий load smoke текущего коммита: `GAME_LOAD_CLIENTS=1200 GAME_LOAD_SECONDS=15 go test ./internal/server -run '^TestLoadConnections$' -count=1 -v` — PASS. Плато 15.018 s, 360001 state frames, 36000 ACK, gaps=0, errors=0, TiDi=100%, payload 38.62 MB/s; Shutdown завершился. [Лог](/tmp/pixi-review-d4a62d7-load.log). Это локальный fixture-тест транспорта с изменением движения раз в 500 ms: он не исполняет браузерный predictor, не проверяет все entity records, не включает полноценный бой и не измеряет p99 end-to-end latency. Его успешность не опровергает воспроизведённые ошибки.

Исходники воспроизведений: [game/review_test.go](/tmp/pixi-review-d4a62d7-05V8tw/internal/game/review_test.go), [liveconfig/review_test.go](/tmp/pixi-review-d4a62d7-05V8tw/internal/liveconfig/review_test.go), [результаты](/tmp/pixi-review-d4a62d7-05V8tw/results.log), [race log](/tmp/pixi-review-d4a62d7-race.log). Файлы в /tmp временные. Повторить из той копии: `GOCACHE=/tmp/pixi-go-review-cache go test -race ./internal/game ./internal/liveconfig -run '^TestReview' -count=1 -v`.

Реальные Postgres/Redis, `.env` и пользовательские процессы не изменялись. Изоляция Docker runner, SQL interleavings и отрисовка Pixi оценены по исходникам, без запуска соответствующих полноценных интеграций. Отдельные architectural gaps — межзонное владение, handoff, persistence и полноценный pipeline попаданий — не изменились и остаются следующим этапом, а не повторно предъявляемыми исправлениями P1 инстанса.
