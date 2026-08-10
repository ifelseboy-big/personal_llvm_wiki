---
name: llm-wiki-query
description: 从 llm-wiki 个人知识库检索已经人工确认发布的可信知识，并在命中后回读原始 knowledge Markdown。用户询问“我的知识库里有什么”、要求查找或引用个人知识时使用；不检索 raw，也不执行采集、发布或维护写操作。
---

# LLM Wiki Query

1. 运行 `llm-wiki locate --json --no-interactive`；用户指定知识库时加 `--wiki <alias|path>`。
2. 从 `data.root` 读取目标 Vault 的 `AGENTS.md`，遵守其中的知识库治理边界。
3. 运行 `llm-wiki query <问题> --wiki <data.root> --json --no-interactive`。`query` 只检索已发布的 `knowledge/`，不得尝试搜索 `raw/`。
4. 对命中的每个目标 `knowledge_id` 运行 `llm-wiki show <knowledge-id> --wiki <data.root> --json --no-interactive`，以回读的原始 knowledge Markdown 和元数据回答；SQLite 候选或缓存片段不能单独作为事实。
5. 需要核对来源关系时运行 `llm-wiki trace <knowledge-id> --wiki <data.root> --json --no-interactive`。

返回知识 ID 与路径。若 `query` 返回 `INDEX_NOT_FOUND` 或 `INDEX_STALE`，停止查询并说明需要使用 `$llm-wiki-maintain` 修复索引；本 Skill 不执行写操作。
