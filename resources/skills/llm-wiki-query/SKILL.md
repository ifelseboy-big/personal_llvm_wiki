---
name: llm-wiki-query
description: 用户询问个人知识库中已经沉淀的可信知识时使用。
version: 4.0.1
---

1. 运行 `llm-wiki locate --json --no-interactive` 定位 Vault，把返回的 `wiki.path` 固定为 `<vault-root>`，并读取 Vault `AGENTS.md` 与 `content-pack.json`。
2. 从 `content-pack.json.workflows` 路由并完整执行 Query Workflow。
3. 运行 `llm-wiki query <question> --wiki <vault-root> --json --no-interactive`，只检索已验证 Knowledge。
4. 对采用的 Knowledge ID 运行 `llm-wiki show <id> --wiki <vault-root> --json --no-interactive` 回读完整 Markdown。
5. 只根据回读后的 Knowledge 回答，并返回 Knowledge ID 与路径，保留争议、有效期和复核 warning。
6. 禁止调用 `inbox list/show`、读取 `inbox/`、写文件或维护索引。
7. 收到 `INDEX_NOT_FOUND`、`INDEX_STALE`、`KNOWLEDGE_INVALID`、`KNOWLEDGE_READ_FAILED` 或 `RECOVERY_REQUIRED` 时停止；不得自行写入或用 Inbox 兜底。

SQLite 只是候选缓存，不是事实源。
