# Разбор matrixlegend-code/vpn-cdn-installer — 12.09.2026

Источник: https://github.com/matrixlegend-code/vpn-cdn-installer

## Что фактически опубликовано

Полностью просмотрены main и вся Git-история. В 13 коммитах единственным файлом был README.md. Других веток, тегов и GitHub Releases нет. Не опубликованы:

- исходный код или исполняемый установщик;
- точные конфигурационные шаблоны Xray, Nginx, Caddy, 3x-ui или Remnawave;
- зафиксированные версии и контрольные суммы загружаемых компонентов;
- тесты, CI, журнал исправлений и воспроизводимые доказательства rollback;
- LICENSE.

Текущий README направляет за платным скриптом в Telegram. Исторический README показывал загрузку персонального payload с отдельного домена, chmod +x и запуск от root с токеном; также заявлял ограниченный срок ссылки и лицензирование. Текущая редакция убрала команду и подробности лицензирования. Это не доказывает вредоносность, но делает поставку непрозрачной: GitHub не позволяет проверить то, что фактически получит root-доступ к серверу.

## Заявленная архитектура

README описывает VLESS XHTTP Packet-Up по цепочке клиент → CDN → Nginx/Caddy → Xray → интернет и каскад с relay/exit. Заявлены три режима: новая панель и нода вместе, удалённая нода и добавление CDN к существующей панели. Поддерживаются Remnawave и 3x-ui 3.7.0, а также отдельные ветки настройки для VK Cloud, Yandex Cloud, TurboFlare, Beeline CDNvideo, Timeweb и Selectel.

Заявленные автоматические изменения очень широкие: Docker/Compose, панель, Xray, Nginx/Caddy, сертификаты, firewall, sysctl/BBR/TCP, geo-файлы, подписки, decoy и health checks. Без исходника нельзя проверить границы изменений, сохранность существующих клиентов и баз, порядок отката и повторный запуск.

## Что полезно для MaestroVPN

1. Отдельный профиль под каждого CDN — правильный принцип. Origin protocol, разрешённые методы, Host/SNI, cache и query handling нельзя копировать между провайдерами.
2. Для Yandex указан HTTPS origin и отдельная нода. Это совпадает с нашей действующей архитектурой.
3. Для Timeweb явно требуется выключить cache/compression и не игнорировать query parameters. Последнее принципиально для packet-up, когда session и seq находятся в query.
4. Для Selectel описаны увеличенные таймауты и отдельный DELETE/POST uplink. Это Selectel-специфичный профиль, а не универсальная настройка XHTTP.
5. Режим добавления CDN к существующей панели ближе к нашей безопасной модели, чем полная переустановка панели.

## Чего в README недостаточно

Для Yandex не опубликованы точные:

- XHTTP mode и extra;
- uplink HTTP method и data placement;
- sessionID/seq keys и placement;
- padding и XMUX;
- server/client Xray compatibility;
- Nginx/Caddy buffer и timeout settings;
- проверка длинного открытого потока, idle resume и повторного запроса;
- subscription renderer для HAPP, INCY, Karing и Mihomo.

Поэтому README не содержит нового параметра, который следует переносить в текущую MaestroVPN Yandex-схему.

## Сверка с первоисточниками

Yandex Cloud документирует, что query parameters могут учитываться в cache key; ignore_query_params=false сохраняет их различия. По умолчанию разрешены GET, HEAD и OPTIONS. POST, PUT, PATCH и DELETE блокируются, пока их явно не разрешат. Наша проверенная GET-body/query схема укладывается в текущие разрешённые методы. Selectel DELETE/POST нельзя переносить на Yandex без отдельной причины и настройки ресурса.

Официальный 3x-ui 3.7.0 включает Xray 26.7.28, но при первом запуске выполняет автоматические schema migrations и требует backup базы. Это подтверждает выбранную границу: здоровый S4 нельзя обновлять только потому, что платный установщик ориентируется на 3.7.0.

## Решение

Использовать этот репозиторий как обзор архитектур и список CDN-провайдеров. Не запускать закрытый установщик на production MaestroVPN. Если payload будет приобретён владельцем отдельно, безопасный путь — одноразовая чистая Ubuntu VM, фиксация хеша полученного файла, исходящих соединений и полного filesystem/service/firewall/sysctl diff, затем ручной перенос только минимальных проверенных параметров. Это отдельная будущая задача; сейчас ничего не приобреталось, не запускалось и на серверах не менялось.

## Источники

- https://github.com/matrixlegend-code/vpn-cdn-installer
- https://github.com/MHSanaei/3x-ui/releases/tag/v3.7.0
- https://yandex.cloud/en/docs/cdn/operations/resources/configure-caching
- https://yandex.cloud/en/docs/cdn/api-ref/ResourceRules/create
- https://yandex.cloud/en/docs/cdn/release-notes
