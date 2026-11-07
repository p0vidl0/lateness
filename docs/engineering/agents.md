# Агенты

Обзор правил. Канон — [`.agents/rules/`](../../.agents/rules/). Этот файл не повторяет чеклисты.

Cursor читает [`.cursor/rules/`](../../.cursor/rules/) — это симлинки на канон. Корневые [`AGENTS.md`](../../AGENTS.md) и [`CLAUDE.md`](../../CLAUDE.md) подключают всегда включённые правила. `backend/AGENTS.md` и `backend/CLAUDE.md` подключают правило сервиса.

| Правило | Когда |
|---|---|
| [verify-before-done](../../.agents/rules/verify-before-done.mdc) | Перед «готово»: тест нижнего слоя и `make validate` (или integration, если задета база); полный прогон — `make test-manual`. Коммит только по просьбе. |
| [shell-long-commands](../../.agents/rules/shell-long-commands.mdc) | Долгие `make test-integration` и `make test-e2e`: лог и код выхода, без `tail` в том же пайпе. |
| [maintain-rules](../../.agents/rules/maintain-rules.mdc) | Устойчивый приём сразу попадает в `.agents/rules`. |
| [service-workflow](../../.agents/rules/service-workflow.mdc) | Файлы `backend/**`: слои, куда класть страницу, сценарий, SQL, outlet, probe, провайдера входа (OIDC). |

Отдельных skills в репозитории нет.
