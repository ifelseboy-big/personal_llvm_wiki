# 检索索引

```text
knowledge/ -> .llm-wiki/index.sqlite -> query candidates -> reread knowledge Markdown
```

- SQLite 只索引 Knowledge，不读取 Inbox、草稿、规则或视图。
- SQLite 只保存可从 Knowledge 重建的路径、ID、元数据、文件哈希和正文 chunk。
- 每个候选返回前必须回读 Markdown，并校验 ID、规范路径、完整文件哈希、正文哈希、chunk 哈希和行边界。
- 索引缺失返回 `INDEX_NOT_FOUND`，文件集合或哈希漂移返回 `INDEX_STALE`；query 不自动写入或修复。
- 可用 `index update` 增量维护，或用 `index rebuild` 在临时数据库完成校验后原子替换。

不得用 SQLite 覆盖或恢复 Knowledge。Inbox 清理不触发索引更新。
