# Покрытие e2e

Сьют `backend/e2e` (`//go:build e2e`, `make test-e2e`) доказывает только то, что требует живых процессов и внешних сторон. Postgres, `lateness receiver` (фейк webhook, Telegram Bot API и проверяемого сайта) и тестовый SMTP-ящик живут в Compose. `lateness serve` и `lateness sweep` — отдельные процессы на хосте. Аккаунт создаётся CLI `lateness invite` и формой приглашения; ожидания и каналы — формами страниц.

Integration-тесты уже гоняют сценарии `app` на реальном Postgres с подставленными часами. E2e это не повторяет.

| Тест | Что доказывает |
|---|---|
| `TestSignalKeepsExpectationOnTime` | Приглашение из CLI, регистрация, создание ожидания через страницы, сигнал через живой `serve` оставляет его `on_time`. |
| `TestSilenceReachesWebhook` | Тишина до дедлайна: живой `sweep` шлёт webhook на приёмник. |
| `TestTelegramOutlet` | Уведомление уходит в Bot API (`/bot<token>/sendMessage`) и приёмник отвечает как Telegram. |
| `TestProbeFailureNotifiesAndSuccessKeepsOnTime` | Живой `sweep` делает probe: `200` держит `on_time`, `503` до дедлайна даёт `late` и webhook, ответ виден в карточке. |
| `TestSMTPThroughTestServer` | Письмо доходит до настоящего SMTP-сервера с implicit TLS и самоподписанным сертификатом. |

## Это не перепроверяем на e2e

- Расчёт плановых запусков и дедлайнов интервала и cron.
- Матрица переходов `quiet` / `on_time` / `late` / `held`, `begin`, повторный `fail`, ретрай probe.
- Разбор тела приёма, правила probe и outlet.
- Удержание, возврат, конфликт версий, soft-delete, изоляция проектов.
- Приглашения (повтор, истечение, отзыв, гонка), вход через провайдера, привязка и отвязка, смена пароля, шифрование настроек.
- Формат письма, ответы webhook и Telegram не-2xx, защита probe от частных адресов.

Это закрывают `internal/domain`, `internal/schedule`, `internal/app`, `internal/adapters/*`, `internal/web` и `internal/integration`.

Отдельного SPA нет, браузерные клики не добавляют того, чего не видит чёрный ящик. Playwright нет. Вход через настоящие VK ID и Telegram в e2e не проверяется: это внешние сервисы. Обмен кодом и проверка `id_token` (подпись, issuer, audience, срок) покрыты unit-тестами на фейках.
