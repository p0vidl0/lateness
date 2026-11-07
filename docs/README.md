# Документация Lateness

Текст кладётся по аудитории. Правила агентов здесь не дублируются: канон в [`.agents/rules/`](../.agents/rules/).

## Продукт

- [Обзор](product/overview.md) — что делает сервис, без устройства кода.
- [Функционал интерфейса](product/ui-functionality.md) — сущности и экраны.

## Инженеры

- [Обзор системы](architecture/system-overview.md) — процессы, слои, данные.
- [ADR 001](adr/001-one-binary-two-roles.md) — один бинарник, две роли, Postgres как единственное состояние.
- [ADR 002](adr/002-no-management-api.md) — без HTTP API управления.
- [ADR 003](adr/003-outlet-secrets-encryption.md) — шифрование настроек outlets.
- [ADR 004](adr/004-probe-as-expectation-source.md) — probe как источник сигнала ожидания.
- [Локальная разработка](engineering/local-development.md).
- [Деплой и CI](../.deploy/README.md) — образ, VPS, GitHub Actions, secrets.
- [Вход и приглашения](engineering/auth.md) — пароль, VK ID, задел под OIDC.
- [Агенты](engineering/agents.md) — обзор; тела правил остаются в `.mdc`.
- [Цикл sweep](runbooks/sweep.md) — что смотреть, если уведомление не ушло.
- [Приглашения](runbooks/invites.md) — как выдать первый доступ в prod.
- [Покрытие e2e](testing/e2e-coverage.md).

## Контракты

- [Приём сигнала](contracts/intake.md).
- [Проверки (probes)](contracts/probes.md).
- [Состояния](contracts/statuses.md).

## Куда класть новый текст

- `product/` — что сервис делает, для человека вне кода.
- `contracts/` — семантика, на которую опираются внешние стороны: задачи, проверяемые сайты, получатели уведомлений.
- `architecture/` и `adr/` — как устроен сервис и почему.
- `engineering/` — как его собирать и менять локально.
- `runbooks/` — как эксплуатировать и что проверить при сбое.
- `testing/` — что какой слой теста обязан доказать.
