# MaestroVPN — актуальный контекст и передача работы

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

Не записывать сюда токены, пароли, приватные URL подписок, данные клиентов или
непроверенные предположения.
