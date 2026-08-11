# personal 2.0.0 模板契约

## 目录与所有权

`template.toml` 的 `managed_files` 是唯一受管文件清单。resources 与本目录中的同路径文件必须同字节。

模板安装或升级可以写受管规则、模板、说明和可选视图，但不得写入 `inbox/` 或 `knowledge/`。冲突必须通过安装基线识别，不得覆盖用户修改。

## 治理

- governance 版本固定为 `personal-2.0`。
- Knowledge 类型为 claim、concept、guide、tutorial、reference、decision、project。
- description、lifecycle 和自包含正文是必需内容。
- relation 属性保存稳定 `know_` ID；路径和 wikilink 不参与完整性权威。
- lineage 保存发布时 Inbox ID、payload hash、source 和 captured_at，仅为历史记录。
- 不强制来源文件脚注，也不要求 Inbox 文件在发布后永久存在。

## Agent 流程

Vault `AGENTS.md` 必须要求：读取规则；用 `inbox list/show` 盘点；用 `query/show` 查重；在非 Knowledge 路径准备草稿；依次运行 plan 和 diff；展示完整冻结计划并停止；只在用户针对 promotion ID、diff 和 plan hash 明确批准后运行 apply。

## 独立性

模板不得要求 Obsidian、`.obsidian/`、Bases、社区插件或模板变量才能生成、验证、发布、查询和维护 Knowledge。`views/` 可删除且不影响正确性。
