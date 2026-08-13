# Maintain Workflow

触发：用户要求检查或处理过期、冲突、重复、错误、需要替代或需要更新的 Knowledge。

## 检查阶段

1. 按 `AGENTS.md` 固定 `<vault-root>`，读取 `content-pack.json` 的生命周期和关系声明。
2. 运行 `doctor` 获取全库治理诊断；用针对性 `query --include-inactive` 查找候选，并对采用的每个 ID 执行 `show`。
3. 区分无效、争议、未生效、已过期、复核到期、重复和内容冲突；保留证据与分歧，不静默选择一方。
4. 用户只要求检查、审计或建议时，输出 ID、路径、诊断、影响和建议后停止，零写入。

## 处理阶段

5. 只有用户明确要求处理时才继续。把本次维护请求、用户提供的新证据和必要诊断通过 Capture 保存为 pending Inbox，作为变更 lineage；不得凭口头授权构造无来源 Promotion。
6. 转 Organize 准备更新或新建草稿。更新 baseline 使用 `show` 返回的 `content_hash/file_hash`；新建目标使用 `template create` 返回的 `proposed_knowledge_id`。
7. 合并、替代或更正涉及 reciprocal 关系时，把所有已存在和新建的相关目标放在同一 Promotion，双向写入策略声明的稳定 ID。删除历史事实不能代替 lifecycle 或替代关系。
8. 转 Publish 冻结计划、展示完整 diff、停止等待批准；获批后才 apply。

所有命令显式带 `--wiki <vault-root> --json --no-interactive`。Maintain 不直接重命名、移动、修改或删除 `knowledge/`，也不在检查阶段创建 Inbox、草稿或 Promotion。
