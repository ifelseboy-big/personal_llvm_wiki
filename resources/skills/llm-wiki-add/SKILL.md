---
name: llm-wiki-add
description: 用户明确要求记住、收集、保存或加入稍后整理的信息时使用。
---

1. 运行 `llm-wiki locate --json --no-interactive` 定位 Vault，把返回的 `wiki.path` 固定为 `<vault-root>`。
2. 读取 Vault `AGENTS.md` 与 `content-pack.json`，从 `workflows` 路由并完整执行 Capture Workflow。
3. 完整保存用户明确提供的文本、文件或目录内容，确定简短的初步功能名称。能从输入确认内容时写一句简述；不能可靠判断时省略简述，让 CLI 按标题和文件名生成概览。简述不得替代原文。
4. 单输入调用 `llm-wiki inbox add <file|-> --title <初步功能名称> --summary <内容简述> --wiki <vault-root> --json --no-interactive`；没有可靠简述时省略 `--summary`，stdin 必须带 `--name`。CLI 为主题创建 `inbox/YYYY-MM-DD-初步功能名称/`，`index.md` 列出本次简述和文件链接，笔记放 `docs/`，原始附件按需放 `attachments/`，ID 只留在笔记元数据中。目录批量采集用 `--batch-manifest`，同一主题的条目使用相同 title 以共用目录，每项可带 `summary`，`note_file` 可省略。
5. 不查询 Inbox，不写 `knowledge/`，不创建或批准 Promotion。
6. 返回可读笔记路径和 `pending` 状态，说明它可继续编辑整理，尚未成为可信知识。

不得用摘要替换用户输入。用户另行提供或要求初步笔记时可使用 `--note-file`。Inbox 不维护采集时哈希；严格快照校验从发布流程开始。
