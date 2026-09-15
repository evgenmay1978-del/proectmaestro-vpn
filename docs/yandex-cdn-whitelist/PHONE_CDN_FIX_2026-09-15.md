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

## Границы продолжения

Не публиковать Release/OTA и не устанавливать APK на production-устройства без отдельного решения владельца. Не менять ТВ, серверы, обычный VPN, оплату, баланс или подписки. Следующий чат Astra 6 должен проверить diff `656430a`, APK `1016005`, screenshots и затем решить, нужен ли дополнительный UI-проход для фактической плотности Samsung S26 Ultra.
