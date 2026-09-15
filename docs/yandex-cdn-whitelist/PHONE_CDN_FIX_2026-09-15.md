# Телефонный CDN и тестовый APK — checkpoint 15.09.2026

Серверы владелец подтвердил рабочими; HAPP и INCY физически подключаются через CDN. В этой точке исправлялся только клиент MaestroVPN. Production-серверы, обычный VPN, ТВ, main и stable OTA не менялись.

## Что исправлено

В `656430a90607e140aba6806c4821c9ea0a94866a`:

- `WhiteListRuntime.whiteListRenewalDeadline` сохраняет текущий действующий permit при кратком `Denied`/`Unavailable`, но не продлевает его и не принимает поздний ответ после дедлайна;
- `WhiteListSelection.whiteListMenuPreview` сохраняет названия CDN-серверов в том же аккаунте/сети при временном отказе, но сбрасывает `previewDeadline`, поэтому сохранённые названия не дают права на новое подключение;
- существующая авторизация, выбранный аккаунт, ordinary VPN, cellular-only guard и единственный `VpnService` сохранены.

До этой правки один ответ `Denied` очищал CDN labels и немедленно завершал сессию через `BoxService.stopService()`. Это объясняло симптом «меню CDN остаётся, серверы исчезают, интернет пропадает» при кратком клиентском отказе runtime-запроса.

## Regression и сборка

Сначала тест был отправлен без production-правки. После исправления GitHub run `34940736065` на `4995fa2` упал именно на двух новых ожиданиях `WhiteListRuntimeTest`; это подтверждённый RED.

После реализации GitHub run `34941407491` на `656430a90607e140aba6806c4821c9ea0a94866a` завершился SUCCESS. Проверялись `PhoneCdnPurchaseLinkTest` и `WhiteListRuntimeTest`; собран signed candidate `1.0.160`, versionCode `1016005`.

Последний preview run `34947214881` на `7976bea9a832e82a27e2e175013ea0c375b7c285` собрал APK job SUCCESS и создал emulator/validation artifacts. APK локально сохранён:

`C:/Users/User/Documents/Codex/2026-09-14/c-users-user-documents-codex-2026/outputs/MaestroVPN-phone-test-1016005-latest/MaestroVPN-phone-test-1016005.apk`

- размер: `202 794 617` байт;
- SHA256: `a38031b571e63580d56556b7d2df412dcdbab7eb3cb278f9c5482de4ae15624b`;
- artifact archive SHA256: `c442097842362c580d080b90a1e1b28d4368bea6179600faf1d27cf49ae853e0`;
- GitHub artifact ID: `10387074230`.

## Реальный вид главного экрана

Preview artifact ID `10388752010` содержит реальные PNG из APK для профиля `412×892 dp`:

`C:/Users/User/Documents/Codex/2026-09-14/c-users-user-documents-codex-2026/work/preview-artifact-7976bea/ui-captures/fixture-console-phone-412-off-status.png`

Кадр показывает цельную тёмную деревянную консоль с единой золотой рамкой, логином и кнопкой «Бот», двумя кошельками VPN/CDN, центральным круглым резным медальоном, единым переключателем «Обычный VPN / CDN», панелью сервера, парой кнопок покупки/продления и четырьмя пунктами нижней навигации. Дополнительно сохранены `connecting-status`, `on-status`, `error-status` и `off-controls`.

На `412×892 dp` основной экран помещается целиком и близок к предоставленному референсу. На 360×780 dp длинный текст ошибки требует прокрутки; это ограниченный низкий экран, а не размер Samsung S26 Ultra.

## Что не подтверждено

- Физический Samsung S26 Ultra не подключён к ADB, поэтому установка на нём и реальный VLESS/XHTTP-трафик именно из APK не проверены.
- Preview instrumentation после запуска Compose зависает в старом harness на первом сценарии и завершается timeout; artifact с первым 412-dp кадром при этом выгружается. Это ограничение эмуляторного стенда, не ошибка runtime regression.
- Windows Xray/эмуляторные unit-проверки не заменяют мобильную сеть HAPP/INCY. Владелец отдельно подтвердил, что HAPP и INCY через CDN работают.

## Полный журнал 15.09.2026 для Astra 6

Помимо APK сегодня был сделан backend renewal bridge `16aed67`: исправлен разрыв `/admin/renew`, при котором обычная дата подписки продлевалась, а CDN billing period оставался старым. Bridge создаёт идемпотентный zero-grant customer-source period до новой даты с generation/expiry/projection/CAS/RPO guards; purchased bytes, lifetime consumption, native-order path и платежи не меняются. Реальный SQLite regression и соседние customer tests прошли. На сервер bridge не устанавливался.

Для phone CDN добавлены RED regression `cd206d0`, workflow hook `5f17a7f`, CI setup fix `4995fa2`, затем client fix `656430a`; GREEN run `34941407491` прошёл. Позже в той же ветке сделаны UI commits `42b4128`, `01f2a11`, `c0bc409`, `799bbb1`, `5c288d2`; `01f2a11` был ошибочно слишком большим и отклонён, `c0bc409` дал белую system bar, `5c288d2` вернул compact medallion. Последний runtime fix `9ba9c4d` заменяет оба `WhiteListSelection.clear(... retainLabels=false)` на `retainLabels=true`, но был сделан после отправленного APK.

Запускались GitHub runs `34947214881`, `34950945260`, `34954623359`, `34957674116`, `34959953693`, `34960823183`, `34961915214`; APK jobs успешны, несколько preview jobs завершались старым instrumentation timeout, хотя PNG выгружались. Скачаны и проверены artifacts, APK identity/signature/SHA. Локальный emulator получил ADB daemon mismatch и System UI/Pixel Launcher ANR, поэтому live CDN там не подтверждён.

Через встроенный Telegram Web открыт личный аккаунт `wapmixx`, найден чат `Saved Messages`, прикреплён и отправлен APK `MaestroVPN-phone-test-1016005-final-5c288d2.apk` размером 193.4 MB; upload завершён. Локальная ссылка с телефона владельца не скачивалась. ImageGen вернул HTTP 429, новой генерации не создано. Direct PNG из `https://ibb.co/G3v7JNGY` остаётся визуальным эталоном; пользователь требует перерисовку Compose-компонентами без цельного screenshot overlay и точный медальон.

Новых веток сегодня не создавалось. Все изменения — одна ветка `codex/yandex-cdn-whitelist-task3-sync`; локальные старые `codex/*` ветки были ранее существующими. Серверы, TV, main/stable OTA, обычный VPN, аккаунт `wapmix`, покупки, продления, paid ledger и балансы не менялись.

### Stop state

Текущий локальный HEAD: `9ba9c4d`; remote branch: `5c288d2`. APK, отправленный владельцу, собран на `5c288d2` и не содержит `9ba9c4d`. Поэтому нельзя считать, что CDN servers в отправленном APK исправлены. Astra 6 должна начать с локального `9ba9c4d`, собрать следующий test APK, проверить CDN при наличии ГБ и только затем решать дальнейшие UI changes.

## Границы продолжения

Не публиковать Release/OTA и не устанавливать APK на production-устройства без отдельного решения владельца. Не менять ТВ, серверы, обычный VPN, оплату, баланс или подписки. Следующий чат Astra 6 должен проверить diff `656430a`, APK `1016005`, screenshots и затем решить, нужен ли дополнительный UI-проход для фактической плотности Samsung S26 Ultra.
