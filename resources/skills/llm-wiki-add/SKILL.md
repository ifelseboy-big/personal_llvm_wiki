---
name: llm-wiki-add
description: 使用 llm-wiki 把用户明确指定的文本、文件或目录采集到个人知识库 raw 证据层。用户说“记住、记录、收集、保存这段材料”时使用；只执行采集，不检索 raw，不生成 knowledge，也不发布。
---

# LLM Wiki Add

1. 运行 `llm-wiki locate --json --no-interactive`；用户指定知识库时加 `--wiki <alias|path>`。
2. 从 `data.root` 读取目标 Vault 的 `AGENTS.md`，并读取其采集与元数据规则。
3. 对对话文本使用显式 stdin：`llm-wiki raw add - --name <可理解文件名> --origin <来源> --wiki <data.root> --json --no-interactive`。对文件或目录把 `-` 换成明确路径。
4. 返回生成的 raw ID、路径和 warning，明确状态为“已采集、未发布”。

`raw add` 不是发布。不要调用 `query`、`publish`，也不要把 raw 内容描述为可信知识；raw 不进入全文检索。
