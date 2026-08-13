# personal 设计

personal 是由数据驱动的 Vault content pack，不是 Go Core 中的产品分支。

- 四个 category 表示需求开发、个人学习、配置信息和业务知识领域。
- 十个 type 表示文档结构与主要用途；category 与 type 正交。
- `content-pack.json` 唯一声明分类、类型、字段、生命周期、关系、模板和 Workflow 路由。
- Capture、Organize、Publish、Maintain、Query 负责语义工作；CLI 负责安全边界。
- 每个 Workflow 固定 Vault root、使用可复制执行的 JSON/no-interactive 命令，并定义授权与失败停止条件。
- Inbox show 暴露经验证的 payload 路径和 manifest 哈希；template create 提供 CLI 生成的新 Knowledge ID。
- Promotion 冻结多输入多输出变更集，批准绑定完整 diff 与 plan hash。
- Agent 禁止直接写 Knowledge；lineage 不依赖 Inbox 永久存在。
- Maintain 检查零写入，处理必须先采集维护依据；reciprocal 变更在同一 Promotion 内闭合。
- SQLite 只索引并回读验证 Knowledge，可删除重建。
- 配置模板禁止秘密，只允许非敏感配置事实和安全引用位置。
- Obsidian 视图与 wikilink 是可选增强。

资源目录和本设计基线中的 `template.toml` 及其全部 `managed_files` 必须同字节。新增分类、类型、字段、模板或 Workflow 只修改内容包数据与基线，不修改 Go。
