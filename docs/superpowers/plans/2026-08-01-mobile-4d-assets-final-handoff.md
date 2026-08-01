# Mobile 4D — финальная передача ассетов Claude

Дата: 01.08.2026. Ветка: `codex/mobile-4d-deck`. Claude видит только GitHub; локальные файлы Codex источником правды не являются.

## Что готово

| Слой | Где лежит | Проверенная геометрия |
|---|---|---|
| `wood` | `design/mobile-asset-redraw/source/home_wood_{l,c,r}.png` | 2160×4670 RGB, ровно 16 досок |
| `frame` | `source/home_frame_{l,c,r}.png` | bbox `[48,48,2112,4622)` |
| `cartouche` | `source/home_cartouche_{l,c,r}.png` | bbox `[304,226,1867,615)`, без текста |
| `vines` | `source/home_vines_{l,c,r}.png` | bbox `[72,72,2088,4598)`, центр и картуш прозрачны |
| `arc` | `source/home_arc_{l,c,r}.png` | bbox `[0,3150,2160,3905)`, ровно 7 секций |
| `ring` | `source/home_ring_{l,c,r}.png` | bbox `[156,827,2005,2676)`, Ø1849, центр `(1080,1751)` |
| `console` | `design/mobile-asset-redraw/kit/home_console_{l,c,r}.png` | bbox `[44,4071,2116,4647)`, 3 зоны |

`arc` и `ring` уже перенесены из `kit/` в runtime `source/` и совпадают с утверждёнными файлами байт-в-байт. `console` оставлена в `kit/`, потому что текущий генератор ещё не знает этот слой.

Ключевые арт-коммиты: `c6690f8` ring; `a65ecc1` arc; `c4b4c99` console; `5e4f0d0` frame; `5449f42` cartouche; `f741c58` vines; `70755be` wood.

## Проверки Codex

- `validate_sources`: PASS, 18 runtime PNG;
- `python -m unittest ops/test_mobile_4d_assets.py -v`: PASS, 7/7;
- `ops/mobile-4d-art-check.py --group arc`: PASS;
- `ops/mobile-4d-art-check.py --group console`: PASS;
- `ops/mobile-4d-art-check.py --selftest`: PASS;
- полный аудит 21 PNG: PASS;
- дерево: RGB, 16 досок, median luma 24.3, p95 32.3;
- дуга: dome 174.78 px, R−G 10.63, relief 5.02%;
- консоль: R−G 9.41, relief 5.82%.

Art-only превью без живого глаза и программного заголовка: `design/mobile-4d-references/05-mobile-4d-art-only-{l,c,r}-2026-08-01.png` и `06-mobile-4d-art-only-lcr-2026-08-01.jpg`.

## Что Claude должен сделать дальше

1. Прочитать полностью `CLAUDE_MOBILE_REBUILD.md`, `CONTEXT_HANDOFF.md`, этот файл, общий план, спеки дуги/консоли и `design/mobile-asset-redraw/SPEC.md`.
2. Добавить `console` в генератор, generated Kotlin и runtime parallax. Рекомендуемый z-order: `wood → console → frame → cartouche → vines → arc → ring →` живой глаз/текст/контролы. Не дублировать слой отдельным механизмом.
3. Запустить генератор из закреплённого окружения Pillow 11.3.0 + libwebp 1.5.0 из `ops/README.md`; обновить atlas и `Mobile4DGeneratedAssets.kt`; проверить попиксельную реконструкцию.
4. Обновить геометрию живого глаза: center `(1080,1751)`, ring Ø1849. Глаз не заменять. Отключено — закрыт, подключение — полуоткрыт, подключено — открыт.
5. Рисовать `MaestroVPN` кодом Playfair в `[382,298,1789,487)`, не запекать текст.
6. Построить `PhoneHomeControlDeck` начисто. Не оставлять `PhoneRevolverMenu`, старые градиенты, плоскую сцену или другой mobile UI под новым.
7. Сохранить callbacks, протоколы/блокировки, покупку/триал, контакты, login/network/update и test tags из `CLAUDE_MOBILE_REBUILD.md`.
8. После проверки ссылок удалить мусор старого mobile UI. ТВ и `tvm_*` не трогать.
9. Проверить обновление: прежний `applicationId`, подпись, растущий `versionCode`, миграции и OTA; совсем древние версии могут быть вне гарантии.
10. APK собирать только GitHub Actions. Отдать artifact URL и SHA. Staging-ветка не является готовым APK до регенерации атласа, подключения console и удаления старого mobile UI.

## Жёсткие запреты

- Никаких наслоений на старый мобильный интерфейс.
- Не запекать глаз, текст, подписи протоколов, иконки, выбор или чужие тени.
- Не использовать `mobile_home_scene.webp`, шестисекторную дугу или овальное пустое кольцо.
- Не трогать ТВ.
- Не force-push; Claude сверяет GitHub SHA.
