# Organize Workflow

触发：用户要求整理明确 Inbox 或 pending 集合。

1. 读取 `AGENTS.md`、`content-pack.json`、`rules/content.md`、`rules/quality.md` 与 `rules/promote.md`。
2. 用 `inbox list/show` 读取授权范围，核对 payload 与初步 note；不得遗漏原始输入中的条件、例外或不确定性。
3. 用 `query` 查找主题相同、可合并、冲突或可能被替代的 Knowledge；对采用的候选用 `show` 回读。
4. 决定保留、创建、更新、拆分或合并。为每个目标独立选择 category 和 type，并读取策略映射的模板。
5. 在 `knowledge/` 外生成自包含草稿。保留未知用户属性；关系只写真实稳定 ID；事实不足时保留问题，不猜测。
6. 生成 N:M promotion manifest，绑定每个 Inbox 的 payload/item hash 和更新目标的 content/file baseline hash。

输出：草稿、manifest、查重依据和每个语义决策的简短理由。Organize 不运行 apply；需要发布时转 Publish。
