---
name: llm-wiki-maintain
description: 检查和修复 llm-wiki 个人知识库的可重建层，包括派生视图、SQLite knowledge 检索索引、状态和 doctor 问题。索引缺失或陈旧、发布后刷新警告、需要重建或健康检查时使用；不修改 raw 或 knowledge 事实。
---

# LLM Wiki Maintain

1. 运行 `llm-wiki locate --json --no-interactive`；用户指定知识库时加 `--wiki <alias|path>`。
2. 从 `data.root` 读取目标 Vault 的 `AGENTS.md` 和派生层规则。
3. 用 `status`、`doctor`、`build status` 和 `index status` 判断问题，不从 SQLite 或 `llm-wiki/` 反向恢复事实。
4. 派生层陈旧时运行 `llm-wiki build --wiki <data.root> --json --no-interactive`；索引缺失或陈旧时运行 `index update`，只有明确需要完整重建时才运行 `index rebuild`。
5. 重跑状态检查并报告修复结果。

本 Skill 只能重建派生层与索引。不得执行 `raw add`、`publish apply`，也不得直接修改 `raw/`、`knowledge/` 或 `llm-wiki/`。
