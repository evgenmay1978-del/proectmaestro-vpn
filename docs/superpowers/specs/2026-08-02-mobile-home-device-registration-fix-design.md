# Mobile Home: регистрация арта, надписей, логотипа и живого глаза на реальном устройстве

**Решение владельца:** 02.08.2026  
**Ветка:** `codex/mobile-4d-deck`  
**База диагностики:** `bacdf7a` (`6dcae22` — APK implementation SHA)  
**Область:** только мобильный `Home`; TV, backend, VPN runtime, release и OTA вне задачи.

## 1. Подтверждённые дефекты и корневые причины

### 1.1 Арт и Compose-контролы используют разные системы координат

`mobile4DSceneLayout()` масштабирует мастер 2160×4670 через `ContentScale.Crop`: берёт максимум
масштабов по ширине и высоте и затем центрирует результат по вертикали. Контролы и кодовый
`MaestroVPN` размещаются отдельно через `phoneHomeReferenceLayout()`, который масштабирует только
по ширине и не применяет `scene.translationY`.

Симулятор проверял идеальный холст 390×844, где соотношение почти совпадает с мастером и
вертикальный crop-сдвиг близок к нулю. На присланных фото системные панели оставляют приложению
примерно 390×797. При ширинном масштабе мастер имеет высоту около 843.2 dp, поэтому текущий Canvas
центрируется с `translationY ≈ (797 - 843.2) / 2 = -23.1 dp`, а логотип и controls остаются на
нулевом origin. Общий `ScrollState` правильно синхронизирует delta, но исходная регистрация уже
разошлась примерно на 23 dp.

### 1.2 Нижняя консоль получает второй ошибочный scale

`BottomConsole` повторно вычисляет масштаб как `(382 - 8) / 390 = 0.95897` даже при эталонной
ширине 390 dp. Его координаты уже заданы в абсолютной системе 390 dp, поэтому иконки, подписи и
hit targets смещаются вверх и сжимаются относительно atlas-слоя `console`.

### 1.3 Глаз и мозаика не имеют общего посадочного контракта

Кольцо с мозаикой рисуется atlas-слоем, а `LivingEyeMedallion` независимо строит универсальный
центрированный oval clip по `min(width, height)` и добавляет тени внутри него. Центр и parallax
сейчас близки, но фактическое посадочное место мозаики, clip глаза и ширина шва не представлены
одной геометрией и не проверяются совместно. Поэтому тени затемняют край, но глаз всё равно
читается отдельным наложенным элементом.

### 1.4 Почему прежние проверки пропустили дефект

- Python simulator рисует арт и подписи сам в одном холсте 390×844; он не воспроизводит
  `Scaffold`/system insets, Compose modifier order, Android font metrics и реальный app viewport.
- JVM-тесты проверяют константы и одинаковый scroll delta, но не абсолютную регистрацию разных
  coordinate spaces.
- Eye asset-тесты проверяют отдельные WebP и alpha support, но не композиционный шов с ring atlas.

## 2. Рассмотренные подходы

### A. Единая ширинная система координат с верхним якорем — выбран

Home получает один transform от master/reference к app viewport: масштаб только по фактической
ширине, `translationX = 0`, `translationY = 0`. На коротком экране нижняя часть не
центр-кропается, а честно достигается существующим scroll. Арт, логотип, eye seat, декоративные
слои и Compose-контролы используют этот transform.

Плюсы: устраняет корень дефекта на всех высотах; соответствует продуктовой директиве «короткий
экран — scroll»; не требует подгонки под конкретную модель телефона. Минус: нужна новая проверка
короткого viewport и landscape, чтобы явно определить допустимый clip нижней части.

### B. Добавить текущий `scene.translationY` ко всем Compose-координатам — отклонён

Это синхронизировало бы текущий центр-crop, но на коротком экране весь Home, включая логотип и
hero, продолжал бы уходить вверх. Такое поведение противоречит верхнему якорю выбранного Home и
делает системные inset величиной скрытого визуального сдвига.

### C. Подогнать отдельные offsets либо запечь общий PNG — отклонён

Индивидуальные offsets снова разойдутся на другой высоте/density/fontScale. Плоский PNG уничтожит
живой глаз, локализацию, доступность, callbacks и 4D-переосвещение и запрещён проектным контрактом.

## 3. Выбранный дизайн

### 3.1 `Mobile4DHomeSceneLayout`

Добавить чистую phone-Home геометрию, которая:

- принимает фактические `widthPx` и `heightPx`;
- использует `scale = widthPx / 2160`;
- закрепляет master origin в `(0, 0)`;
- сохраняет master height как scrollable design height, не пытаясь уместить её в viewport;
- вычисляет center/radius ring и eye seat тем же transform;
- не меняет существующий общий `mobile4DSceneLayout()` там, где нужен прежний crop-контракт.

`Mobile4DHome`, Canvas, кодовый титул и `PhoneHomeControlDeck` получают одну вычисленную модель.
`ScrollState.value` остаётся единственным scroll delta: fixed hero не движется, а `console`,
`contacts`, `arc` и их controls движутся одинаково.

### 3.2 Контролы и типографика

- Удалить повторное масштабирование консоли; все её абсолютные координаты переводить одним
  `referenceScale = viewportWidthDp / 390`.
- Contacts, protocol cells, buy и console не получают локальных компенсирующих offsets.
- Логотип сохраняет Playfair и три слоя объёма, но его bounds принадлежат общей Home-геометрии и
  следуют тому же top-anchored transform, что `cartouche`.
- Размеры hit targets остаются не меньше 48 dp; видимый орнамент может быть меньше.
- Системные insets применяются ровно один раз снаружи Home; внутри нет второй компенсации status
  или navigation bar.

### 3.3 Общий `EyeSeatGeometry`

Ввести чистую модель посадочного места с полями:

```text
centerX, centerY
socketRadiusX, socketRadiusY
seamOverlapPx
innerOcclusionWidthPx
```

Ring atlas и `LivingEyeMedallion` получают одну модель и один parallax offset. Текущий generic
oval заменяется socket-mask, измеренной по фактическому внутреннему контуру мозаики/бронзы.
Видимая часть глаза перекрывает шов минимум на 1 px, а передняя окклюзия мозаики закрывает край,
не образуя второй самостоятельной окружности.

Не меняются: исходный размер радужки, open/squint/closed registration, blink, gaze, pupil motion,
touch reaction, catchlight, glow и mapping трёх VPN-состояний. Новый плоский eye/mosaic asset не
создаётся.

## 4. Проверяемые контракты

### Чистые JVM-тесты

1. При 390×844 и 390×797 Home transform имеет одинаковые `scale`, `translationY = 0` и одинаковые
   title/ring/deck anchors; меняются только viewport height и доступный scroll range.
2. Art bounds и control bounds для contacts/arc/console используют один reference scale.
3. Console при ширине 390 имеет scale 1.0; при ширине 320 — ровно `320/390`.
4. `screenEyeCenter == screenSocketCenter` с допуском 0.5 px.
5. Eye/socket seam overlap не меньше 1 px; opaque eye content за socket-mask отсутствует.
6. Fixed layers имеют delta 0, deck layers и controls имеют одинаковый `-scrollPx`.

### Simulator

Симулятор принимает отдельно full screen и app viewport/system insets. Обязательные boards:

- 390×844 без inset для исторического сравнения;
- app viewport 390×797, соответствующий присланным фото;
- scroll 0 и 64 dp на коротком viewport;
- connected/connecting/disconnected для общего eye socket.

Он обязан использовать те же чистые transform/geometry числа и показывать отдельные guides
alpha-bounds для title, contacts, arc, console и eye seam.

### Android gate

- `testOtherDebugUnitTest` и `assembleOtherDebug` только в GitHub Actions;
- instrumentation screenshot/layout test реального composable для короткого viewport, если его
  компиляция добавлена в workflow;
- финальная приёмка — новый APK и фото владельца, а не Python board.

## 5. Область изменений и запреты

Разрешены только mobile Home geometry/compositor/control deck/eye integration, их JVM/Python
tests, simulator и актуальная документация.

Нулевой diff обязателен для `TvHomeScreen.kt`, `TvEskizHome.kt`, `TvEskizSpec.kt`,
`SFANavigation.kt`, `tvm_*`, TV tests/simulators, backend, API, VPN runtime, signing, workflows,
release и OTA. Нельзя merge в `main`, публиковать release или OTA.

## 6. Критерии приёмки

1. На присланном типе viewport логотип сидит на картуше, а все подписи и иконки находятся в
   своих резных плитах до и после scroll.
2. Scroll остаётся единым; fixed hero не движется.
3. Глаз читается как отверстие/живой элемент внутри мозаики без второй окружности и видимого шва.
4. Размер и анимационная анатомия глаза сохранены.
5. 390×844, 390×797 и короткий 320×568 не используют индивидуальные offsets.
6. Старый revolver/flattened Home и дублирующий UI не возвращаются.
7. TV/backend/release/OTA diff равен нулю.
