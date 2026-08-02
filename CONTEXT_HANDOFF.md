# MaestroVPN — актуальный контекст и передача работы

## 0F. LIVE: компайл-гейт ЗЕЛЁНЫЙ, тестовый APK собран

Раздел новее 0E. Это первый в этой работе прогон, доказавший, что телефонный код собирается.

- Ветка `codex/mobile-4d-deck`, HEAD на момент прогона `39112cf`.
- Прогон **`30736147048`**: `assembleOtherDebug` **success**, `testOtherDebugUnitTest`
  **success**, шаг загрузки success И артефакт реально существует.
- Артефакт `maestrovpn-tv-test-apk`, **177 358 268 байт**, id `8829679430`, не истёк.
  Отчёт тестов — `unit-test-report`, id `8829684189`.
- Предыдущий прогон `30735360248` на `89230ec`: сборка прошла, упали 4 теста из 72 — все
  из-за устаревшей геометрии в моих же тестах, не из-за рантайма:
  центр медальона переехал на новое кольцо (195.0/316.4 в портрете, 422.0/−33.2 в ландшафте);
  атлас вырос с пяти слоёв до восьми, и на memoryClass 256 МиБ два света помещаются в
  1408 px вместо 1536 (87.6 МиБ при бюджете 94.4; при 512 МиБ три света по-прежнему на
  полных 1620 — 173.8 при 196.8);
  допуск центров секторов 2.5 dp не покрывал собственный замер (вторая ячейка 88.3 против
  номинальных 91). Поправлено в `39112cf`.
- ⛔ Релиза, GitHub Release, подписи для прода и OTA НЕ делалось. Мерж в `main` запрещён.
- Что осталось: владелец ставит APK и смотрит глазами; диаметр мозаичного диска не решён —
  мой замер 301.2 dp включал боковые изумруды, независимая проверка даёт ~231 dp (см. 0E).

⛔ Урок этой серии, чтобы следующий контекст не повторил: число, уходящее в спеку или в код
как константа, обязано быть подтверждено ВТОРЫМ способом с другой физикой. На этой сцене
зелёными являются пять разных вещей — мозаика, изумруды кольца, радужка, статусная надпись и
индикатор выбора, — и одноразовый порог «яркая зелень» ловит не то, что имелось в виду.
Подробно: `memory/measure-twice-rule-2026-08-02`.

## 0E. LIVE: исправление Home после скриншота владельца 23:05

Этот раздел новее 0D. Claude работает только с GitHub и должен начинать с ветки
`codex/mobile-4d-deck`. Локальные файлы Windows источником правды не являются.

- База исправления: `95b4847`. Implementation-checkpoint: `db5a683`.
- Скриншот владельца с временем 23:05 был сделан до коммита `6447a9a` (23:07 МСК),
  поэтому съехавшие Material-глифы Telegram/WhatsApp на нём не соответствуют текущему коду.
  В runtime уже используются фирменные `telegram.webp`, `max.webp`, `whatsapp.webp`.
- Найден реальный runtime-дефект: слой `contacts` был сгенерирован в атлас, но не входил в
  список отрисовки. Теперь порядок восьми слоёв един и проверяется тестом:
  `wood → console → contacts → frame → cartouche → vines → arc → ring`.
- Тёмное пятно/срез внизу мозаики удалён обратимым rollback: восстановлен RGB трёх
  `home_ring_*` из последнего состояния до cap (`03a672a`). Альфа, бронзовая рама, камни и
  регистрация не менялись; source и kit совпадают. Это временная визуальная коррекция, а не
  заявление о выполнении спорного resize из `95b4847`. Перегенерирована только страница
  `atlas_*_08.webp`.
- Alpha SHA кольца: `a0f07bea7e3b770c326687ee934c8d8dfd6629c164086fcb082d1a10c095bde0`.
  Raw RGBA SHA-256: `_l` `29b1e212256911593a64bc33c11e975b39b049bc2969cce8a3f2c0871a5e7f96`,
  `_c` `87dc65186d3a93a3b980ab23c3352786ecb7be9a33cd7e0df32b34c411e462e0`,
  `_r` `ed95115915781538f33beaeeee6213a730d135c7e4632d8478dce11619e3f825`.
- Новый art-gate проверяет непрерывность мозаики на master-y 2250/2300 и отсутствие
  искусственного перепада яркости. Он намеренно не фиксирует долю зелени в статусной зоне и не
  выдаёт её за диаметр. `--group ring` и `--selftest` проходят; реконструкция 27 WebP из 90
  фрагментов попиксельно точна и стабильна.
- Строка статуса теперь честно различает `Подключён`, `Подключение`, `Отключён`.
  Визуальная высота кнопок телефона/покупки совпадает с эталоном (38/45 dp), а доступная
  область касания остаётся не меньше 48 dp.
- Контактные плиты используют утверждённые в `95b4847` границы:
  `34.1…134.5`, `144.8…245.2`, `255.5…355.7 dp`; иконки 26 dp, подписи 10.5 sp.
- Спецификация `2026-08-01-mobile-4d-mosaic-disc-diameter.md` содержит ошибочный замер:
  заявленные 301.2 dp включают два боковых изумруда. Повторная проверка по переходу материала
  `G−R` на всех непрозрачных пикселях (не по порогу яркой зелени) даёт границу r=644…645 px и
  физический диаметр около 1280…1290 px / 231.1…232.9 dp. Поэтому target 1469 px не уменьшит,
  а увеличит диск примерно на 14%. Ресайз по этому target намеренно не выполнялся. Сначала
  нужен новый APK и свежий скриншот; корректировать диаметр можно только по контуру мозаики без
  самоцветов.
- Дизайн-решение и исполнимый план:
  `docs/superpowers/specs/2026-08-01-mobile-home-ring-and-contacts-correction-design.md` и
  `docs/superpowers/plans/2026-08-01-mobile-home-ring-and-contacts-correction.md`.
- Финальный тройной review: art/atlas, Kotlin/runtime и независимый замер границы материала —
  Critical/Important замечаний нет. `git diff --check`, Python compile, ring/selftest, simulator
  и нулевой TV-scope проходят.
- Локальные Gradle/APK-сборки не выполнялись. TV-пути, `TvEskiz*` и `tvm_*` не менялись.

Следующий внешний шаг: собрать тестовый APK в GitHub Actions из запушенного checkpoint,
установить его поверх поддерживаемой текущей версии и сделать новый скриншот Home. Нельзя
оценивать эти исправления по старому APK или накладывать новый deck поверх старого mobile UI.

## 0D. LIVE: перерисовка всех семи слоёв завершена и проверена

Этот раздел новее 0C. Claude работает только с GitHub и должен начинать с ветки
`codex/mobile-4d-deck`.

- Финальная инструкция: `docs/superpowers/plans/2026-08-01-mobile-4d-assets-final-handoff.md`.
- Все 18 runtime-source PNG уже заменены в `design/mobile-asset-redraw/source/`:
  `wood`, `frame`, `cartouche`, `vines`, семисекторная `arc`, круглый `ring` с мозаикой;
  для каждого есть `_l/_c/_r`.
- Новая нижняя консоль лежит отдельно в `design/mobile-asset-redraw/kit/home_console_{l,c,r}.png`:
  её нужно добавить в генератор и runtime как новый слой, а не накладывать поверх старого UI.
- Утверждённые `arc` и `ring` в `source/` байт-в-байт совпадают с копиями в `kit/`.
- Контрольные art-only превью: `design/mobile-4d-references/05-mobile-4d-art-only-{l,c,r}-2026-08-01.png`
  и `06-mobile-4d-art-only-lcr-2026-08-01.jpg`. Глаз и заголовок там намеренно отсутствуют —
  их рисует код.
- Проверки: контракт 18 runtime PNG — PASS; unit tests `7/7` — PASS; arc/console/selftest — PASS;
  полный аудит 21 PNG — PASS. APK/Gradle локально не запускались.
- Старый мобильный интерфейс нельзя оставлять под новым. При подключении удалить старую сцену и
  revolver-реализацию после проверки ссылок. ТВ-код и `tvm_*` не трогать.

## 0C. LIVE: утверждена очередь оставшихся слоёв Home — кольцо первое

Этот раздел новее 0B и является обязательной отправной точкой для Claude/Codex.

- Рабочая staging-ветка: `codex/mobile-4d-deck`; Claude видит только то, что запушено на GitHub.
- Утверждённая владельцем спецификация:
  `docs/superpowers/specs/2026-08-01-mobile-4d-seven-sector-arc-console-design.md`;
  файл впервые опубликован коммитом `fdf74d0`.
- Авторитетный общий план: `docs/superpowers/plans/2026-08-01-mobile-4d-remaining-layers.md`,
  commit `99a166d`, code-base `c2a6980`. Он задаёт очередь по видимому эффекту.
- Отдельная утверждённая спека дуги/консоли из `fdf74d0` остаётся источником точной
  геометрии для пунктов 2 и 3 общего плана.
- Code checkpoint `c2a6980` уже содержит новый контракт размера/клипа глаза и внутреннее
  свечение кольца, но `source/` и runtime-атлас от `e6b0922` всё ещё содержат старую
  шестисекторную дугу и прежний ring-арт.
- Очередь: `ring + mosaic → arc → console → frame → cartouche → vines → wood`.
- Первый art-чекпойнт — кольцо Ø1849 px и мозаичный диск под существующим живым глазом:
  мозаика доходит от век до бронзы, голого дерева внутри медальона нет.
- Дуга: семь рабочих интерьеров с центрами
  `39/91/143/195/247/299/351 dp`; центральный четвёртый сектор — AnyTLS; минимум
  каждой ячейки `44×46 dp`; нижний ромб удалён; верхний замок не пересекает центр.
- Консоль: три полных варианта освещения на холсте 2160×4670, alpha bbox
  `(44,4071)–(2116,4647)`; это не позиционные left/center/right-фрагменты.
- Между семью ячейками шесть резных разделителей уже входят в дугу. Отдельный
  `separator_gold` поверх них не накладывать.
- Runtime-атлас находится в `app/src/main/assets/mobile_4d/atlas_{l,c,r}_NN.webp`,
  24 файла; загрузчик — `Mobile4DGeneratedAssets.kt`.
- GitHub-only инвариант: локальный коммит или файл не считается checkpoint. После каждого
  материального шага обновить этот handoff, запушить `codex/mobile-4d-deck` и подтвердить
  удалённый SHA через `git ls-remote`.
- Локальные Gradle/APK-сборки на компьютере владельца запрещены; TV и `tvm_*` не трогать.

## 0B. LIVE: полноширинная дуга перерендерена, CI-контракт исправлен

Раздел 0B исторический и заменён разделом 0C в части количества ячеек и следующего шага.

Этот checkpoint новее раздела 0A и обязателен перед продолжением работы Claude.

- Рабочая staging-ветка: `codex/mobile-4d-deck`; PR для неё не создавался.
- Исправление документации: `a1e0879` (`docs: correct mobile arc and CI contract`).
- Новый asset-checkpoint: `3bb5cff` (`design: rerender full-width mobile protocol arc`).
- Заменены только `design/mobile-asset-redraw/kit/home_arc_l.png`,
  `home_arc_c.png`, `home_arc_r.png`.
- Каждый файл — полный элемент на общем холсте 2160×4670, а не позиционная треть.
- Альфа-область всех трёх вариантов совпадает попиксельно и занимает
  `(0,3150)–(2160,3905)`; SHA-256 общей альфы:
  `33027c27c5b83275b0628d022699d483689b7b2152ccf267d930a7d91eeeb5c3`.
- В этом историческом checkpoint ошибочно заявлены семь секций; фактически их шесть. Текст, протоколы, иконки, selected/disabled-состояния,
  отдельные `separator_gold` и `center_jewel` рисует/накладывает код.
- Варианты различаются только направлением рельефного света: `_l`, `_c`, `_r`.
- PNG: RGBA, 8 бит, без ICC, без chroma-key каймы; inventory кита остаётся 31/31.
- Android/Gradle/APK локально не запускались; TV и `tvm_*` не менялись.

Критическое исправление CI:

- Неверно считать, что push только в `design/**` не запускает CI на ветке открытого PR.
- PR #74 имеет head `codex/mobile-4d-interface` и уже содержит `app/**` в полном diff
  относительно `main`; поэтому любой push в эту ветку повторно проходит path-filter.
- Это подтверждено успешным run `30672441581` на `06c905f`.
- До закрытия PR #74 не пушить туда промежуточные ассеты. Для безопасной передачи использовать
  `codex/mobile-4d-deck`; после push обязательно проверять отсутствие нового workflow run.

Следующий шаг Claude:

1. Начать с `docs/superpowers/specs/2026-08-01-mobile-4d-arc-rerender-design.md`,
   `docs/superpowers/plans/2026-08-01-mobile-4d-arc-rerender.md` и `design/mobile-asset-redraw/KIT.md`.
2. Подключить три полноширинных варианта дуги в `PhoneHomeControlDeck`, смешивая свет по наклону.
3. Не накладывать новую дугу поверх старого `PhoneRevolverMenu` или других mobile-only слоёв:
   новый deck становится единственным владельцем зоны, заменённые старые реализации удаляются.
4. Сохранить семь протокольных позиций, callbacks, locked/selected/disabled-состояния и теги тестов.
5. Перед обновлением проверить миграцию поверх поддерживаемой версии приложения: тот же
   `applicationId`, возрастающие `versionCode`, сохранение пользовательских данных и чистое
   удаление obsolete mobile-only ресурсов. Совсем древние неподдерживаемые версии можно исключить
   только явным version-gate. TV — строго нулевой diff.

## 0A. LIVE: исходный component kit из 31 PNG готов

Этот checkpoint новее прежнего описания недостающих ассетов и обязателен для Claude/Codex перед дальнейшей мобильной работой.

- Ветка: `codex/mobile-4d-interface`.
- Asset-only commit: `af36016` (`design: add mobile 4D component kit`).
- Утверждённые документы: `a639fa4` (design spec) и `176ea73` (implementation plan).
- Готовые файлы: `design/mobile-asset-redraw/kit/`.
- Точный состав — 31 самостоятельный PNG:
  - 7 элементов нижней дуги и её динамических эффектов;
  - 3 позиционных модуля нижней консоли;
  - 15 элементов трёхчастного конструктора кнопок и состояний;
  - 3 панели `connected` / `connecting` / `error`;
  - 3 контактные иконки `telegram` / `whatsapp` / `max`.
- Формат: RGBA PNG, 8 бит, sRGB без ICC; `home_arc_*` и `home_console_*` имеют ширину 2160 px; контактные иконки — 192×192.
- Каталожный лист использован только как визуальный референс: его пиксели не вырезались и не растягивались.
- Приёмка: `FULL PASS 31/31`; точный inventory, прозрачные углы, отсутствие chroma-key, совпадение alpha кнопочных состояний и статусных панелей.
- Aggregate kit SHA-256: `237995f659fa4b94637d4091e7dc417ed47babb57f7836d8608fa88f32a12a1b`; общий размер 8 085 225 байт.
- Scope gate: diff `app/`, `ops/`, `.github/`, TV и `tvm_*` пуст; Gradle/APK, merge, release и OTA не запускались.

Следующий безопасный шаг для Claude:

1. Читать `docs/superpowers/specs/2026-08-01-mobile-4d-component-kit-design.md` и `docs/superpowers/plans/2026-08-01-mobile-4d-component-kit.md`.
2. Брать исходники только из `design/mobile-asset-redraw/kit/`; не резать прежний каталог и не накладывать новый UI поверх старого mobile-only интерфейса.
3. Подключение в runtime выполнять отдельной проверяемой задачей с удалением заменённого mobile-only UI и нулевым TV-diff.

## 0. LIVE: mobile 4D реализация завершена, GitHub CI и test APK готовы

Этот раздел новее остальных и имеет приоритет, если ниже встречается устаревшая формулировка.

### НОВЕЙШИЙ implementation checkpoint — АВТОРИТЕТНЫЙ

- Ветка: `codex/mobile-4d-interface`.
- Последний implementation HEAD перед этим docs-checkpoint: `8d1a431`.
- Последовательность UI-коммитов:
  - `1ec8bb9` — общий phone-only `MobilePremium4DShell` и premium component kit;
  - `77dec2b` — phone purchase flow перенесён на 4D shell;
  - `7b09aa2` — phone QR scanner/error surfaces и share dialog перенесены на premium kit;
  - `67e567a` — phone split-tunnel presentation перенесён на premium shell;
  - `e197617` — исправлены dismissal и адаптивная ширина phone share dialog;
  - `c15b4e3` — удалён старый flat Home, mobile tooling переведён на 4D atlas;
  - `8d1a431` — исправлен пакет импорта Compose `zIndex`, обнаруженный первой CI-сборкой.
- Task 7 завершает визуальную миграцию ровно **6 достижимых phone-экранов + 1 dialog**:
  `tvhome`, `claim`, `trial`, `buy`, `scanqr`, `split` и `IosKaringDialog`.
- Полная матрица source composables, состояний и исключений находится в
  `docs/mobile-screen-coverage.md`.
- Phone Home использует полный многослойный `Mobile4DHome`; пять внутренних экранов используют
  лёгкий `MobilePremium4DShell` без удержания Home atlas; share остаётся dialog поверх Home.
- TV остаётся отдельной неизменённой presentation-веткой universal APK: `TvEskizHome`,
  `TvEskizSpec`, D-pad/focus/Back, TV geometry, `tvm_*` и TV tooling не переносились на phone kit.
- Локальная Android/Gradle/APK-сборка не выполнялась по прямому запрету владельца о слабом ПК.
  GitHub Actions run `30645395284` (№239) успешно выполнил `assembleOtherDebug`, загрузил
  test APK и завершил `testOtherDebugUnitTest` без ошибок.
- Cleanup и combined review завершены: старый `mobile_home_scene.webp` удалён, app/ops больше
  не содержат его потребителей, два найденных замечания share dialog закрыты повторным review.
- Test APK artifact `8799326567` (`maestrovpn-tv-test-apk`) имеет размер `153712227` байт,
  SHA-256 `1044d64e805fb2ac72f9c430e74ba4973ae2eaedebe24a0dbff6580d19894fda`
  и не истёк. Следующий внешний шаг — ручной просмотр владельцем на телефоне. Не делать merge,
  GitHub Release, production signing или OTA.

### Текущее Git-состояние

- Изолированный worktree:
  `C:\Users\User\Documents\Codex\2026-07-30\github-plugin-github-openai-curated-remote\work\proectmaestro-vpn-mobile-4d`.
- Ветка реализации: `codex/mobile-4d-interface`.
- База ветки: `1019339ac29135e79c9901b8e562a2cbe240c06a`
  (`codex/mobile-4d-reference-pack`).
- Последний implementation HEAD перед docs-checkpoint: `8d1a431`.
- Уже созданы локальные коммиты:
  - `ba11ff8` — исправленный scope/план: ровно 6 экранов + 1 dialog;
  - `3d0902d` — чистая модель scene/crop/light/parallax/eye + JVM tests;
  - `a1b1bbf` — deterministic memory-safe atlas generator, manifest, tests и 24 WebP;
  - `ac5defe` — path-safe, interruption-recoverable asset transaction + focused tests;
  - `a3b9cfc` — durable checkpoint этого handoff;
  - `f54c0fe` — lifecycle-safe tilt и memory-budgeted bitmap loader;
  - `5acc6db` — cancellation/OOM-safe bitmap ownership и actual allocation gate;
  - `e34335e` — durable runtime checkpoint этого handoff;
  - `ad7f60d` — чистый mobile 4D Home compositor и eye-state tests;
  - `d5228d9` — hero atlas переведён на обязательный `FilterQuality.High`;
  - `ba87356` — новый Home подключён в phone seam, старое phone-дерево удалено;
  - `1d423f7` — durable checkpoint подключённого mobile 4D Home;
  - `1ec8bb9` — общий lightweight phone-only premium shell;
  - `77dec2b` — purchase phone presentation на premium 4D shell;
  - `7b09aa2` — QR/share phone presentation на premium surfaces;
  - `67e567a` — split phone presentation на premium shell;
  - `e197617` — исправления premium phone dialog после review;
  - `c15b4e3` — удаление obsolete mobile flatten и ремонт phone tooling;
  - `6608f0d` — durable docs handoff перед CI;
  - `8d1a431` — исправление импорта Compose `zIndex` после CI run №238.
- Ветка отправлена в `origin/codex/mobile-4d-interface`; открыт draft PR
  [№74](https://github.com/evgenmay1978-del/proectmaestro-vpn/pull/74).
- Исходный worktree `work/proectmaestro-vpn` не использовать для реализации. Его ложный
  `M ops/phone-screen-sim.py` связан с CRLF; отдельный implementation-worktree создан именно
  для чистой работы.

### Последнее решение владельца — ОБЯЗАТЕЛЬНО

Владелец сначала потребовал все реально существующие mobile-экраны, а затем отдельно
исправил завышенный подсчёт:

> «У меня нет столько экранов в приложении моем».

Следовательно, scope определяется не количеством зарегистрированных внутренних routes, а
фактическими переходами из обычного мобильного запуска. Это **6 экранов + 1 dialog**.
Android TV остаётся строго вне scope. Нельзя менять поведение `TvEskizHome`, TV focus/D-pad/Back,
`tvm_*`, TV-геометрию или TV-симуляторы.

Новое прямое решение владельца по сборке:

> «На компьютере ты не соберешь у меня слабый комп. на гитхаб собери тестовый апк я посмотрю».

Поэтому не выполнять локальную Android APK-сборку на компьютере владельца. После появления
визуально рабочего 6-screen flow отправить implementation branch на GitHub и запускать
`.github/workflows/android-test.yml`. Результат — только test APK artifact для ручного просмотра
владельцем. Запрещены GitHub Release, production signing, merge, OTA и обновление пользователей.

### Инвентарь реальных mobile-экранов

Из обычного запуска пользователь реально достигает:

- `tvhome` — главный экран, встроенный выбор протокола и account card;
- `claim` — ввод логина/кода;
- `trial` — пробный период;
- `buy` — тарифы, оплата и все состояния оплаты;
- `scanqr` — камера, ручной ввод, permission/error states;
- `split` — выбор приложений для VPN;
- `IosKaringDialog` — dialog подключения/передачи на другой телефон, не отдельный экран.

Итого: ровно **6 экранов + 1 dialog**.

Не считать отдельными экранами:

- выбор протокола и account card — секции `tvhome`;
- «отключено / подключение / подключено» — состояния `tvhome`;
- тарифы / ожидание оплаты / подтверждение / активация / done / error — состояния `buy`;
- permission/error — состояния `scanqr`;
- поиск/выбор/предупреждение — состояния `split`.

Внешний OS-intent import/profile flow (`profile/new`, `profile/edit/{id}` и вложенные editor
routes) не входит в обычный MaestroVPN mobile flow и исключён из текущей визуальной работы.
`Dashboard`, `Connections` и `Tools` не зарегистрированы в активном `SFANavHost`.

### Обнаруженная проблема reachability

`Settings`, `Log`, `Groups` и их дочерние routes зарегистрированы, но фактически скрыты:
`bottomNavigationScreens` и `railScreens` пусты, переходов с Home нет,
`pendingNavigationRoute` нигде не получает значение. По последнему уточнению владельца они
**не считаются экранами его текущего приложения и исключены из scope**. Не рестайлить их и не
возвращать в навигацию без отдельного прямого запроса.

### Утверждённая техническая архитектура 4D

Нельзя загружать 15 исходных PNG обычным `painterResource`/`ImageBitmap.imageResource`:
15 × 2160×4670 ARGB требуют примерно **577 MiB decoded RAM**.

Безопасная схема:

1. Сохранить master PNG 2160×4670 в `design/mobile-asset-redraw/source`.
2. Детерминированный generator режет **все пять слоёв** сеткой 3×8.
3. У RGBA убираются прозрачные поля; для каждого fragment сохраняется одинаковая геометрия
   `_l/_c/_r`.
4. Добавляется 2 px edge-extruded gutter.
5. Fragments пакуются в одинаковые для L/C/R atlas pages не больше 2048×2048, чтобы не упереться
   в texture limit старых API 23 GPU.
6. Generator реконструирует atlas обратно в canvas и попиксельно сравнивает с source.
7. Runtime декодирует страницы сразу под физическую ширину viewport, шаг 64 px, максимум 1620.
8. На нормальном устройстве все три направления могут оставаться resident; за кадр рисуются
   только два ненулевых света. На low-RAM — только `_c`, максимум 1080, tilt-light выключен.
9. Бюджет decoded-art — не более 35–40% `ActivityManager.memoryClass`.

Ориентир полной трёхсветовой памяти atlas-комплекта: около 62 MiB при scene width 1110,
111 MiB при 1480, 133 MiB при 1620; существующий eye добавляет около 7.5 MiB.

Прозрачные relief-слои смешиваются внутри изолированного `saveLayer` через
premultiplied `BlendMode.Plus`; обычный последовательный `SrcOver` создаёт alpha dip/кайму.
Wood можно смешивать обычным centre + active-side crossfade.

Предлагаемые runtime-файлы:

- `Mobile4DSceneModel.kt` — чистая математика света/crop/parallax/memory profile;
- `Mobile4DTilt.kt` — lifecycle-safe `TYPE_GAME_ROTATION_VECTOR`, fallback
  `TYPE_ROTATION_VECTOR`, калибровка, display remap, ±12°, low-pass;
- сгенерированный atlas manifest;
- `Mobile4DBitmapStore.kt` — последовательный IO decode и lifecycle;
- `Mobile4DScene.kt` — единый Canvas;
- `Mobile4DHome.kt` — eye/title/connect/revolver;
- общий phone-only `MobilePremium4DShell`/background/dialog для пяти дочерних экранов normal flow.

Глубина: wood ≈0.5 dp, frame 1.5 dp, cartouche 2.5 dp, vines 3.5 dp,
ring/eye 5 dp. Тени рисуются кодом повторным tinted alpha draw; fullscreen blur запрещён.

### LIVE checkpoint реализации — после Task 2

Task 1 принят отдельным review без замечаний:

- commit `3d0902d`;
- `Mobile4DSceneModel.kt` остаётся Android/Compose-free;
- тестами зафиксированы 2160×4670, ContentScale.Crop, L/C/R weights, hysteresis,
  глубины parallax и три состояния глаза;
- локальный Gradle не дошёл до компиляции: wrapper 9.3.1 не скачивается из sandbox.
  Это остаётся CI-gate, а не локальный PASS.

Task 2 завершён коммитами `a1b1bbf..ac5defe` и принят scoped re-review:

- 77 logical fragments;
- 8 одинаковых layouts для каждого направления света;
- 24 lossless WebP, всего 41 749 476 bytes;
- все pages ≤2048×2048;
- source→atlas→source reconstruction exact для всех 15 layers;
- `python ops/mobile-4d-assets.py` — PASS;
- `python ops/mobile-4d-assets.py --check` — PASS, output byte-stable;
- Pillow 11.3.0 и libwebp 1.5.0 закреплены как deterministic toolchain;
- WebP method 0 выбран из-за стабильности на слабом компьютере; это увеличило payload
  на 6 896 538 bytes (≈19,8%), но не decoded RAM и не точность;
- eye assets, old flatten, UI, TV не менялись.

Scoped re-review подтвердил `ADDRESSED` для обоих прежних Important findings:

1. Atlas, manifest и все их ancestors проходят lexical/resolved-path и symlink/reparse check
   до создания каталогов или замены файлов.
2. Persistent fsynced transaction journal восстанавливает согласованные atlas + manifest после
   `KeyboardInterrupt`, kill/process crash и проверяет committed inventory по SHA-256.

Focused safety tests: **7/7 PASS**. Независимый `--check`: **PASS**, reconstruction exact,
output stable. Состояние Task 3 зафиксировано в следующем checkpoint.

### LIVE checkpoint реализации — после Task 3

Task 3 завершён коммитами `f54c0fe..5acc6db` и принят scoped re-review:

- `TYPE_GAME_ROTATION_VECTOR` с fallback на `TYPE_ROTATION_VECTOR`;
- lifecycle registration только в `RESUMED`, neutral state при pause/dispose/no sensor/reduced motion;
- калибровка стабильной нейтрали, display rotation remap, dead zone, ±12° clamp и elapsed-time low-pass;
- target buckets 64 px, caps 1620/1080, 40% memory-class budget и ≈8 MiB reserve под глаз;
- internal-screen mode не удерживает home atlas;
- centre/active-side/all-lights retention выбирается по памяти и проверяется повторно по
  фактическому `Bitmap.allocationByteCount`;
- sequential IO decode, centre fallback, hysteresis и bounded retry при смене стороны;
- reference-counted leases не recycle bitmap, пока предыдущий Compose draw может его видеть;
- prompt cancellation, OOM и partial retain проходят transactional cleanup/rollback.

В первом review было 3 Important finding; fix round 1/5 закрыл все 3 (`ADDRESSED`), новых
регрессий scoped review не нашёл. Добавлены pure tests для cancellation handoff, actual budget,
OOM rollback и partial-retain rollback. Локальный Gradle/APK не запускался; compile/GREEN остаётся
GitHub CI gate. Состояние Task 4/5 зафиксировано в следующем checkpoint.

### LIVE checkpoint реализации — после Task 5

Task 4 завершён коммитами `ad7f60d..d5228d9` и принят review:

- чистый `Mobile4DHome` без `mobile_home_scene.webp` fallback;
- wood → frame → cartouche → vines → ring → existing eye → Playfair title → revolver;
- L/C/R relighting, bounded additive relief, runtime shadows и parallax;
- atlas fragments рисуются с `FilterQuality.High`;
- disconnected eye = `opennessOverride=0f`, connecting = `0.5f`, connected = living/null;
- sensor-rate updates изолированы от `PhoneRevolverMenu`;
- instrumentation tests фиксируют eye semantics/click и старые premium tags.

Task 5 завершён коммитом `ba87356` и принят review:

- phone-ветка `TvHomeScreen` теперь содержит один `Mobile4DHome`;
- старые flat scene/glow/eye/menu layers удалены из phone Compose tree, а не скрыты;
- оба call site передают `serviceStatus == Status.Starting` как `connecting`;
- `connected = Started || Starting` и все callbacks сохранены;
- diff `TvEskizHome`, `TvEskizSpec`, `tvm_*`, ресурсов и tools пуст.

### Исторический checkpoint реализации — Task 6

Task 6 реализован в коммите, содержащем этот checkpoint (`feat: add shared mobile premium 4D shell`):

- `MobilePremiumScreen` обратно совместимо делегирует новому phone-only `MobilePremium4DShell`;
- shell учитывает safe-drawing/IME insets, compact/regular/expanded layout и сохраняет 48 dp Back;
- фон не использует `mobile_surface.webp`, home atlas, cartouche, ring или eye: только сдержанный
  walnut/light draw и существующая настоящая nine-patch `frame_panel`;
- добавлены общие Playfair top bar, dialog/sheet surfaces, setting row и switch;
- shell возвращает нейтральный content-only fallback, если его ошибочно вызвали на TV; нормальные
  TV-ветки и TV-файлы не менялись;
- тесты добавлены до production API; Android/Gradle по прямому запрету владельца локально не
  запускались, поэтому compile/GREEN остаётся GitHub CI gate;
- статические scope-проверки и `git diff --check` прошли.

На момент этого checkpoint следующей была Task 7; её актуальный результат зафиксирован в
новейшем checkpoint в начале раздела 0.

### Состояния глаза

- `Stopped`/`Stopping`/«Отключено» — глаз полностью закрыт.
- `Starting`/«Подключение…» — отдельное полуоткрытое состояние.
- `Started`/«Подключено» — открыт, моргает, смотрит и реагирует на touch как сейчас.

Для этого добавить `connecting: Boolean = false` в `TvHomeScreen`, передавать
`serviceStatus == Status.Starting` из обоих call sites `SFANavigation`. TV этот параметр
игнорирует.

### Повторно используемый premium-кит

Не переписывать без причины:

- `premium/MobilePremiumSurface.kt`;
- `premium/MobilePremiumControls.kt`;
- `premium/MobilePremiumLayout.kt`;
- `premium/MobilePremiumTokens.kt`;
- `fantasy/FantasyDialog.kt`;
- `fantasy/FantasyFrame.kt`;
- `fantasy/FantasyListRow.kt`;
- `fantasy/FantasyToggle.kt`;
- `fantasy/CarvedKit.kt`.

`Claim`, `Trial` и большая часть `Buy` уже используют premium controls. Основная задача пяти
дочерних экранов normal flow — заменить плоский `mobile_surface` единым лёгким 4D shell и
хирургически убрать donor Material UI только в `scanqr`/`split`, не меняя callbacks/данные.
Скрытые Settings/Log/Groups/profile composables не трогать.

### Глобальные overlays, входящие в scope

Premium shell нужен также для app-owned dialogs/sheets:

- `IosKaringDialog`;
- `SelectableMessageDialog`;
- `UpdateDialog`;
- QR permission/error states;
- dialogs в normal-flow `PerAppProxyScreen`;
- service/update/download dialogs, реально показываемые поверх normal flow в `MainActivity`.

OS-intent-only import/profile dialogs и скрытый groups `ModalBottomSheet` исключены.

Системный Android permission dialog стилизовать невозможно и не требуется; стилизуется только
app-owned pre-permission explanation.

### Что уже сделано в этой implementation-сессии

- полностью прочитаны project handoff/spec/reference docs;
- визуально открыт `PREVIEW_c.png` и boards `01`, `02`, `03`;
- подтверждён безопасный seam: TV остаётся в `if (isTv) { TvEskizHome(...) }`, phone `else`
  заменяется clean mobile composable;
- подтверждено, что старый phone glow/web находится вне `else` в `TvHomeScreen` и должен быть
  удалён, а не остаться под новой сценой;
- старый `mobile_home_scene.webp` удалён; simulator реконструирует Home из centre-light 4D atlas,
  а eye generator больше не создаёт плоскую сцену;
- создан isolated worktree/branch;
- материализованы sparse paths `design`, `docs`, `gradle`, `.github`;
- подробный TDD-план исправлен после замечания владельца: scope = 6 normal-flow screens +
  `IosKaringDialog`, а не все зарегистрированные routes;
- Task 1 реализован и принят review (`3d0902d`);
- Task 2 реализован и принят (`a1b1bbf..ac5defe`): generation/`--check` PASS, оба safety finding
  закрыты scoped re-review;
- Task 3 реализован и принят (`f54c0fe..5acc6db`): tilt/loader/ownership готовы, 3 review finding
  закрыты;
- Task 4 Home реализован и принят (`ad7f60d..d5228d9`);
- Task 5 phone seam реализован и принят (`ba87356`): новый Home подключён, старое phone-дерево удалено;
- Task 6 shared phone-only shell реализован (`1ec8bb9`);
- Task 7 завершён (`77dec2b`, `7b09aa2`, `67e567a`): purchase, QR/share и split перенесены,
  а Claim/Trial получают тот же shell через обратно совместимый `MobilePremiumScreen`;
- cleanup завершён (`c15b4e3`), combined review закрыт (`e197617`).

### Локальные ограничения проверки

- `adb devices -l` не показывает подключённого устройства.
- Владелец отдельно запретил нагружать слабый компьютер локальной APK-сборкой; Android build
  переносится в GitHub Actions `android-test.yml`.
- Локально отсутствует `app/libs/libbox.aar`; CI скачивает normal libbox из успешного
  `libbox.yml`.
- `.github/workflows/android-test.yml` запускает `assembleOtherDebug` и
  `testOtherDebugUnitTest`, но существующие instrumentation tests не компилирует и не запускает.
- Для полной mobile-проверки нужно минимум добавить/запустить
  `assembleOtherDebugAndroidTest`; реальный `connectedOtherDebugAndroidTest` требует устройство.

### Точная следующая точка продолжения

1. Использовать исправленный план
   `docs/superpowers/plans/2026-07-31-mobile-premium-4d-interface.md`.
2. `subagent-driven-development/SKILL.md` уже прочитан; plan-scoped SDD ledger создан в
   `.superpowers/sdd/2026-07-31-mobile-premium-4d-interface/progress.md` и игнорируется Git.
3. Проверить чистый worktree, отсутствие runtime-потребителей старого flatten и пустой TV/tvm diff.
4. Локально запускать только лёгкие Python/статические проверки. Android compile/test APK выполнить
   через GitHub Actions `android-test.yml`, скачать/передать владельцу artifact для просмотра.
5. Push ветку, открыть draft PR, дождаться test APK, затем провести visual QA по шести экранам и
   dialog. Не merge/release/OTA.

Обновлено: **31.07.2026**. Этот документ — первая точка входа для нового окна
Codex/Claude. Сначала проверить volatile-факты командами Git и на GitHub, затем
продолжать с раздела «Следующий безопасный шаг».

## 1. Подтверждённое состояние

- Репозиторий: `evgenmay1978-del/proectmaestro-vpn`.
- Базовая ветка: `main`; на момент проверки её HEAD — `5e16c00`.
- Рабочая ветка мобильных референсов:
  `codex/mobile-4d-reference-pack`.
- Базовый коммит готового комплекта ассетов: `21ad085`
  (`design: add mobile 4D redraw assets`). Текущий HEAD может быть новее из-за
  обновления документации — всегда проверять `git rev-parse HEAD`.
- Открыт draft PR
  [№73](https://github.com/evgenmay1978-del/proectmaestro-vpn/pull/73):
  `design: mobile 4D references and 15-layer redraw pack`.
- Ветка и PR содержат документацию, визуальные референсы и исходный арт.
  **Android-код, backend, TV, release, OTA и workflow в этой ветке не менялись.**
- `AGENTS.md` и этот handoff существуют в PR-ветке; до merge PR №73 новый
  checkout от `main` их не увидит. Для продолжения выбрать указанную ветку.
- Новый арт ещё не подключён в приложение. Старый
  `app/src/main/res/drawable-nodpi/mobile_home_scene.webp` пока остаётся рабочим
  runtime-ресурсом и удаляется только вместе с проверенной миграцией к слоям.

Локальная особенность текущего Windows-worktree: `ops/phone-screen-sim.py` может
показываться как `M` из-за CRLF-метаданных, хотя при последней проверке
worktree/index имели одинаковый blob `8b15218daf7f32c7f95d780e65b730cd81cfa963`.
Не добавлять и не «восстанавливать» файл автоматически; сначала повторно
сравнить `git hash-object -- ops/phone-screen-sim.py` с
`git rev-parse HEAD:ops/phone-screen-sim.py`.

## 2. Решение владельца

Мобильный интерфейс MaestroVPN нужно пересобрать начисто как многослойную
«4D»-сцену: глубина, параллакс, переосвещение по наклону телефона, программные
межслойные тени и живой глаз.

Нельзя класть новый дизайн поверх старого Compose-дерева или оставлять старые
полноэкранные мобильные слои скрытыми под новым. После переноса нужно удалить
заменённый mobile-only код и ассеты, но только после проверки всех call sites.

Телевизионная версия в этой работе **строго вне области изменений**.

## 3. Что уже подготовлено

### Визуальные референсы экранов

Каталог [`design/mobile-4d-references/`](design/mobile-4d-references/):

- `00-current-mobile-ui.png` — структура текущего телефона;
- `01-core-flow-4d.png` — главный экран и основные состояния;
- `02-subscription-activation-4d.png` — подписка и активация;
- `03-settings-advanced-4d.png` — настройки и дополнительные экраны;
- `CLAUDE_INSTRUCTIONS.md` — контракт чистой реализации.

Критичное состояние: **`ОТКЛЮЧЕНО` — глаз полностью закрыт**, радужка и зрачок
не видны. `ПОДКЛЮЧЕНИЕ` — глаз открывается. `ПОДКЛЮЧЕНО` — полностью открыт.

### Готовый исходный арт главного экрана

Полное ТЗ: [`design/mobile-asset-redraw/SPEC.md`](design/mobile-asset-redraw/SPEC.md).

Готовые файлы:
[`design/mobile-asset-redraw/source/`](design/mobile-asset-redraw/source/).

Комплект состоит ровно из 15 PNG:

| Слой | Свет слева | Центр | Свет справа |
|---|---|---|---|
| дерево | `home_wood_l.png` | `home_wood_c.png` | `home_wood_r.png` |
| рамка | `home_frame_l.png` | `home_frame_c.png` | `home_frame_r.png` |
| картуш | `home_cartouche_l.png` | `home_cartouche_c.png` | `home_cartouche_r.png` |
| кольцо | `home_ring_l.png` | `home_ring_c.png` | `home_ring_r.png` |
| лозы | `home_vines_l.png` | `home_vines_c.png` | `home_vines_r.png` |

Формат всех файлов: **2160×4670**, PNG, 8 бит, sRGB без ICC. Дерево — RGB,
остальные 12 файлов — RGBA. Варианты `_l/_c/_r` одного слоя имеют идентичную
альфа-геометрию; меняется только освещение.

Контрольная сборка:
[`design/mobile-asset-redraw/PREVIEW_c.png`](design/mobile-asset-redraw/PREVIEW_c.png).

## 4. Контракт сборки

Порядок наложения без ручных смещений:

```text
home_wood
home_frame
home_cartouche
home_vines
home_ring
existing mobile_eye_* layers
Playfair title
interactive mobile UI
```

Код должен:

- плавно смешивать `_l/_c/_r` по наклону телефона;
- разводить слои на разную глубину параллаксом;
- рисовать мягкие тени между слоями;
- рисовать `MaestroVPN` шрифтом
  `app/src/main/res/font/playfair_display.ttf`;
- сохранить существующие `mobile_eye_open`, `mobile_eye_squint`,
  `mobile_eye_closed`, `mobile_eye_sclera`, `mobile_eye_iris` и
  `mobile_eye_catchlight`.

В арт не запечены и не должны запекаться текст, глаз и тени одного слоя на
другом. У `home_ring_*` центр намеренно прозрачный.

## 5. Жёсткие запреты

- Не использовать `mobile_home_scene.webp` как скрытый фон под новой сценой.
- Не внедрять новый экран дополнительной полноэкранной обёрткой поверх старого.
- Не прятать старые слои через `alpha = 0`, `visible = false`, clipping или
  перекрывающую картинку.
- Не удалять общий ресурс до проверки всех ссылок и TV-потребителей.
- Не менять `tvm_*`, `TvEskizHome.kt`, `TvEskizSpec.kt`, D-pad/focus/Back,
  TV-геометрию, TV-симуляторы и ветки `isTv`.
- Не менять backend, API или VPN-runtime ради визуальной задачи.
- Не делать merge в `main`, release или OTA без отдельного разрешения владельца.

## 6. Что проверено

Для комплекта из 15 PNG выполнена локальная строгая проверка:

- точные имена и холст 2160×4670;
- PNG 8-bit и декларация sRGB без ICC;
- RGB для дерева и RGBA для остальных слоёв;
- одинаковая alpha во всех `_l/_c/_r`;
- отсутствие геометрического сдвига между вариантами света;
- прозрачный центр кольца;
- контрольные композиты для левого, центрального и правого света.

Результат последнего запуска: **PASS, 15/15 файлов, 0 ошибок**.

Не выполнялись Android build, emulator/device-тест и внедрение в runtime,
поскольку этот PR пока содержит только референсы и исходники.

## 7. Что не завершено

- Владелец ещё должен визуально принять или скорректировать новый центральный
  композит.
- 15 исходных PNG не конвертированы в Android lossless WebP.
- В мобильном Compose-коде ещё нет многослойного композитора, смешивания света,
  параллакса и межслойных теней.
- Старый плоский `mobile_home_scene.webp` и заменяемые mobile-only слои ещё не
  удалены.
- Нет нового Android CI-build и ручной проверки APK на физическом телефоне.

## 8. Следующий безопасный шаг

После явного разрешения владельца на реализацию:

1. Проверить текущие branch/HEAD/PR и прочитать все документы из раздела 9.
2. Инвентаризировать mobile-only composable, ресурсы и call sites старой сцены.
3. Показать владельцу список экранов и точный план удаления старого mobile-only
   UI до правки кода.
4. В отдельном implementation PR подключить обработанные lossless WebP,
   mobile-only композитор и состояния глаза, не меняя TV-ветку.
5. После успешной миграции удалить старый плоский композит и мёртвые мобильные
   слои, подтвердив поиском отсутствие ссылок.
6. Запустить `assembleOtherDebug`, unit tests, доступные UI-тесты и сделать
   сравнение всех мобильных экранов на телефоне. Отдельно доказать, что TV diff
   отсутствует.

## 9. Обязательный порядок чтения

1. Этот `CONTEXT_HANDOFF.md`.
2. [`AGENTS.md`](AGENTS.md).
3. [`CLAUDE.md`](CLAUDE.md).
4. [`design/mobile-4d-references/README.md`](design/mobile-4d-references/README.md).
5. [`design/mobile-4d-references/CLAUDE_INSTRUCTIONS.md`](design/mobile-4d-references/CLAUDE_INSTRUCTIONS.md).
6. [`design/mobile-asset-redraw/SPEC.md`](design/mobile-asset-redraw/SPEC.md).
7. [`design/mobile-asset-redraw/README.md`](design/mobile-asset-redraw/README.md).
8. Только для подтверждённо нужной старой геометрии глаза и истории CI:
   [`MAESTROVPN_UI_HANDOFF.md`](MAESTROVPN_UI_HANDOFF.md).

`MAESTROVPN_UI_HANDOFF.md` — исторический документ предыдущей premium-итерации.
Его старое утверждение «fixed mobile_home_scene remains unchanged» **заменено**
решением владельца от 31.07.2026 о чистой многослойной пересборке. Исторические
числа геометрии глаза и факты CI можно использовать только после сверки с кодом.

## 10. Как поддерживать этот handoff

После каждого материального изменения обновлять:

- дату проверки;
- ветку, HEAD и PR;
- разделы «Что проверено», «Что не завершено» и «Следующий безопасный шаг»;
- ссылки на новые решения и артефакты.

Локальная работа не является handoff: каждый материальный checkpoint должен быть запушен
в указанную staging-ветку и подтверждён удалённым SHA.

Не записывать сюда токены, пароли, приватные URL подписок, данные клиентов или
непроверенные предположения.
