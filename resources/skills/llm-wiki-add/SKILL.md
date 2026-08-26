---
name: llm-wiki-add
description: 用户明确要求记住、收集、保存或加入稍后整理的信息时使用。
---

1. 运行 `llm-wiki locate --json --no-interactive` 定位 Vault，把返回的 `wiki.path` 固定为 `<vault-root>`。
2. 读取 Vault `AGENTS.md` 与 `content-pack.json`，从 `workflows` 路由并完整执行 Capture Workflow。
3. 完整保存用户明确提供的文本、文件或目录内容，只确定简短的初步功能名称。无需先生成摘要或初步整理笔记。
4. 单输入调用 `llm-wiki inbox add <file|-> --title <初步功能名称> --wiki <vault-root> --json --no-interactive`；stdin 必须带 `--name`。CLI 按 `YYYY-MM-DD-初步功能名称.md` 命名，ID 只留在元数据中。目录批量采集用 `--batch-manifest`，`note_file` 可省略。
5. 不查询 Inbox，不写 `knowledge/`，不创建或批准 Promotion。
6. 返回可读笔记路径和 `pending` 状态，说明它可继续编辑整理，尚未成为可信知识。

不得用摘要替换用户输入。用户另行提供或要求初步笔记时可使用 `--note-file`。Inbox 不维护采集时哈希；严格快照校验从发布流程开始。
