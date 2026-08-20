# AiR Widget

![air_widget](air_widget_logo.png)

[🇬🇧 English version](README.md)

![Версия Go](https://img.shields.io/badge/Go-1.25.8-00ADD8?logo=go)
![Лицензия](https://img.shields.io/badge/license-MIT-blue)
[![Telegram](https://img.shields.io/badge/Telegram-Join%20Chat-blue?logo=telegram)](https://t.me/marusia_dev)

`air_widget` — микросервис AiR-платформы, позволяющий интегрировать веб-виджет на любой сайт для взаимодействия пользователей с AI-ассистентом.

## Защита пользовательских данных

Все пользовательские данные шифруются индивидуальным `MasterKey`. Данный ключ доступен только по паролю пользователя и шифрует историю диалогов, индивидуальные настройки бота и прочие данные пользователя. Расшифровка данных возможна только после авторизации пользователя в системе индивидуальным паролем пользователя. Даже в случае компрометации или утечки базы данных, все пользовательские данные останутся недоступны как для злоумышленников, так и для администрации сервиса.

## Возможности

- запуск, остановка и перезапуск пользовательских экземпляров виджета;
- авторизация и обновление токенов доступа;
- ограничение подключения по списку разрешённых доменов;
- поддержка временных и бессрочных конфигураций виджета;
- обработка сообщений и получение истории диалога;
- передача ответов ассистента в режиме реального времени через Server-Sent Events (SSE);
- интеграция с CRM и операторским режимом;
- шифрованное хранение настроек в MySQL и Redis;
- получение пользовательского `MasterKey` через gRPC;
- Prometheus-метрики и корректное завершение активных соединений.

## Архитектура

```text
Браузерный виджет
        |
        v
   air_widget ---- MySQL
        |  \------ Redis
        |  \------ AiR Orchestrator (gRPC)
        \--------- AI-модель / CRM / Operator
```

Аутентификация выполняется с помощью JWT-токенов. Для передачи событий клиенту используется SSE.

## Авторизация

Выпуск, обновление и проверка JWT-токенов выполняются основным сервисом `air_orchestrator`. `air_widget` обращается к нему по RPC и использует полученные токены для авторизации запросов к виджету и установления SSE-соединений.

Код виджета также создаётся и проверяется через RPC-вызовы `air_orchestrator`. Сам `air_widget` отвечает за обработку HTTP-запросов, проверку разрешённого origin и работу активных пользовательских виджетов, но не является сервисом подписи JWT-токенов.

## Технологии

Go, Gin, MySQL, Redis, gRPC, JWT, SSE, Prometheus, Docker и Docker Compose.

## Запуск в development

Для запуска используется [`dev.yml`](dev.yml):

```bash
docker compose -f dev.yml up --build
```

Development-конфигурация подключает сервис к MySQL, Redis и gRPC-сервису AiR Orchestrator, использует `localhost` как публичный адрес и включает debug-логирование.

## Конфигурация

Основные переменные окружения:

```text
DB_HOST
DB_NAME
DB_USER
DB_PASSWORD
REDIS_ADDR
REDIS_PASSWORD
REDIS_DB
GRPC_CONFIG_HOST
REAL_URL
LOG_LEVEL
```

Production-конфигурация находится в [`prod.yml`](prod.yml). Публичный адрес передаётся через переменную `DOMAIN`.

## HTTP API и мониторинг

Сервис предоставляет следующие группы маршрутов:

```text
GET  /metrics

GET  /v1/widget/available
POST /v1/widget/exam
GET  /v1/widget/validate
POST /v1/widget/refresh
GET  /v1/widget/username
GET  /v1/widget/dialog
POST /v1/widget/data
GET  /v1/widget/events
POST /v1/widget/events-ticket
POST /v1/widget/code

GET  /widget/enable
GET  /widget/disable
GET  /widget/restart
```

Маршрут `/v1/widget/events` используется для SSE-соединения и потоковой передачи событий ассистента. Маршрут `/metrics` предоставляет метрики Prometheus, включая HTTP-запросы, SSE-соединения, обработку сообщений, активные диалоги и жизненный цикл виджетов.

Полный описательный контракт находится в [`doc/openapi.yaml`](doc/openapi.yaml).

## Docker

[`Dockerfile`](Dockerfile) собирает статический Go-бинарник в отдельном build-этапе, сжимает его с помощью UPX и запускает в минимальном runtime-образе `scratch` с сертификатами CA и данными часовых поясов.

## Связанные сервисы
- [air_common](https://github.com/ikermy/air_common) — Общая библиотека для AI‑микросервисов
- [air_orchestrator](https://github.com/ikermy/air_orchestrator) — Главный сервис оркестратор
- [air_operator](https://github.com/ikermy/air_operator) — Сервис переадресации ответов на операторов от пользователей, поддерживает все типы ботов
- [marusia_crm](https://github.com/ikermy/marusia_crm) — Сервис интеграции с внешними CRM системами
- [air_logger](https://github.com/ikermy/air_logger) — Вспомогательный сервис логирования событий с поддержкой многопользовательского режима и поддержкой сборщика логов loki

## Лицензия

Проект распространяется по лицензии [MIT](LICENSE). Она разрешает свободно использовать, копировать, изменять и распространять программное обеспечение при сохранении текста лицензии и уведомления об авторских правах.

Полный текст лицензии доступен в файле [`LICENSE`](LICENSE).

## Контакты
[![Telegram](https://img.shields.io/badge/Telegram-Contact-blue?logo=telegram)](https://t.me/ikermy)
