# personal 2.0.0 设计

该模板实现本地优先的 Inbox -> Promotion -> Knowledge 流程。

- Inbox 完整保存原始 payload 和初步整理，但不参与事实查询。
- Vault 内 Agent 负责查重、拆分、合并和准备 Promotion；它不是第三个可安装 Skill。
- Promotion 冻结多输入多输出变更集，人工批准绑定完整 diff 与 plan hash。
- Knowledge 自包含且是唯一事实源；lineage 不依赖 Inbox 永久存在。
- SQLite 只索引 Knowledge，可删除重建。
- Obsidian 视图和 wikilink 是可选增强，核心流程只依赖普通文件与 CLI。
