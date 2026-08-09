---
name: llm-wiki
description: 使用 llm-wiki CLI 存入、查询、整理、发布和追溯个人可信知识；当用户要求记录知识、查个人知识库、整理 raw 材料或发布知识时使用。
---

# llm-wiki

## 启动目标知识库

每次开始一个知识库工作流时：

1. 运行 `llm-wiki locate --json --no-interactive`；跨项目时显式传入 `--wiki <alias|path>`。
2. 从返回的 `data.root` 读取目标知识库根目录下的 `AGENTS.md`，不得用当前工作目录或 Skill 中的旧规则代替。
3. 按该 `AGENTS.md` 的操作路由，只读取本次任务要求的 `rules/` 和 `templates/` 文件。
4. 记录目标 wiki ID 和规则文件状态；切换知识库或规则变化后重新读取。

同一任务、同一 wiki 且相关规则未变化时，不必在每条 CLI 命令前重复读取。

## 执行边界

- `knowledge/` 中已发布 Markdown 是唯一最终事实源。
- `raw/` 是证据，`.llm-wiki/index.sqlite` 只提供检索候选，`llm-wiki/` 只是可重建派生视图。
- 执行 `raw add` 前，必须先完成 `AGENTS.md` 指定的采集规则读取。
- `query` 只负责检索并回读发布文档；只有返回文档出现非当前状态、有效期或替代关系时，才按 `AGENTS.md` 读取生命周期规则。
- 知识整理、去重、类型选择、冲突处理和生命周期判断由 Agent 依照目标知识库规则完成，CLI 只提供受控操作。
- 不得直接修改 `knowledge/` 或 `llm-wiki/`。发布必须展示 diff；除非用户明确批准，不执行 `publish apply`。
- AI 调用始终使用 `--json --no-interactive`，只处理 JSON 字段和 `error.code`。stale 或冲突不能强制覆盖。
