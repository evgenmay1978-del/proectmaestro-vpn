# MaestroVPN — покрытие мобильных экранов

Проверено по активной навигации и исходникам ветки `codex/mobile-4d-interface` через
implementation checkpoint `c15b4e3` (31.07.2026). Контракт этой работы: ровно **6 достижимых из обычного
запуска экранов телефона + 1 dialog**. Состояния одного route не считаются отдельными
экранами.

## Матрица покрытия

| № | Route / элемент | Исходные composable | Новая реализация телефона | Ключевые состояния и проверяемое поведение |
|---:|---|---|---|---|
| 1 | `tvhome` | `TvHomeScreen` → `Mobile4DHome`, `LivingEyeMedallion`, `PhoneRevolverMenu` | Полная многослойная 4D-сцена: atlas L/C/R, parallax, runtime shadows, существующий живой глаз, Playfair title и revolver UI | `Отключено` — глаз полностью закрыт; `Подключение` — полуоткрыт; `Подключено` — открыт. Выбор протокола и account card остаются секциями Home. `MobilePremiumFlowsTest` фиксирует три eye-state, connect/disconnect action и старые premium tags. |
| 2 | `claim` | `ClaimScreen` → `ClaimPhoneForm` | `MobilePremiumScreen` делегирует `MobilePremium4DShell`; premium panel, text field, button и error surface | Idle, ввод логина/кода, Busy с отключённым вводом/кнопкой, Done с возвратом Home, Error + retry. Автотесты фиксируют controls, Busy и Error. |
| 3 | `trial` | `TrialScreen` → `TrialPhoneForm` | Тот же лёгкий `MobilePremium4DShell`, без Home atlas; premium form controls | Idle, ввод ника, Busy, Done, Error + retry. Автотесты фиксируют controls, Busy и Error. |
| 4 | `buy` | `BuyScreen`, `PhoneTariffSelection`, `PhonePaymentContent`, `PhonePaymentResultContent` | Phone-ветка `BuyScreen` использует `MobilePremium4DShell`; тарифы и все payment states используют premium panel/button/loading/error kit | Loading tariffs, Tariffs, AwaitingPayment, AwaitingConfirm, Activating, Done, Error. Сохраняются QR оплаты, URL оплаты, код заказа и `Я оплатил`. Автотесты фиксируют порядок тарифов, callbacks и payment-state transitions. |
| 5 | `scanqr` | `ScanQrActivateScreen` → `QRScanSheet`, `QRScanViewModel`, `CameraPreview` | `MobilePremiumSheetSurface` для сканера и `MobilePremiumDialogSurface` для app-owned ошибок; live camera preview остаётся незакрытым | Запрос CAMERA permission, denial explanation, loading/preview, front camera, torch, vendor analyzer, QRS progress, успешный MaestroVPN subscription QR, unsupported payload и scanner error. Системный permission dialog не стилизуется. |
| 6 | `split` | `PerAppProxyScreen`, `PerAppProxyMenus`, `AppSelectionCard` | Только phone presentation переведён на `MobilePremium4DShell`; premium description/search, прежний app row с реальной иконкой и long-press copy | Loading, include/exclude, search, sort/filter, select/deselect all, import/export, app scan progress/empty/found, выбор и сохранение UID. System Back сначала закрывает поиск, затем вызывает route `onBack`; menu callbacks и service reload сохранены. |
| D1 | `IosKaringDialog` | `IosKaringDialog` → `ShareBody` | Phone-ветка использует `MobilePremiumDialogSurface`; TV сохраняет прежний `FantasyDialog` | Loading, NeedActivate, Failed, Ready; Android/iPhone segmented mode, black-on-white QR и Close. Это dialog поверх Home, не седьмой экран. |

## Общий mobile-кит

- `MobilePremium4DShell` — phone-only scaffold с safe-drawing/IME insets, Playfair top bar,
  48 dp Back/actions и compact/regular/expanded layout.
- Внутренние экраны не декодируют Home atlas и не рисуют cartouche, ring или eye.
- Лёгкий фон использует сдержанный walnut/light draw и существующую настоящую nine-patch
  `frame_panel`; старый `mobile_surface.webp` не является фоном этих migrated phone paths.
- `MobilePremiumPanel`, button, text field, setting row, switch, dialog и sheet surfaces сохраняют
  доступные роли, enabled/disabled state и минимум 48 dp для интерактивных целей.

## Явная граница телефона и ТВ

- Один universal APK сохраняется.
- В `TvHomeScreen` TV продолжает использовать `TvEskizHome`; только phone-ветка использует
  `Mobile4DHome`.
- `ClaimScreen`, `TrialScreen`, `BuyScreen`, `PerAppProxyScreen` и `IosKaringDialog` выбирают
  новую presentation только при `!isTv` либо через phone-only wrapper.
- `scanqr` достижим только из phone action; у TV нет camera route в обычном flow.
- `TvEskizHome`, `TvEskizSpec`, D-pad/focus/Back, TV geometry, `tvm_*` и TV simulators этой
  миграцией не меняются.

## Что намеренно исключено

- Protocol selector и account card — части `tvhome`, не отдельные экраны.
- Home connection states и Buy payment states — состояния своих экранов.
- QR permission/error и Split search/scan dialogs — состояния `scanqr`/`split`.
- Скрытые `Settings`, `Log`, `Groups` и их дочерние routes: normal-flow entry points отсутствуют.
- `profile/new`, `profile/edit/{id}` и вложенные editor routes: внешний OS-intent/internal flow.
- `Dashboard`, `Connections`, `Tools`: не зарегистрированы в активном `SFANavHost`.
- Android system permission UI: принадлежит ОС и не может быть стилизован приложением.
- Глобальные app-owned service/update/download overlays учитываются отдельно при overlay audit;
  они не увеличивают число экранов.

## Статус доказательств

- Source/navigation/cleanup audit: выполнен через `c15b4e3`.
- Python asset generation/`--check`: ранее PASS; 24 lossless WebP, reconstruction exact.
- Compose/JVM/instrumentation tests добавлены, но локально не запускались: владелец запретил
  Android build на слабом компьютере, а локально также отсутствует `libbox.aar`.
- Реальный Android compile/test и test APK должны пройти в GitHub Actions
  `.github/workflows/android-test.yml`.
- Device screenshots, 320×568 / 390×844 / landscape / font-scale 2.0 и окончательный visual QA
  остаются pending до CI artifact и проверки владельцем на телефоне.
