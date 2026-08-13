---
name: llm-wiki-add
description: 用户明确要求记住、收集、保存或加入稍后整理的信息时使用。
---

1. 运行 `llm-wiki locate --json --no-interactive` 定位 Vault，把返回的 `wiki.path` 固定为 `<vault-root>`。
2. 读取 Vault `AGENTS.md` 与 `content-pack.json`，从 `workflows` 路由并完整执行 Capture Workflow。
3. 完整保留用户明确提供的文本、文件或目录内容，生成不丢信息的初步标题、摘要、来源和可选标签到临时 note 文件。
4. 单输入调用 `llm-wiki inbox add <file|-> --note-file <note> --wiki <vault-root> --json --no-interactive`；stdin 必须带 `--name`。目录批量采集使用 `inbox add --batch-manifest <file>`，为每个 payload 映射独立 note。
5. 不查询 Inbox，不写 `knowledge/`，不创建或批准 Promotion。
6. 返回 Inbox ID、item/payload 路径和 `pending` 状态，明确说明它尚未成为可信知识。

不得用摘要替换用户输入；原始字节必须保留到条目 processed 后被明确清理。
