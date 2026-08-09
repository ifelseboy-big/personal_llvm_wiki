# 元数据与 Obsidian Properties 规则

知识库使用 Markdown YAML frontmatter，兼容 Obsidian Properties。属性分为用户属性和系统属性。

## 用户属性

- `title`、`type`、`aliases`、`tags` 参与发布和检索。
- raw 模板中的 `origin` 描述材料的真实来源类型；显式传入 `raw add --origin` 时以命令参数为准，否则保留模板值。
- `description` 用一句话说明内容，不能引入正文或来源没有支持的新事实。
- `cssclasses` 是 Obsidian 样式类列表；`related` 是相关知识链接列表，内部链接必须写成带引号的 `"[[知识标题]]"`。
- 其他自定义属性允许保留。属性值应是简短、原子的文本、数字、布尔值、日期、日期时间或列表，不在属性中存放长篇 Markdown。
- 更新知识时，草稿中省略的用户属性保持原值，同名属性覆盖原值，显式写为 `null` 才删除。`tags: []` 或 `aliases: []` 表示明确清空列表。

## 系统属性

`schema_version`、`id`、`status`、`sources`、`captured_at`、`published_at`、`updated_at`、`content_hash`、`media_type`、`original_name`、`asset` 及派生构建字段由 `llm-wiki` 管理。

草稿中的系统属性不能覆盖工具生成值。`sources` 保存 raw ID 与发布时内容哈希的绑定，虽然 Obsidian Properties 界面不能直接编辑其嵌套结构，但不得拍平、删除或转存到 SQLite。需要查看或修改时使用源码模式和受控发布流程。

SQLite 中的 `metadata_json`、全文字段和 chunk 正文只是文件解析缓存；查询只用它们选择候选。最终元数据必须重新读取 `knowledge/` Markdown frontmatter，不能从 SQLite、raw 或派生文件补写事实。
