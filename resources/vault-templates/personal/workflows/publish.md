# Publish Workflow

触发：用户要求发布已整理草稿，或 Organize 已准备 manifest。

1. 复核 `content-pack.json` 当前版本、草稿完整性、关系、来源和 manifest baseline。
2. 运行 `llm-wiki promote plan --manifest <file> --json --no-interactive`。
3. 运行 `llm-wiki promote diff <promotion-id>`，取得冻结计划的完整 diff。
4. 向用户完整展示 promotion ID、plan hash、全部目标、consume 的 Inbox 和完整 diff，然后停止。
5. 只有用户明确批准该 promotion ID、plan hash 与已展示完整 diff，才运行 `llm-wiki promote apply <id> --approve <plan-hash> --json --no-interactive`。
6. 报告 Knowledge ID/路径、Inbox 状态、事务状态和索引 warning。

缺少明确批准、计划漂移、锁冲突或 stale 时禁止 apply。不得用变化后的草稿替代冻结文件，也不得直接写 `knowledge/`。
