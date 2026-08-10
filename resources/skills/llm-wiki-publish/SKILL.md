---
name: llm-wiki-publish
description: 使用 llm-wiki 审查指定 raw 材料、与现有已发布知识比对、生成发布提案和差异，并只在用户明确批准该差异后发布唯一可信的 knowledge。用户要求整理、归档、更新或发布个人知识时使用；不采集新 raw。
---

# LLM Wiki Publish

1. 运行 `llm-wiki locate --json --no-interactive`；用户指定知识库时加 `--wiki <alias|path>`。
2. 从 `data.root` 读取目标 Vault 的 `AGENTS.md`，再按其中的发布路由读取类型、元数据、引用、质量、发布规则和所选模板。
3. 只通过明确 raw ID 运行 `llm-wiki raw show <raw-id> --wiki <data.root> --json --no-interactive`；可用 `raw list --unreferenced` 盘点待归档项，但不得全文检索 raw。
4. 用 `llm-wiki query <问题> ...` 查重，并对相关 `knowledge_id` 执行 `show` 回读原始 knowledge。
5. 在非受管临时路径生成草稿，运行 `publish propose --source <raw-id> --file <draft> ...`，再运行 `publish diff <change-id> ...`。
6. 向用户展示具体 diff 后停止。只有用户针对该 diff 明确批准，才能运行 `publish apply <change-id> ...`；否则使用 `publish reject` 或保持提案待审。

成功的 `publish apply` 才使 knowledge 成为唯一可信事实，并自动刷新 knowledge SQLite 索引。若响应 warning 表示刷新失败，知识发布仍成立，但应交给 `$llm-wiki-maintain` 修复索引。不得直接写 `knowledge/`。
