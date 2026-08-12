# Promotion 规则

1. 按 Organize Workflow 读取用户指定的 pending Inbox，并用 `query/show` 检查现有 Knowledge。
2. 从 `content-pack.json` 选择 category、type、模板与字段，在 `knowledge/` 之外准备自包含草稿和多目标 manifest。
3. 运行 `promote plan --manifest <file>` 冻结所有目标，再运行 `promote diff <promotion-id>`。
4. 向用户完整展示目标、consume 的 Inbox、diff 与 plan hash，然后停止。
5. 只有用户针对该 promotion ID、完整 diff 和 plan hash 明确批准后，才运行 `promote apply <id> --approve <plan-hash>`。
6. 不批准时可 reject 或保持 planned。禁止重读变化后的草稿替代冻结文件。

Promotion 可以 N:M 拆分或合并。计划、来源、目标基线或冻结文件漂移时必须进入 stale，不能覆盖 Knowledge。
