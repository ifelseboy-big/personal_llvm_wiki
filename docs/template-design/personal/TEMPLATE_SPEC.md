# personal 内容包契约

## 目录与所有权

`template.toml` 的 `managed_files` 是唯一受管清单，`content_pack` 指向机器策略。resources 与本目录中的同路径文件必须同字节。

安装或升级可以写受管策略、规则、模板、Workflow、说明和可选视图，但不得写入 `inbox/` 或 `knowledge/`。冲突通过安装基线识别，不覆盖用户修改。

## 唯一机器语义

`content-pack.json` 遵守 `schemas/content-pack.schema.json`，并唯一声明：

- content pack/governance identity；
- category 与 type；
- 公共字段、类型字段、条件必填、关系、草稿默认关系和生命周期召回语义；
- type 到模板的映射；
- Capture、Organize、Publish、Maintain、Query Workflow 路由。

Markdown 规则只能解释如何使用该文件，不维护第二份枚举。Core 必须严格拒绝 identity/version 不匹配、未知 type/category、字段规则失败或引用路径不安全的实例。

## 内容质量

六种 Knowledge 模板必须包含适用条件、边界、来源或证据、验证或复核要求，而不是空标题集合。配置说明必须禁止密码、Token、私钥、cookie、恢复码等秘密，只记录非敏感值、影响、验证和安全引用位置。

模板正文先呈现该类型的核心答案，再展开依据与细节：方案说明呈现问题、目标与当前选择，操作指南呈现目标与入口，知识笔记呈现结论或定义，配置说明与规则说明呈现适用事实，复盘总结呈现结果与经验。尚未决定或验证的内容必须在开头如实标明。

一个主题按主要用途选择模板，不因出现需求、设计、决策、概念或协作步骤而强制拆篇。方案的实现细节与备选比较、指南的多人交接、笔记的学习过程按实际材料展开；缺失材料保留未决，不为覆盖合并后的全部用途编造内容。

未标注“按需”的章节保留其核心问题，使用短段落、明确标签、步骤或表格提供可填写的结构；按需内容不适用时删除，确实相关但信息不足时记为未决。表格用于比较、判定、交接或验证，不要求将解释性内容全部表格化。不得为补齐结构编造事实、指标、备选、批准、责任人或实测结果。

各模板以 `llm-wiki:prompt` 承载选型、填写与完成检查指引。发布前删除所有提示、空白占位、未使用的表格行与不适用的按需内容，保留可独立阅读的正文；来源应有精确定位，验证方法、计划与实际结果应明确区分。这些是内容编写与审查要求；类型字段仅由机器策略声明，不在 Go 中固定章节或类型语义。

未知用户属性全链路保留。relation 属性保存稳定 `know_` ID；lineage 保存发布时 Inbox ID、payload hash、source 和 captured_at，均不依赖 Inbox 永久存在。

## Agent Workflow

Vault `AGENTS.md` 必须根据机器策略路由，并把解析出的 Vault root 显式传给后续所有 CLI 命令。Capture 完整保存原始输入并正确处理 stdin/batch；Organize 从 `inbox show` 返回的规范路径读取 payload、用 CLI 返回的哈希构造 manifest，并选择正交 category/type；Publish 运行 plan/diff、展示完整冻结计划并停止，明确批准后才 apply；Maintain 检查阶段零写入，处理阶段先 Capture 维护依据并只通过 Promotion 改事实；Query 只使用 CLI 回读验证后的 Knowledge。

`template create` 返回 CLI 生成的 `proposed_knowledge_id`，供同一 Promotion 中的新建目标和 reciprocal 关系使用。`promote apply` 返回事务状态；Agent 不得隐藏 `files_committed`、索引失败或恢复 warning。

## 独立性

内容包不得要求 Obsidian、`.obsidian/`、Bases、社区插件或模板变量才能生成、验证、发布、查询和维护 Knowledge。`views/` 可删除且不影响正确性。
