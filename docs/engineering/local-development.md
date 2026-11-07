# Локальная разработка

Команды запускаются из корня репозитория. Три проекта Compose не делят volume: `lateness-dev`, `lateness-integration`, `lateness-e2e`.

`make render-env` собирает `.docker/env/<project>.env` из шаблона и профиля (`.docker/env/profiles/`). Рядом можно положить gitignored `.local.env`, например с ключами VK ID и Telegram Login: make подхватывает его в окружение shell. Контейнеры читают сгенерированный файл, поэтому URL базы внутри Docker остаётся именем сервиса `postgres`. Версии образов Compose — в `.docker/pinned-images.env` (тег и digest `@sha256:…`).

| Цель | Что делает |
|---|---|
| `make infra-up` | Postgres и тестовый SMTP-ящик проекта `lateness-dev`. |
| `make serve-dev` | Поднимает инфру и запускает `serve` на хосте. |
| `make sweep-dev` | `sweep` на хосте против той же инфры. |
| `make dev-up` | Инфра и оба процесса в `Dockerfile.dev` (`go run`, исходники смонтированы). Печатает URL и учётку сида. |
| `make dev-down` | Останавливает стек. |
| `make dev-restart` | Пересоздаёт только `serve` и `sweep`. |
| `make invite [ADMIN=1] [NOTE=…]` | Печатает ссылку-приглашение для dev-стека. |
| `make check-config` | Рендер всех трёх профилей env, `docker compose config` для каждой комбинации compose-файлов (включая `.deploy/compose.yaml`), `bash -n` для скриптов. |
| `make deploy-image` | Собирает production-образ из `.deploy/Dockerfile` как `lateness:local` и запускает в нём `lateness version`. |
| `make check-go` | `gofmt`, `go mod tidy -diff`, `go mod verify`, сборка и `go vet` с тегами `integration` и `e2e`. |
| `make vulncheck` | `govulncheck`: известные уязвимости (vuln.go.dev) в зависимостях и стандартной библиотеке, до которых реально доходит код. Нужна сеть. Версия сканера закреплена директивой `tool` в `go.mod`. |
| `make backend-lint` | golangci-lint, включая код за тегами `integration` и `e2e`. |
| `make test-unit` / `make test` | `go test ./... -short -race`. Docker не нужен. |
| `make validate` | `check-go`, lint и unit. |
| `make test-integration` | Свой Postgres, сценарии `app` против него, затем `down -v`. `KEEP_INTEGRATION_STACK=1` оставляет стек. |
| `make test-e2e` | Postgres, приёмник и тестовый SMTP в Compose; `serve` и `sweep` — процессы на хосте. `KEEP_E2E_STACK=1` оставляет стек. |
| `make test-manual` | Всё по порядку, от дешёвого к дорогому, до первого сбоя: `check-config`, `validate`, `vulncheck`, `deploy-image`, `test-integration`, `test-e2e`. В конце — `ALL CHECKS PASSED`. Нужен запущенный Docker. CI выполняет те же цели (см. [`.deploy/README.md`](../../.deploy/README.md)). |

Порты lateness лежат в диапазоне `2xxxx`, чтобы не пересекаться с соседними проектами (oval занимает `15433`, `15434`, `11025`, `11026`). Стенды integration и e2e снимаются при выходе, даже если `compose up` упал.

Порты dev: приложение `28080`, Postgres `25432`, SMTP-ящик: UI `28025`, API `28026`, SMTPS `21025`.

Сид dev: `operator@localhost` / `lateness-dev`, администратор, проект «Мой проект», outlet «Dev SMTP» в тестовый ящик. Хостовый `serve-dev` подменяет SMTP-хост на `127.0.0.1` и опубликованный порт.

## Уязвимости

`make vulncheck` падает, если код вызывает уязвимую функцию. Лечится обновлением: зависимость — `go get <module>@<fixed>` и `go mod tidy`; стандартная библиотека — `go get go@<patch>` (строка `go` в `go.mod`), затем тот же патч Go в `LATENESS_IMAGE_GOLANG` (`.docker/pinned-images.env`) в `ARG` `.docker/Dockerfile.dev` и в стадии сборки `.deploy/Dockerfile`. Находки «в модулях, которые вы требуете, но не вызываете» сборку не валят; их стоит снимать при следующем обновлении зависимостей.

## Окружение

| Переменная | Зачем |
|---|---|
| `LATENESS_DATABASE_URL` | Postgres. Обязательна. |
| `LATENESS_SECRET_KEY` | Base64 от 32 байт, шифрует настройки outlets. Обязательна. В prod: `openssl rand -base64 32`, хранить вне репозитория. Смена ключа делает сохранённые outlets нечитаемыми. |
| `LATENESS_PUBLIC_URL` | Внешний адрес: ссылки приёма, приглашений, redirect URI провайдеров. `https://` включает флаг `Secure` у cookie. |
| `LATENESS_SWEEP_INTERVAL` | Пауза между проходами sweep. |
| `LATENESS_SESSION_TTL` | Срок сессии. |
| `LATENESS_VK_CLIENT_ID`, `LATENESS_VK_CLIENT_SECRET` | Вход через VK ID; без них кнопки нет. |
| `LATENESS_TELEGRAM_LOGIN_CLIENT_ID`, `LATENESS_TELEGRAM_LOGIN_CLIENT_SECRET` | Вход через Telegram (OIDC); без них кнопки нет. |
| `LATENESS_TELEGRAM_API_URL` | Адрес Bot API; пусто — `https://api.telegram.org`. В e2e — приёмник. |
| `LATENESS_SMTP_INSECURE_TLS` | Не проверять сертификат SMTP. Только для тестового ящика с самоподписанным сертификатом. |
| `LATENESS_PROBE_ALLOW_PRIVATE` | Разрешить probes на локальные и частные адреса. Только dev и e2e. |
| `LATENESS_SEED_*` | Сид-аккаунт и его SMTP-outlet. В prod не задаются. |

## Схема

Миграции выполняет только `serve`. Пока сервис не выпущен, схема — один файл `001_init.sql`, и он правится на месте. После такой правки пересоздайте базу: `docker compose -p lateness-dev down -v` (или удалите volume), иначе `serve` остановится с `schema version mismatch` либо на старой схеме.
