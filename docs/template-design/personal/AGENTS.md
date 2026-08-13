# 知识库 Agent 执行契约

本 Vault 使用 Inbox -> Promotion -> Knowledge。`content-pack.json` 是 category、type、字段约束、模板映射、生命周期检索语义和 Workflow 路由的唯一机器权威来源。

## 每次任务的启动顺序

1. 用户已指定 Vault 时使用该路径；否则运行 `llm-wiki locate --json --no-interactive`，把返回的 `wiki.path` 固定为 `<vault-root>`。
2. 读取 `<vault-root>/content-pack.json`，从 `workflows` 查找动作对应文件；不得根据文件名或旧版本记忆猜测。
3. 后续每条 CLI 命令都显式传入 `--wiki <vault-root> --json --no-interactive`，不得依赖当前目录或默认 Vault。
4. 内容包、实例或 frontmatter 版本不匹配时停止；不得兼容读取、猜字段或隐式迁移。

## 授权边界

| 动作 | 允许读取 | 允许写入 | 必须停止的位置 |
| --- | --- | --- | --- |
| Capture | 用户明确提供的输入 | 受管目录外的临时 note、`inbox/` | 返回 pending Inbox；不得整理或发布 |
| Organize | 用户授权的 pending Inbox、经 CLI 验证的 Knowledge | `knowledge/` 外的工作草稿和 manifest | 输出整理结果；不得 apply |
| Publish | 草稿、manifest、冻结 Promotion | Promotion 与获批后的事务写入 | 展示完整 diff 后先停止；获批后才 apply |
| Maintain 检查 | 经 CLI 验证的 Knowledge 与 doctor 诊断 | 无 | 返回检查报告 |
| Maintain 处理 | 上述证据及为本次维护采集的 pending Inbox | `knowledge/` 外的草稿和 manifest | 转 Publish，仍需冻结计划批准 |
| Query | `query/show` 回读验证后的 Knowledge | 无 | 返回证据化回答 |

采集授权、整理授权、维护处理授权和冻结计划批准互不等价。用户只要求检查、解释或预览时不得生成草稿、创建 Promotion 或执行写操作。

## 事实与写入边界

- `knowledge/` 中由 `promote apply` 写入的 Markdown 是唯一可信事实源。
- `inbox/` 是临时输入区，只能在 Capture、获授权的 Organize 和 Publish 中读取；禁止用于 Query 回答。
- SQLite 只选择候选；回答前必须通过 CLI 回读并验证 Knowledge Markdown。
- `templates/`、`rules/`、`workflows/`、`views/`、工作草稿和 Promotion 元数据都不是用户事实。

禁止直接创建、修改、移动或删除 `knowledge/` 文件。所有新建、更新、失效、替代、合并和拆分必须通过 Promotion。Knowledge 必须自包含；lineage 是发布时历史，不要求 Inbox 永久存在。

## 通用语义要求

- category 表示知识领域，type 表示文档结构与主要用途；二者正交，只能选择 `content-pack.json` 当前声明值。
- 类型字段按对应 type 声明验证；保留未知用户属性，不补旧版本字段。
- 关系字段只保存真实稳定 `know_` ID。新建草稿使用 `template create` 返回的 `proposed_knowledge_id`，并由 Promotion 再次校验。
- 不猜测缺失事实。原话、观察、推断、建议和未决问题必须区分。
- CLI 返回失败、warning、漂移、锁冲突或恢复要求时按 Workflow 的停止条件处理，不得用直接文件操作绕过。

面向人的入口见 `LLM-WIKI.md`；确定性规则见 `rules/`。
