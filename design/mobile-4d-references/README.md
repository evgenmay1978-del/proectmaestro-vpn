# MaestroVPN Mobile 4D — эталонные референсы

Эта папка хранит визуальные источники мобильного интерфейса MaestroVPN. Перед
любой работой с телефонным UI сначала прочитать
[`CLAUDE_INSTRUCTIONS.md`](CLAUDE_INSTRUCTIONS.md).

Технические требования к новым исходным слоям находятся в
[`../mobile-asset-redraw/SPEC.md`](../mobile-asset-redraw/SPEC.md).

## Иерархия источников Home

1. `08-owner-installed-test-home-2026-08-08.jpg` — единственный авторитетный
   эталон полного реально установленного Home: title, frame, medallion
   registration, status, phone, contacts, protocol arc и lower deck. Файл имеет
   точный размер `591×1280`, SHA-256 `9251457407f3aeee17b5281b32634e6c0d03e7fce3e9db12c16706444a9f800b`.
2. `04-owner-selected-home-2026-07-31.jpg` — только история выбранного
   художественного направления глаза и окружающего eye-surround. Он не является
   текущей установленной сборкой. Из него запрещено брать full-layout, status,
   contacts, protocol arc или lower-deck geometry.
3. `09-owner-selected-closed-eye-v2-2026-08-08.png` - current owner-selected closed-eye/eye-surround art source only; `04` is historical only.
4. Boards `00..03` — исторические референсы продуктового потока; они не
   переопределяют полный Home `08`.

## Файлы

- `08-owner-installed-test-home-2026-08-08.jpg` — фотография экрана текущей
  тестовой сборки, от которой выполняется замена центральной мозаики.
- `09-owner-selected-closed-eye-v2-2026-08-08.png` - current closed-lid/eye-surround art source; RGB 852×1846; archive SHA-256 `97cf7cddbeb780fd23ad8035c394183ce680b149895f0b26cbbb3a004122ac81`.
- `04-owner-selected-home-2026-07-31.jpg` - historical eye/eye-surround art direction only.
- `00-current-mobile-ui.png` — исторический интерфейс из
  `ops/phone-screen-sim.py`.
- `01-core-flow-4d.png` — исторический board главного экрана и состояний.
- `02-subscription-activation-4d.png` — подписка и активация.
- `03-settings-advanced-4d.png` — настройки и дополнительные экраны.

## Авторитетная геометрия Home

Полный raster-baseline — `08` в его нативном размере `591×1280`. Старые
landmarks, измеренные на `04` как на `390×844`, больше не являются
layout-контрактом. Production dp-геометрия подтверждается текущими Kotlin
constants и сравнением с `08`; её нельзя заново выводить из `04`.

При scripted-preview допустимы изменения только в явно заданной области
центрального eye-surround, а для connected-состояния — в статусной области.
Остальная регистрация полного экрана сохраняется от `08`.

## Неподвижные правила продукта

- `ОТКЛЮЧЕНО` — глаз полностью закрыт, радужка и зрачок не видны.
- `ПОДКЛЮЧЕНИЕ` — глаз начинает открываться, появляется слабое свечение.
- `ПОДКЛЮЧЕНО` — глаз полностью открыт, живой и реагирует на пользователя.
- Сохраняются тёмное дерево, бронзовая резьба, изумрудный глаз и золотая
  типографика MaestroVPN.
- «4D» означает глубину, параллакс, свет, движение и физически убедительные
  материалы. Это не повод добавлять новые панели и накладывать декор поверх
  старого интерфейса.
- ТВ-версия не изменяется.

## Назначение

Сначала показать владельцу повторяемые состояния на точном установленном Home
`08`. После одобрения завершить ring/runtime и получить тестовый APK на GitHub.
После проверки APK на физическом телефоне OTA рассматривается только по
отдельному явному разрешению владельца.

## Current closed-eye source - option 2

`09-owner-selected-closed-eye-v2-2026-08-08.png` is the authoritative closed-eye art source: RGB 852×1846, SHA-256 `97cf7cddbeb780fd23ad8035c394183ce680b149895f0b26cbbb3a004122ac81`. `04-owner-selected-home-2026-07-31.jpg` is historical only.

Rebuild/check the tracked RGB 1254×1254 master without rendering `home_ring_*`:

```text
python ops/mobile-eye-surround-assets.py --material-only
python ops/mobile-eye-surround-assets.py --material-only --check
```

Current master SHA-256: `0f5d565c2a269579166723b7b59532cde7032cb0b7ea668847b95c5531f278ca`.
