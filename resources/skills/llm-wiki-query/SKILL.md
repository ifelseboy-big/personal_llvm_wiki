---
name: llm-wiki-query
description: 用户询问个人知识库中已经沉淀的可信知识时使用。
version: 3.0.0
---

1. 运行 `llm-wiki locate --json --no-interactive` 定位 Vault，并读取 Vault `AGENTS.md` 的事实边界。
2. 运行 `llm-wiki query <question> --json --no-interactive`，只检索已验证 Knowledge。
3. 对采用的 Knowledge ID 运行 `llm-wiki show <id> --json --no-interactive` 回读完整 Markdown。
4. 只根据回读后的 Knowledge 回答，并返回 Knowledge ID 与路径。
5. 禁止调用 `inbox list/show`、读取 `inbox/`、写文件或维护索引。
6. 收到 `INDEX_NOT_FOUND` 或 `INDEX_STALE` 时停止，提示用户在 Vault 内运行索引维护；不得自行写入或用 Inbox 兜底。

SQLite 只是候选缓存，不是事实源。
