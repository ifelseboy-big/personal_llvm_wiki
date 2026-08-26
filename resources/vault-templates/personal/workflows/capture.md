# Capture / Add Workflow

触发：用户明确要求记住、保存、收集或稍后整理内容。

1. 按 `AGENTS.md` 定位并固定 `<vault-root>`，读取当前 `content-pack.json` 与 `rules/inbox.md`。
2. 为输入确定简短的初步功能名称，使用 `--title`；CLI 生成 `YYYY-MM-DD-初步功能名称.md`，不把 ID 放进文件名。只命名和保存，不要求先生成摘要或完成整理。
3. 文件输入执行：
   `llm-wiki inbox add <file> --title <初步功能名称> --wiki <vault-root> --json --no-interactive`
4. 文本通过 stdin 输入时必须提供名称：
   `llm-wiki inbox add - --name <name> --title <初步功能名称> --source <source> --wiki <vault-root> --json --no-interactive`
5. 用户另行提供或要求初步笔记时可加 `--note-file <note>`；不得用摘要替换用户输入。多文件或目录输入先生成 batch manifest，再执行 `inbox add --batch-manifest <manifest.json>`。

```json
{
  "schema_version": 1,
  "items": [
    { "input": "source/file.pdf", "title": "登录功能资料", "source": "用户提供" }
  ]
}
```

相对路径以 batch manifest 所在目录为基准，`note_file` 可省略。CLI 拒绝敏感文件时停止并报告，不得自动添加 `--allow-sensitive`。

文本默认直接成为可编辑笔记；二进制材料或带独立 note 的输入保存在共享 attachments 中。成功后核对 JSON 中的 Inbox ID、`item_path`、`payload_path` 和 `pending` 状态。返回的哈希是当前读取快照，不限制后续整理。Capture 不查询 Knowledge、不选择 category/type、不创建 Promotion、不写 `knowledge/`。保存授权不等于整理或发布授权。
