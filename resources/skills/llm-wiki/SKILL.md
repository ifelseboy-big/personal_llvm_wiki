---
name: llm-wiki
description: 使用 llm-wiki CLI 存入、查询、整理、发布和追溯个人可信知识；当用户要求记录知识、查个人知识库、整理 raw 材料或发布知识时使用。
---

# llm-wiki

先运行 `llm-wiki locate --json --no-interactive` 确认目标知识库。跨项目工作时显式传入 `--wiki <alias|path>`。

## 强制流程

1. 新输入只能调用 `llm-wiki raw add <file|directory|-> --json --no-interactive`。
2. 查询调用 `llm-wiki query "<问题>" --json --no-interactive`，基于返回的 evidence 回答并引用 knowledge ID、相对路径和 raw 来源。
3. 整理时先读取 raw，再准备知识草稿，调用 `llm-wiki publish propose --source <raw-id> --file <draft> --json --no-interactive`。
4. 展示 `llm-wiki publish diff <change-id>` 供用户审阅。除非用户明确要求审批，不执行 `publish apply`。
5. 发布后调用 `llm-wiki build --json --no-interactive`。

不得直接修改 `knowledge/` 或 `llm-wiki/`，不得将 `.llm-wiki/index.sqlite` 当作事实来源，不解析面向人的输出。失败时根据 JSON `error.code` 处理；冲突或 stale 提案必须重新生成，不能强制覆盖。
