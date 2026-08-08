# MaestroVPN mobile Home — замена мозаики живым глазом

**Статус:** утверждённое направление от 05.08.2026; фактический layout-baseline
исправлен владельцем 08.08.2026 по установленной тестовой сборке.

**Ветка:** `codex/mobile-4d-deck`.

## Авторитетные визуальные источники

- Единственный эталон полного Home, его статуса, контактов, дуги протоколов и
  нижней деки:
  `design/mobile-4d-references/08-owner-installed-test-home-2026-08-08.jpg`,
  ровно `591×1280`, SHA-256 `9251457407f3aeee17b5281b32634e6c0d03e7fce3e9db12c16706444a9f800b`.
- `design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg` остаётся
  только историей художественного направления глаза и окружающего его
  eye-surround. Запрещено брать из него full-layout, status, contacts, protocol
  arc, lower-deck geometry или показывать его как текущую установленную сборку.

Эта спецификация заменяет прежний контракт «маленький глаз поверх радиальной
мозаики» из документов 02.08. Центральная мозаика больше не является допустимой
частью Home. Все изменения должны начинаться от установленного Home `08`, а не
от старого концепт-макета `04`.

## Результат

В центре существующего медальона установленной тестовой сборки находится крупный
живой анатомический глаз. Всё пространство, которое занимала радиальная
изумрудная мозаика, физически заменено непрерывным тёмно-изумрудным рельефом
верхнего и нижнего века с трещинами и бронзовыми прожилками. Художественное
Material direction for the closed lids/eye-surround compares only to `09-owner-selected-closed-eye-v2-2026-08-08.png`; layout registration and the surrounding medallion compare only to `08`.
окружение медальона — только с `08`.

Это замена пикселей существующего `ring`, а не новый диск, Canvas или bitmap,
положенный поверх старой мозаики. В runtime остаётся один статический владелец
окружения — `home_ring_{l,c,r}` — и один динамический владелец анатомии —
`LivingEyeMedallion`. Остальной установленный Home не перестраивается по
старому макету.

## Геометрия на 390×844

Эти числа принадлежат production-геометрии и проверяются относительно полного
layout-baseline `08`; они не выводятся из `04`.

- Центр композиции: `(195.0, 258.45)` dp.
- Внешний бронзовый медальон: примерно `338×309` dp; его геометрия не меняется.
- Область заменяемого материала: круг диаметром `232±1` dp,
  bbox около `(79,142)–(311,375)`.
- На master-холсте `2160×4670` материал центрирован в `(1080,1751)` и имеет
  радиус `644 px`. Переходная кромка прячется под существующей бронзой.
- Бронзовый runtime clip остаётся около `214.18` dp.
- Анатомия глаза увеличивается одним uniform transform `1.10` относительно
  текущего состояния и смещается примерно на `(+3.5,+7)` dp.
- Ожидаемая открытая апертура: примерно `181×69` dp,
  bbox `(107.5,231.5)–(288.5,300.5)`, допустимое расхождение `±4` dp.

Запрещено отдельно масштабировать радужку, зрачок, блик или контур век. Один и тот
же `LivingEyeLayerFit` преобразует open-layer, aperture, sclera, iris, pupil,
catchlight, gaze и контактный шов.

## Арт и освещение

- Новый master-материал хранится отдельно от runtime-слоёв и не содержит глаза,
  текста, самоцветов, внешней бронзы, радиальной сетки или мозаичных спиц.
- Один master используется для `_l/_c/_r`; световые варианты создаются
  детерминированно, поэтому геометрия трещин не прыгает при наклоне телефона.
- Скрипт заменяет RGB только внутри material-mask, сохраняет прежнюю alpha-маску
  и байты внешнего кольца вне переходной кромки, затем синхронно обновляет
  `kit/home_ring_*` и `source/home_ring_*`.
- Атлас пересобирается штатным `ops/mobile-4d-assets.py`; второго runtime-слоя
  окружения глаза не появляется.

## Живое поведение

Сохраняются существующие coroutine/state-механизмы:

- случайное моргание и фазы `0/.25/.5/.75/1`;
- случайный взгляд и взгляд за касанием;
- реакция зрачка;
- catchlight;
- состояния connected / connecting / disconnected;
- отключение glow и анатомических слоёв при полном закрытии.

При полном закрытии виден новый рельеф и тонкий контактный шов. Старая мозаика не
должна проявляться ни в одной фазе.

## Повторяемое лёгкое превью

До тяжёлой генерации и APK два состояния показываются скриптом:

```powershell
python ops/mobile-eye-state-preview.py
python ops/mobile-eye-state-preview.py --output-dir build/mobile-eye-state-preview-qa --scale 1
python ops/mobile-eye-state-preview.py --check
```

Точные выходы в выбранном каталоге:

- `home-disconnected.png`;
- `home-connected.png`;
- `home-eye-states-comparison.png`.

Это сценарное превью на точном raster-baseline `08`, а не runtime screenshot,
не Android render и не доказательство готовности APK. Визуальное одобрение
владельца разрешает продолжить следующую стадию, но не разрешает OTA.

## Приёмка

1. `ops/mobile-eye-surround-assets.py --check` доказывает воспроизводимость,
   неизменность внешнего кольца, совпадение alpha у `_l/_c/_r` и синхронность
   `kit/source`.
2. `ops/mobile-4d-art-check.py --group ring` проверяет material-mask и отсутствие
   прежнего mosaic-контракта; `--selftest` обязан ловить подмену старой мозаикой.
3. Pure Kotlin-тест фиксирует uniform scale `1.10`, нормализованный сдвиг и
   ожидаемые aperture-ratios, сохраняя правила 70/30 и полного закрытия.
4. `ops/phone-screen-sim.py --eye-phases-only` показывает пять фаз на новом
   материале; phase `1.0` не содержит open-eye alpha и не открывает мозаику.
5. Full layout, status, contacts, protocol arc and lower deck compare only to `08`; closed lids and eye-surround compare only to `09-owner-selected-closed-eye-v2-2026-08-08.png`. `04` is historical only.
6. Тяжёлая генерация ring/atlas, полный simulator, Gradle и APK выполняются
   только на GitHub runner; слабый Windows-компьютер владельца ими не нагружается.
7. GitHub Actions `android-test.yml` проходит `assembleOtherDebug` и
   `testOtherDebugUnitTest`; APK берётся только из artifact этого workflow.
8. После визуального одобрения превью владелец отдельно проверяет точный APK на
   физическом телефоне. OTA возможна только после ещё одного явного разрешения.

## Не менять

TV, D-pad/focus/Back, backend, API, VPN runtime, scroll-owner нижней деки,
статусные callbacks, tilt/parallax/memory policy, signing, release, OTA и `main`.

## Option-2 closed-eye source correction (2026-08-08)

The closed-eye eye-surround is compared only with archived owner selection
`09-owner-selected-closed-eye-v2-2026-08-08.png` (RGB 852×1846, SHA-256 `97cf7cddbeb780fd23ad8035c394183ce680b149895f0b26cbbb3a004122ac81`). `04` is historical art direction,
not the current closed-lid reference. The deterministic RGB 1254×1254 master
SHA-256 is `0f5d565c2a269579166723b7b59532cde7032cb0b7ea668847b95c5531f278ca`.

Regenerate or verify it without rendering `home_ring_*`:

```text
python ops/mobile-eye-surround-assets.py --material-only
python ops/mobile-eye-surround-assets.py --material-only --check
```
