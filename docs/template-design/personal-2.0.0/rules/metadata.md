# 元数据规则

YAML frontmatter 是格式权威。Obsidian Properties 只是可选编辑视图，不改变字段语义。

## Knowledge 用户属性

| 属性 | 要求 |
| --- | --- |
| `type` | 必填，遵守类型规则 |
| `title` | 必填，与第一个 H1 一致 |
| `description` | 必填，可独立理解的一句话摘要 |
| `lifecycle` | 必填：current、disputed、superseded、retracted |
| `related`、`supersedes`、`superseded_by` | 稳定 `know_` ID 列表，不使用路径或标题作为权威 |
| `tags`、`aliases` | 可选的唯一字符串列表 |
| 类型专属字段 | 遵守 `rules/types.md` |

未知用户属性必须往返保留。长篇内容写在正文，不塞入 Properties。

## 系统属性

`schema_version`、`id`、`status`、`published_at`、`updated_at`、`content_hash`、`governance_version` 与 `lineage` 只能由 CLI 生成。

Knowledge 的 `lineage` 记录 Inbox ID、发布时 payload hash、来源和采集时间。它是历史元数据，不要求对应 Inbox 永久存在。

Inbox 的 `payload`、`payload_hash`、`payload_bytes`、`status`、`processed_at` 和 `knowledge_ids` 同样由 CLI 维护。
