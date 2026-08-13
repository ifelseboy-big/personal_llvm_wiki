# Promotion 规则

Promotion manifest 使用以下当前协议；不得添加旧字段、猜测 baseline 或省略 lineage 来源：

```json
{
  "schema_version": 1,
  "inboxes": [
    {
      "id": "inbox_...",
      "payload_hash": "sha256:...",
      "item_hash": "sha256:...",
      "consume": true
    }
  ],
  "targets": [
    {
      "operation": "create",
      "draft_file": "drafts/new.md",
      "knowledge_id": "know_...",
      "inbox_ids": ["inbox_..."]
    },
    {
      "operation": "update",
      "draft_file": "drafts/existing.md",
      "knowledge_id": "know_...",
      "base_content_hash": "sha256:...",
      "base_file_hash": "sha256:...",
      "inbox_ids": ["inbox_..."]
    }
  ]
}
```

- Inbox 的 ID 与哈希只取自同次 `inbox show`；每个 Inbox 必须至少映射一个 target。
- create 的 `knowledge_id` 使用 `template create` 返回的 `proposed_knowledge_id`，不得自行编造；update 的 ID 和 baseline 只取自 `show`。
- `draft_file` 是相对 manifest 所在目录的安全路径；所有草稿必须在 Vault 受管目录外。
- `consume=true` 只用于本次成功发布后应变为 processed 的 Inbox；是否 consume 必须向用户展示。
- 同一事实变更涉及多输入、多输出或 reciprocal 关系时放在一个 N:M Promotion 中，保证计划内一致。

运行 `promote plan --manifest <file>` 冻结所有目标，再运行 `promote diff <promotion-id>`。向用户展示内容包 identity、全部目标、consume 决策、完整 diff 和 plan hash 后停止。只有用户针对该 promotion ID、完整 diff 和 plan hash 明确批准后，才运行 `promote apply <id> --approve <plan-hash>`。

不批准时可 reject 或保持 planned。计划、来源、内容包、目标 baseline 或冻结文件漂移时必须进入 stale，不能覆盖 Knowledge，也不能沿用旧批准重新规划。
