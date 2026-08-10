# 检索索引

事实流向是单向的：

```text
raw/ -> publish proposal -> knowledge/ -> .llm-wiki/index.sqlite
```

`knowledge/` 是已发布事实层和唯一最终数据源。SQLite 只是可删除重建的候选索引，任何情况下都不能反向覆盖知识文件。

## SQLite 边界

SQLite 的全文检索只索引已发布 knowledge，并保存可重建的路径、ID、标题、类型、标签、来源关联、内容哈希和正文 chunk；raw 正文不得写入全文索引。

每个命中候选都必须回读并验证对应的原始 knowledge Markdown；索引中的元数据、摘要或片段不能作为独立事实。索引缺失或陈旧时运行 `llm-wiki index update`，需要完整重建时运行 `llm-wiki index rebuild`。

索引损坏时删除并重建是安全的。知识文件损坏时不能用 SQLite 恢复为权威版本，必须回到版本历史、发布提案或原始资料。
