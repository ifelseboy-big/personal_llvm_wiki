# Query Workflow

触发：用户询问已经沉淀的可信知识。

1. 读取 Vault `AGENTS.md`、`content-pack.json` 与 `rules/index.md`。
2. 运行 `llm-wiki query <question> --json --no-interactive`。需要审计 inactive 知识时只在用户意图明确后加 `--include-inactive`。
3. 对采用的每个 Knowledge ID 运行 `llm-wiki show <id> --json --no-interactive` 回读完整 Markdown。
4. 只依据回读后的 Knowledge 组织回答，显著表达 CLI 返回的争议、有效期、过期或复核 warning，并列出 Knowledge ID 与路径。

禁止读取 Inbox、草稿、模板或 SQLite 作为回答事实；禁止写文件或自动修复索引。遇到 `INDEX_NOT_FOUND`、`INDEX_STALE`、`KNOWLEDGE_INVALID` 或恢复要求时停止并报告。
