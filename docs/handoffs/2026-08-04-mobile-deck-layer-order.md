# Handoff — Mobile Home: движущаяся дека поверх бокового орнамента

**Обновлено:** 04.08.2026  
**Репозиторий:** `evgenmay1978-del/proectmaestro-vpn`  
**Рабочая ветка:** `codex/mobile-deck-layer-order`  
**База:** `codex/mobile-4d-deck`  
**Последний содержательный checkpoint перед созданием этого файла:** `5b432ea73919b1eacab0c87532de352a1147194d`  
**Статус:** решение и процесс утверждены; код ещё не изменён; PR/нового CI/APK ещё нет.

Текущий HEAD всегда перепроверять по ветке: commit самого handoff и последующие документационные checkpoints закономерно новее указанного выше SHA.

## 1. Решение владельца

Владелец подтвердил найденный QA-интерфейс и выбрал вариант 1:

- дуга протоколов уже правильно движется поверх бокового орнамента;
- плитки Telegram/МАКС/WhatsApp должны двигаться и перекрывать орнамент так же;
- весь ряд `Ввести логин / Тест сети / Подключить телефон` должен вести себя так же;
- позиции, скорость скролла и дизайн не менять;
- допиливать только тестовые сборки;
- к обновлению переходить только после отдельной фразы **«добро на обновление»**.

Фраза пользователя «153» была ошибочно принята нами за версию сборки. Это неверно. Текущая тестовая линия workflow использует `VERSION_NAME=1.0.92-test`.

## 2. Точная исходная APK

Дефект воспроизведён владельцем на последней документированной test APK:

- implementation SHA: `120fb816f4fd8be6c05f328d33d36089af9fbe54`;
- branch: `codex/mobile-4d-deck`;
- GitHub Actions run: [30764526376](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/30764526376);
- artifact: `maestrovpn-tv-test-apk`;
- artifact id: `8838559790`;
- size: `177365255` bytes;
- digest: `sha256:64aadd731303732a1def8c5fb01db95510197ef730eb2514db10cf377100ac25`;
- [прямая страница artifact](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/30764526376/artifacts/8838559790).

Это repro baseline, не новая исправленная APK.

## 3. Точное сохранённое изображение

Нужный созданный ранее QA-рендер сохранён в GitHub:

- path: `docs/evidence/2026-08-04-owner-home-scroll-proof-qa.svg`;
- evidence commit: [`aa9badc6a7897d74b9e77218b63b2649f322af61`](https://github.com/evgenmay1978-del/proectmaestro-vpn/commit/aa9badc6a7897d74b9e77218b63b2649f322af61);
- SVG blob: `b9d1cd19a4fc1229af7e30103afda036311acef3`;
- вложенный исходный JPEG: `450×530`, `28512` bytes;
- JPEG SHA-256: `ab153d2a00c05a7326b42ed7856b42dd01dd6f382f79f0ff04716aaab4d85add`;
- [неизменяемая raw-ссылка](https://github.com/evgenmay1978-del/proectmaestro-vpn/blob/aa9badc6a7897d74b9e77218b63b2649f322af61/docs/evidence/2026-08-04-owner-home-scroll-proof-qa.svg?raw=1).

На нём слева «Начало деки», справа «Дека прокручена на 64 dp», заголовок «Home — fixed hero и единый скролл нижней деки».

Это QA-симуляция, не скриншот физической APK и не эталон `04`.

## 4. Не путать доказательства

- `04-owner-selected-home-2026-07-31.jpg` — исходный эталон владельца.
- `01-core-flow-4d.png` — ранний исторический storyboard.
- `05/06` — art-only материалы.
- `owner-home-scroll-proof-qa` — созданная QA-симуляция тестового Home.
- Только снимок/видео установленной test APK доказывает реальный runtime.

Никогда не показывать `01`, `04`, `05` или `06` как доказательство текущей тестовой реализации.

## 5. Подтверждённая причина

Один `ScrollState` уже правильно используется Compose-декой и слоями `console`, `contacts`, `arc`. Все три получают одно смещение:

```text
+25 dp - scrollState.value
```

и один верхний clip:

```text
deckTop = 434 dp
```

Ошибка только в runtime z-order.

Текущий порядок:

```text
wood → console(scroll) → contacts(scroll) → frame → cartouche → vines →
arc(scroll) → ring
```

Целевой утверждённый порядок:

```text
wood → frame → cartouche → vines → console(scroll) → contacts(scroll) →
arc(scroll) → ring
```

Generated manifest сохраняет старый packing order; WebP/atlas не перегенерируются.

## 6. Точные файлы задачи

Production:

- `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DHome.kt`

RED/GREEN contract:

- `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DGeneratedAssetsTest.kt`

Существующий simulator, который нужно синхронизировать, а не дублировать:

- `ops/phone-screen-sim.py`

Документация:

- `docs/superpowers/specs/2026-08-04-mobile-deck-foreground-scroll-design.md`
- `docs/superpowers/plans/2026-08-04-mobile-deck-foreground-scroll.md`
- `docs/agent-working-contract.md`
- этот handoff;
- `CONTEXT_HANDOFF.md`;
- `design-qa.md`.

## 7. Уже выполнено

- [x] Найдена точная предыдущая test APK и её artifact.
- [x] Найден точный ранее созданный QA scroll-proof.
- [x] Изображение сохранено на GitHub с исходным hash.
- [x] Причина локализована до z-order; scroll delta и geometry исключены.
- [x] Владелец выбрал вариант 1 и подтвердил используемый интерфейс.
- [x] Зафиксирована spec.
- [x] Зафиксирован рабочий контракт GitHub-only.
- [x] Зафиксирован подробный TDD implementation plan.
- [ ] Production-код не изменён.
- [ ] RED-контракт не закоммичен.
- [ ] Draft PR не открыт.
- [ ] Новый Actions run не запускался.
- [ ] Исправленная test APK не создана.
- [ ] Device QA владельцем не выполнялся.

## 8. Журнал ошибок этой сессии

1. Сначала был показан эталон `04`, хотя владелец просил ранее созданную тестовую реализацию.
2. Был создан/показан нерелевантный новый визуал вместо поиска существующего результата.
3. Был показан art-only материал `06`, который не является интерфейсом test APK.
4. Ранний `01-core-flow-4d.png` рассматривался как кандидат на текущую реализацию; это исторический storyboard.
5. Фраза про «153» была неверно истолкована как номер нужной сборки, хотя владелец говорил о тестовых сборках.
6. Поиск повторялся по одним и тем же GitHub-местам после того, как уже было установлено: нужный bitmap создавался в игнорируемом `build/phone-screen-sim/` и не был загружен как artifact.
7. Неудачи и точка продолжения не были сразу записаны в постоянный handoff, поэтому контекст приходилось восстанавливать повторно.
8. Исправляющее действие: точный JPEG восстановлен один раз, его неизменяемые пиксели сохранены в GitHub внутри SVG; spec, plan, contract и этот failure log теперь находятся в рабочей ветке.

Что не повторять без новых данных:

- не искать QA-JPEG снова в старых PR/Actions;
- не выдавать reference/art board за test implementation;
- не начинать поиск от «версии 153»;
- не запускать проект на Windows;
- не создавать новый simulator вместо исправления `ops/phone-screen-sim.py`;
- не считать успешный upload step доказательством существования пригодной APK.

## 9. Границы и запреты

Не менять:

- TV, `tvm_*`, D-pad/focus/Back;
- backend, API, VPN-runtime;
- geometry, art, atlas, WebP;
- callbacks, hit targets, semantics;
- workflow-файлы;
- version/signing;
- `main`, Release, OTA.

Разрешён только draft PR в `codex/mobile-4d-deck` и workflow `.github/workflows/android-test.yml`. На Windows ничего не выполнять.

## 10. Следующее точное действие

1. Изменить только `Mobile4DGeneratedAssetsTest.kt`: отдельно закрепить неизменный manifest order, целевой runtime order и состав scroll-слоёв.
2. Commit: `test: define foreground mobile deck order`.
3. Открыть draft PR `codex/mobile-deck-layer-order → codex/mobile-4d-deck`.
4. Дождаться ожидаемого RED: build проходит, новый runtime-order unit test падает на текущем порядке.
5. Записать run ID и запретить выдачу RED artifact владельцу.
6. Только затем переставить шесть записей `mobile4DHomeReliefLayers` и получить GREEN.
7. После GREEN синхронизировать порядок в существующем `ops/phone-screen-sim.py`, проверить итоговый SHA и реальный APK artifact.
8. Передать владельцу только итоговую test APK для проверки на телефоне.
9. Даже после визуального одобрения не выполнять release/OTA до фразы **«добро на обновление»**.
