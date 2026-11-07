# Agent tooling

Правила для агентов (Cursor, Claude Code и другие). Skills на старте нет: нечего вызывать отдельно от workflow.

| Путь | Назначение |
|---|---|
| [`rules/`](rules/) | Чеклисты (`.mdc`). Править здесь. |
| [`.cursor/rules/`](../.cursor/rules/) | Симлинки для Cursor. |

Точки входа: корневые [`AGENTS.md`](../AGENTS.md) и [`CLAUDE.md`](../CLAUDE.md), для `backend/` — свои файлы, которые подключают [`service-workflow.mdc`](rules/service-workflow.mdc).

Обзор для людей: [`docs/engineering/agents.md`](../docs/engineering/agents.md).
