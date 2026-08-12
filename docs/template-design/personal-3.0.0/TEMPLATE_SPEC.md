# personal 3.0.0 内容包契约

## 目录与所有权

`template.toml` 的 `managed_files` 是唯一受管清单，`content_pack` 指向机器策略。resources 与本目录中的同路径文件必须同字节。

安装或升级可以写受管策略、规则、模板、Workflow、说明和可选视图，但不得写入 `inbox/` 或 `knowledge/`。冲突通过安装基线识别，不覆盖用户修改。

## 唯一机器语义

`content-pack.json` 遵守 `schemas/content-pack-v1.schema.json`，并唯一声明：

- content pack/governance identity；
- category 与 type；
- 公共字段、类型字段、条件必填、关系、草稿默认关系和生命周期召回语义；
- type 到模板的映射；
- Capture、Organize、Publish、Maintain、Query Workflow 路由。

Markdown 规则只能解释如何使用该文件，不维护第二份枚举。Core 必须严格拒绝 identity/version 不匹配、未知 type/category、字段规则失败或引用路径不安全的实例。

## 内容质量

十种 Knowledge 模板必须包含适用条件、边界、来源或证据、验证或复核要求，而不是空标题集合。configuration 模板必须禁止密码、Token、私钥、cookie、恢复码等秘密，只记录非敏感值、影响、验证和安全引用位置。

未知用户属性全链路保留。relation 属性保存稳定 `know_` ID；lineage 保存发布时 Inbox ID、payload hash、source 和 captured_at，均不依赖 Inbox 永久存在。

## Agent Workflow

Vault `AGENTS.md` 必须根据机器策略路由：Capture 完整保存原始输入；Organize 查重并选择正交 category/type；Publish 运行 plan/diff、展示完整冻结计划并停止，明确批准后才 apply；Maintain 也只通过 Promotion 改事实；Query 只使用 CLI 回读验证后的 Knowledge。

## 独立性

内容包不得要求 Obsidian、`.obsidian/`、Bases、社区插件或模板变量才能生成、验证、发布、查询和维护 Knowledge。`views/` 可删除且不影响正确性。
