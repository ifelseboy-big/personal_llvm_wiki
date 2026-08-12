# Inbox 规则

- `inbox/` 是临时工作区，不是可信事实源，也不得进入查询索引。
- 每条输入必须完整保留在 `payload/`；初步整理只写 `item.md`，不得覆盖原始字节或用摘要替代。
- 只有 `pending` 条目可进入新 Promotion；只有成功 Promotion 明确 consume 的条目可变为 `processed`。
- Organize 必须先用 `inbox list/show` 选定授权范围，再只按 `inbox show` 返回的规范 `payload_path` 读取原始 payload。不得遍历或猜测 Inbox 路径。
- `inbox show` 返回的 `item_hash`、`payload_hash` 是 Promotion manifest 的来源；发现读取失败或哈希变化时停止。
- 回答知识问题不得读取 Inbox。清理是独立动作，只能删除已校验、无活动 Promotion 引用的 processed 条目。
- Capture 必须保留原始输入；Organize 可以拆分或合并语义草稿，但不能改写 payload。
