# Publish Workflow

触发：用户要求审阅或发布已整理草稿，或 Organize 已准备 manifest。用户说“发布”只授权生成并展示冻结计划，不自动等同于批准 apply。

1. 按 `AGENTS.md` 固定 `<vault-root>`，复核当前内容包版本、草稿完整性、来源映射、关系和更新 baseline。
2. 运行：
   `llm-wiki promote plan --manifest <file> --wiki <vault-root> --json --no-interactive`
3. 使用 plan 返回的 ID 运行：
   `llm-wiki promote diff <promotion-id> --wiki <vault-root> --json --no-interactive`
4. 核对两次返回的 promotion ID 与 plan hash 完全一致。向用户完整展示内容包 identity、plan hash、全部 target、consume 决策、完整 diff 和 warning，然后停止。
5. 只有用户在看到上述完整内容后，明确批准该 promotion ID 和 plan hash，才运行：
   `llm-wiki promote apply <promotion-id> --approve <plan-hash> --wiki <vault-root> --json --no-interactive`
6. 返回 Knowledge ID/路径、consumed Inbox、`transaction_state`、索引结果和全部 warning。`transaction_state=files_committed` 表示事实文件已提交但仍需恢复或索引完成，不能宣称全流程完成。

缺少明确批准、批准对象不一致、计划漂移、锁冲突、stale 或恢复要求时禁止 apply。不得重建一个“相似计划”沿用旧批准，不得用变化后的草稿替代冻结文件，也不得直接写 `knowledge/`。
