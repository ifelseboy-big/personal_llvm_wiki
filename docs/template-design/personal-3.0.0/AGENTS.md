# 知识库 Agent 执行契约

本 Vault 使用 Inbox -> Promotion -> Knowledge。`content-pack.json` 是 category、type、字段约束、模板映射、生命周期检索语义和 Workflow 路由的唯一机器权威来源；开始任何语义工作前必须读取它。

## 事实边界

- `knowledge/` 中由 `promote apply` 写入的 Markdown 是唯一可信事实源。
- `inbox/` 是临时输入区，只能在 Capture/Organize/Publish 中按授权读取，禁止用于 Query 回答。
- SQLite 只选择候选；回答前必须通过 CLI 回读并验证 Knowledge Markdown。
- `templates/`、`rules/`、`workflows/`、`views/` 和工作草稿都不是用户事实。

禁止直接创建、修改或移动 `knowledge/` 文件。所有新建、更新、失效、合并和拆分必须通过 Promotion。采集授权、整理授权、维护授权和冻结计划批准互不等价。

## Workflow 路由

1. 从 `content-pack.json.workflows` 解析动作对应的文件，不凭文件名猜测。
2. 采集或保存输入执行 Capture。
3. 对 pending Inbox 查重、分类、拆分或合并执行 Organize。
4. 冻结、展示 diff、等待批准和 apply 执行 Publish。
5. 处理过期、冲突、重复、替代或更正执行 Maintain。
6. 回答已沉淀知识执行 Query。

## 通用语义要求

- category 表示知识领域，type 表示文档结构与主要用途；二者正交，只能选择 `content-pack.json` 当前声明值。
- 类型专属字段按对应 type 的声明验证，未知用户属性必须保留，不得自行创造旧字段兼容。
- Knowledge 必须自包含；lineage 是发布时历史记录，不要求 Inbox 永久存在。
- 关系字段只保存稳定 `know_` ID。路径、标题和 wikilink 不参与关系权威。
- 不猜测缺失事实。观察、原话、推断、建议和未决问题必须区分。

面向人的入口见 `LLM-WIKI.md`；确定性安全边界见 `rules/`。
