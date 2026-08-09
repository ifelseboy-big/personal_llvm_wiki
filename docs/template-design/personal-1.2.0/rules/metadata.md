# 元数据与 Obsidian Properties 规则

知识库使用 Markdown YAML frontmatter。用户属性面向人、Obsidian 和检索；系统属性由 CLI 生成并维护。

## 用户属性

- 属性只保存短文本、数字、布尔值、日期、日期时间、内部链接或列表，不保存长篇 Markdown。
- 同一属性名在整个 Vault 中保持相同类型。例如 `authors` 始终是列表，`review_after` 始终是日期。
- `tags`、`aliases`、`cssclasses` 使用 Obsidian 官方复数属性名。
- tags 用于少量跨类型主题，不复制 `type`、`lifecycle`、`origin` 等已有结构字段。
- 内部链接必须带引号，并指向真实 Vault 路径。
- 自定义用户属性允许保留并进入可重建检索索引。

## Knowledge 公共属性

| 属性 | 类型 | 要求 |
|---|---|---|
| `type` | text | 必填，遵守 [[rules/types|类型规则]] |
| `title` | text | 必填，与第一个 H1 一致 |
| `description` | text | 必填，一句话摘要 |
| `lifecycle` | text | 必填，遵守 [[rules/lifecycle|生命周期规则]] |
| `valid_from` | date | 可选，开始适用日期 |
| `valid_until` | date | 可选，结束适用日期 |
| `review_after` | date | 可选，复查日期 |
| `aliases` | list | 可选，同义名称 |
| `tags` | list | 可选，主题标签 |
| `cssclasses` | list | 可选，Obsidian 样式类 |
| `related` | list[link] | 可选，相关可信知识 |
| `supersedes` | list[link] | 可选，被当前知识替代的知识 |
| `superseded_by` | list[link] | 可选，替代当前知识的知识 |

## 更新语义

- 新建知识以草稿属性为准；空日期在发布后省略。
- 更新知识时，草稿省略用户属性表示保留原值。
- 同名属性覆盖旧值；写为 `null` 表示删除。
- `tags: []`、`aliases: []` 或其他空列表表示明确清空。

## 系统属性

以下字段不得被草稿覆盖：

```text
schema_version, id, status, sources,
captured_at, published_at, updated_at,
content_hash, media_type, original_name, asset,
derived_from, compiler, compiler_version,
build_fingerprint, generated_at
```

`sources` 保存 raw ID 与发布时内容哈希的绑定。Obsidian Properties 界面不支持编辑其嵌套结构，因此只在源码模式查看，不手工修改、不拍平、不转存到 SQLite。

SQLite 的 `metadata_json`、全文字段和 chunk 正文只是 Markdown 的解析缓存，可以随时删除重建。查询只用它们选择候选，最终 metadata、正文和来源必须重新读取 `knowledge/` 文件。
