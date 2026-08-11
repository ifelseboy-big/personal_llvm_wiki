# 知识库管理规则

本 Vault 的核心工作流是 Inbox -> Promotion -> Knowledge。Obsidian 仅为可选展示工具；没有 Obsidian、`.obsidian/`、Bases 或插件时，全部流程仍必须可用。

## 事实边界

- `knowledge/` 中由 `promote apply` 写入的 Markdown 是唯一可信事实源。
- `inbox/` 是临时整理区，不得用于回答知识问题，也不得进入 SQLite。
- `.llm-wiki/index.sqlite` 只选择 Knowledge 候选；回答前必须用 `show` 回读 Markdown。
- `templates/`、`rules/`、`views/` 和工作草稿不是用户事实。

禁止直接创建、修改、移动 `knowledge/` 文件。Knowledge 的唯一写入路径是 `promote apply`。

## 路由

| 动作 | 必须读取 |
| --- | --- |
| 采集输入 | `rules/inbox.md`、`rules/metadata.md` |
| 整理 Inbox | `rules/inbox.md`、`rules/types.md`、`rules/metadata.md`、`rules/lifecycle.md`、`rules/quality.md`、`rules/promote.md` |
| 查询 Knowledge | `rules/index.md`；必要时读取 `rules/lifecycle.md` |
| 审阅、批准或拒绝 | `rules/promote.md` |

## 整理与批准流程

1. 只有用户明确要求整理时，使用 `inbox list/show` 读取指定或 pending 条目。
2. 使用 `query/show` 查重，决定创建、更新、拆分、合并或保留冲突。
3. 在非 `knowledge/` 路径准备自包含草稿和 promotion manifest。
4. 运行 `promote plan` 与 `promote diff`。
5. 展示完整 diff、目标文件、consume 的 Inbox、promotion ID 和 plan hash，然后停止。
6. 只有用户针对该冻结计划明确批准，才运行 `promote apply <id> --approve <plan-hash>`。
7. 报告 Knowledge ID、路径、Inbox 状态和索引 warning。

采集授权、整理授权和 Apply 批准互不等价。Inbox 清理必须另行明确执行，不能由 Apply 静默完成。

## 关系与来源

- Knowledge 必须自包含，不能要求回看 Inbox 才能理解。
- `lineage` 是发布时历史记录；Inbox 清理后缺失不构成 Knowledge 损坏。
- `related`、`supersedes`、`superseded_by` 只保存稳定 `know_` ID。wikilink 仅可作为展示增强。

面向人的入口见 `LLM-WIKI.md`。
