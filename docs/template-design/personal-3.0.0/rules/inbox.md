# Inbox 规则

- `inbox/` 是临时工作区，不是可信事实源，也不得进入查询索引。
- 每条输入必须完整保留在 `payload/`，初步整理只写 `item.md`，不得覆盖原始字节。
- 只有 `pending` 条目可进入新 Promotion；只有成功 Promotion 明确 consume 的条目可变为 `processed`。
- 读取 Inbox 只能使用明确的 `inbox list/show` 整理流程；回答知识问题不得读取 Inbox。
- 清理是独立动作，只能删除已校验、无活动 Promotion 引用的 processed 条目。
- Capture 必须保留原始输入；Organize 可以拆分或合并语义草稿，但不能改写 payload。
