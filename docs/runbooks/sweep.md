# Sweep

`lateness sweep` в цикле:

1. Проверяет версию схемы. На старте до 90 секунд ждёт, пока `serve` создаст таблицы; несовпадение версии — выход.
2. **Probes.** Выбирает ожидания с `source = 'probe'`, не на паузе, у которых наступил `probe_at` (`FOR UPDATE SKIP LOCKED`), сдвигает `probe_at` на аренду (90 с) и выполняет запросы параллельно, до 8. Успех пишется как сигнал `ok` и открывает новое окно. Неудача — как сигнал `fail` в журнале, следующая попытка через `min(30 с, период)`, состояние не меняется.
3. **Окна.** Выбирает ожидания `quiet` или `on_time`, у которых наступил `deadline_at` или `run_deadline_at`. Для каждого пишет transition (`window_closed` или `begin_expired`) и по одной `pending` delivery на назначенный outlet (или одну без outlet, если набор пуст).
4. **Доставки.** Забирает `pending`, помечает `sending` и отправляет в outlet. Успех — `sent`. Ошибка — снова `pending`, пока попыток меньше пяти, затем `failed`. Нет outlet — сразу `failed` с текстом `outlet is not configured`. Строка `sending` старше двух минут возвращается в `pending`.

Пока ожидание остаётся `late`, второй transition и вторая delivery за тот же эпизод не создаются. Удалённые ожидания (`deleted_at`) sweep не видит; уже поставленные доставки уходят.

Интервал — `LATENESS_SWEEP_INTERVAL` (в dev 5s, в e2e 1s).

## Если уведомление не пришло

1. `SELECT status, source, deadline_at, run_deadline_at, probe_at, deleted_at FROM expectations WHERE name = '...'` — статус `quiet`/`on_time` и дедлайны в будущем значат, что окно не закрыто. `held` и удалённые sweep не трогает.
2. `SELECT outcome, note, received_at FROM signals WHERE expectation_id = '...' ORDER BY received_at DESC LIMIT 10` — для probe здесь видны ответы и ошибки (`timeout`, `→ 503`, `address is not allowed for probes`).
3. `SELECT status, attempt_count, last_error, sent_at, outlet_id FROM deliveries WHERE expectation_id = '...'` — пусто значит, что переход не записан. `pending`/`failed` — смотрите `last_error`.
4. Назначения: `SELECT outlet_id FROM expectation_outlets WHERE expectation_id = '...'`.
5. Лог процесса sweep. Для dev-стека: `docker compose -p lateness-dev logs sweep`.

Типичные `last_error`:

| Ошибка | Что делать |
|---|---|
| `x509: certificate …` | SMTP с самоподписанным сертификатом. В prod — нормальный сертификат; `LATENESS_SMTP_INSECURE_TLS` только для тестового ящика. |
| `EOF` или таймаут на SMTP | Неверное шифрование: порт 465 — «TLS сразу», 587 — STARTTLS. |
| `telegram: Bad Request: chat not found` | Бот не добавлен в чат или неверный chat id. |
| `telegram: Unauthorized` | Неверный токен бота. Токен в ошибках заменён на `***`. |
| `http status 4xx/5xx` | Webhook ответил не 2xx. |
| `outlet … settings: sealed value is malformed …` | Изменился `LATENESS_SECRET_KEY`. Верните ключ, с которым настройки сохранялись. |

Почта в dev смотрится в тестовом SMTP-ящике (`make dev-up` печатает адрес), не в локальном почтовом клиенте.

`sweep` не лечит схему. Если в логе mismatch, перезапустите `serve` той же версии бинарника.

## Ручная проверка dev

`make dev-up` печатает URL и учётку.

1. Войти сидом. В проекте уже есть канал «Dev SMTP». Создать ожидание «Сигнал от задачи» с периодом в час и отправить `curl` из карточки с `{"outcome":"ok"}`. Карточка показывает «Вовремя».
2. Создать второе ожидание с периодом в несколько секунд, допуском 0 и каналом «Dev SMTP» и не слать сигнал. Через один-два прохода sweep в тестовом ящике появляется письмо «became late».
3. Создать «Проверку URL» на `http://serve:8080/healthz` (dev разрешает внутренние адреса). Через проход карточка показывает «Вовремя» и строку проверки в ленте.
