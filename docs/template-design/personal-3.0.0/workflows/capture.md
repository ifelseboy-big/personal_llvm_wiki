# Capture / Add Workflow

触发：用户明确要求记住、保存、收集或稍后整理内容。

1. 读取 Vault `AGENTS.md`、`content-pack.json` 与 `rules/inbox.md`。
2. 保留用户提供的完整文本或文件字节；为每个独立 payload 准备不丢信息的初步 note，区分原话、摘要、上下文和待整理问题。
3. 单输入调用 `llm-wiki inbox add <file|-> --note-file <note> --json --no-interactive`；目录输入使用 batch manifest，为每项映射独立 note。
4. 返回 Inbox ID、item/payload 路径和 pending 状态。

停止条件：Capture 不查询 Knowledge、不整理分类、不创建 Promotion、不写 `knowledge/`。保存授权不等于发布授权。
