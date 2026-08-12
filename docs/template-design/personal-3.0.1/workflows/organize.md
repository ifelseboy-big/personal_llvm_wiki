# Organize Workflow

触发：用户要求整理明确 Inbox ID 或明确授权的 pending 集合。

1. 按 `AGENTS.md` 固定 `<vault-root>`，读取 `content-pack.json`、`rules/content.md`、`rules/quality.md` 与 `rules/promote.md`。
2. 用 `inbox list --status pending` 确认授权范围；对每个目标执行 `inbox show <id>`。只使用返回的规范 `payload_path` 读取原始 payload，不从标题、ID 或目录遍历猜路径。
3. 同时核对 `item_hash`、`payload_hash`、初步 note 和原始 payload。无法读取或理解某种格式时停止并说明，不得仅根据摘要补齐原文事实。
4. 用 `query` 查找相同主题、可合并、冲突或可能被替代的 Knowledge；对采用的候选执行 `show`，以其 `content_hash` 和 `file_hash` 作为唯一更新 baseline。
5. 决定保留、创建、更新、拆分或合并。为每个目标独立选择 category 和 type，读取策略映射的模板及字段声明，并给出简短理由。
6. 新建草稿执行 `template create <type> --title <title> --output <draft> --set category=<category> --set description=<description> ...`。保留 JSON 返回的 `proposed_knowledge_id`，用作 create target 的 `knowledge_id` 和同一 Promotion 内其他草稿的稳定关系 ID。
7. 在 `knowledge/` 外补全草稿。删除所有 `llm-wiki:prompt` 注释和未解析变量；保留未知用户属性；关系只写真实稳定 ID；事实不足时保留未决问题。
8. 按 `rules/promote.md` 生成 N:M manifest：Inbox 哈希只取自 `inbox show`，更新 baseline 只取自 `show`，create ID 只取自 `template create`。

所有命令显式带 `--wiki <vault-root> --json --no-interactive`。输出草稿、manifest、来源映射、查重依据和语义决策；Organize 不运行 `promote plan/apply`，需要发布时转 Publish。
