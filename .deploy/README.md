# Деплой на VPS (Traefik + GHCR)

Образ собирается в GitHub Actions и пушится в **GHCR**. На сервер по **SSH** копируются `compose.yaml`, **`config.env`** (из репозитория) и **`secrets.env`** (собирается в CI из GitHub Secrets), затем `docker compose up -d --wait`.

## Каталог `.deploy`

| Файл | Назначение |
|------|------------|
| `Dockerfile` | Production-образ: статический бинарник `lateness` в distroless (`nonroot`), без shell. Версия — git SHA (`lateness version`). |
| `compose.yaml` | `serve` за Traefik и `sweep` рядом, один образ. |
| `config.env` | Несекретный конфиг: домен, публичный URL, Traefik, интервалы. Редактируется в git. |
| `secrets.env.example` | Шаблон того, что CI пишет в `secrets.env`. |

Миграции применяет сам `serve` при старте; `sweep` ждёт `serve` (`depends_on: service_healthy`) и только проверяет версию схемы. Отдельного шага миграций нет.

Healthcheck `serve` — `/lateness health` (запрос к `/readyz`: база доступна, схема нужной версии). `up --wait` в деплое падает, если `serve` не стал healthy за 120 с.

## Файлы на сервере (`SSH_PATH`)

| Файл | Источник |
|------|----------|
| `compose.yaml`, `config.env` | репозиторий |
| `secrets.env` | генерируется в CI при каждом деплое, права `600` |
| `.env` | на деплое: `cat config.env secrets.env` — подстановки `${LATENESS_*}` для `docker compose` |

## GitHub Actions

Workflow: [`.github/workflows/deploy-vps.yml`](../.github/workflows/deploy-vps.yml).

| Job | Когда | Что делает |
|-----|-------|------------|
| `checks` | всегда | `make check-config`, `make validate` (gofmt, `go.mod`, сборка и vet с тегами, golangci-lint, unit с `-race`), `make vulncheck` |
| `integration` | всегда | `make test-integration` |
| `e2e` | всегда | `make test-e2e`; при падении — хвосты логов `serve` и `sweep` |
| `image` | после трёх проверок | сборка `.deploy/Dockerfile`; push в GHCR только на `master` |
| `deploy` | push в `master` или ручной запуск на `master` | `secrets.env` → SSH → `pull` и `up -d --wait` |

Триггеры: push в `master`, pull request, `workflow_dispatch`. Новый push в ту же ветку отменяет текущий прогон. Job `deploy` использует environment `production` — в настройках репозитория на него можно повесить ручное подтверждение.

### Secrets

**SSH:** `SSH_HOST`, `SSH_USER`, `SSH_KEY`, `SSH_PATH`, опционально `SSH_KNOWN_HOSTS` (без него — `ssh-keyscan` при каждом деплое).

**Приложение (обязательные):**
- `LATENESS_DATABASE_URL` — например `postgres://lateness:…@postgres:5432/lateness?sslmode=disable`.
- `LATENESS_SECRET_KEY` — `openssl rand -base64 32`. Ключ шифрует настройки каналов. Сменить его после сохранения каналов нельзя: они станут нечитаемыми (см. [ADR 003](../docs/adr/003-outlet-secrets-encryption.md)). Храните копию вне GitHub.

**Приложение (по желанию):**
- `LATENESS_VK_CLIENT_ID` / `LATENESS_VK_CLIENT_SECRET` — вход через VK ID, redirect `https://<домен>/auth/vk/callback`.
- `LATENESS_TELEGRAM_LOGIN_CLIENT_ID` / `LATENESS_TELEGRAM_LOGIN_CLIENT_SECRET` — вход через Telegram, redirect `https://<домен>/auth/telegram/callback`.
- `LATENESS_TELEGRAM_API_URL` — reverse proxy Bot API для Telegram-каналов уведомлений.

Пустой секрет в `secrets.env` не попадает — соответствующей кнопки входа просто не будет.

## Подготовка сервера (чеклист)

1. **Пользователь и каталог** — пользователь `lateness`, каталог `SSH_PATH`, ключ деплоя в `authorized_keys`, Docker с **Compose V2** (`docker compose`).
2. **GHCR** — если пакет приватный: `docker login ghcr.io` токеном с `read:packages`.
3. **Postgres** — БД и пользователь `lateness`, хост `postgres:5432` во внешней сети `lateness-db`: `docker network create lateness-db`. Порт наружу не публикуется.
4. **Traefik** — внешняя сеть `web` (`TRAEFIK_NETWORK` в `config.env`), entrypoint `websecure`, cert resolver `TRAEFIK_CERT_RESOLVER`. `serve` в сетях `web` и `lateness-db`; `sweep` — в `lateness-db` и собственной сети `egress` для исходящих SMTP, webhook, Telegram и probes.
5. **`config.env`** — домен и `LATENESS_PUBLIC_URL` (от него строятся ссылки приёма, приглашений и redirect URI).
6. Задать secrets в GitHub, запустить workflow.
7. **Первый администратор** — после первого деплоя:

   ```bash
   docker compose -f compose.yaml exec serve /lateness invite -admin -note "первый администратор"
   ```

   Подробнее — [runbooks/invites.md](../docs/runbooks/invites.md).

## Проверка локально

```bash
make check-config   # compose.yaml с config.env и secrets.env.example парсится
make deploy-image   # тот же Dockerfile, образ lateness:local
```

Обе цели входят в `make test-manual`.
