# S2 VLESS-Reality cutover — 12.09.2026

## Решение

Чешский Hysteria2 находился на S2, а не на S4. Его binary был текущим официальным Hysteria v2.9.2, поэтому обновление версии не устраняло наблюдавшиеся UDP/QUIC timeout/error. S4 обслуживает отдельный VLESS-Reality и в этой неисправности не участвовал.

S4 3x-ui/Xray не обновлялись. В live-инвентаре x-ui 3.4.0 и встроенный Xray 26.6.22 работали; официальный 3x-ui 3.7.0 предупреждает об автоматической миграции схемы при первом старте. Для несвязанного S2-сбоя этот риск не оправдан.

## Установка

- S2 service: maestro-vless-s2.service
- Xray: 26.7.28, commit 5ca6f4b
- Xray binary SHA256: 64d46afb80adea1bf97a0d467e83f4a9ac1ebd0995891e84bca3f1a1d1affb1d
- Transport: VLESS TCP Reality, vision, port 2096
- Access: существующие основные VLESS UUID; 42 активных клиента
- Source: 4d8e23747e82
- GitHub build-only: run 34696383136, tests skipped
- Artifact: 10299211030
- S1 panel/controller binary SHA256: 7881c7b08787a23adb50d141d00ca69763b8fee79a152e93b95405a829b79f42

Новый provisioning синхронизирует в S2 VLESS только активные записи, сохраняя server-side Reality key/target/listener. Новые клиенты получают S2 VLESS на том же UUID, что основной VLESS. Старые Hysteria credentials сохранены только в закрытых backup/store данных для отката и больше не публикуются.

## Наблюдаемый результат

Перед общим переключением owner-canary через официальный Xray 26.7.28 получил HTTPS 200 за 0,396 с. После синхронизации и production-выдачи точная выданная чешская ссылка получила HTTPS 200 за 0,357 с.

- format=links: 8 VLESS-ссылок, включая одну Чехию на 2096; Hysteria2 отсутствует.
- format=xray: 8 конфигураций = 4 ordinary VLESS + 4 CDN; Чехия VLESS присутствует один раз; X-Maestro-CDN=included.
- App/SFA JSON: S2 VLESS присутствует; Hysteria2 отсутствует.
- Profile-Title декодируется в MaestroVPN; Subscription-Userinfo сохранён.
- S1 panel/controller active, exact installed binary, controller healthz 200.
- S2 VLESS active/enabled, 42 клиента, TCP 2096.
- S2 AnyTLS и Telegram bot active; failed units 0.
- Hysteria unit not-found, UDP 8443 не слушается.
- UFW, CDN accounting, оплаты, балансы, заказы, приложение/OTA и S3/S4 не изменялись.

## Откат

- S1 panel/controller/env/unit: /var/backups/maestro-vless2-s1-20260912T133212Z
- S2 full user-set: /var/backups/maestro-vless-s2-users-20260912T132658Z
- S2 initial canary: /var/backups/maestro-s2-vless-canary-20260912T130031Z
- Retired Hysteria unit/config/binary: /var/backups/maestro-hysteria-retired-20260912T134101Z

Customer/order DB при откате кода не восстанавливать. Сначала вернуть S1 binary/env/unit, затем при необходимости восстановить Hysteria files из закрытого backup и выполнить systemctl daemon-reload и systemctl enable --now hysteria-server.service. S2 VLESS можно оставить параллельно или остановить отдельно.

## Первоисточники версий

- https://github.com/XTLS/Xray-core/releases/tag/v26.7.28
- https://github.com/apernet/hysteria/releases
- https://github.com/MHSanaei/3x-ui/releases/tag/v3.7.0
