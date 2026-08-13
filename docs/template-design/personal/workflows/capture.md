# Capture / Add Workflow

触发：用户明确要求记住、保存、收集或稍后整理内容。

1. 按 `AGENTS.md` 定位并固定 `<vault-root>`，读取当前 `content-pack.json` 与 `rules/inbox.md`。
2. 为每个独立 payload 在受管目录外准备 note，清楚区分原话、初步摘要、来源与上下文、待整理问题；note 不得替代或改写原始输入。
3. 文件输入执行：
   `llm-wiki inbox add <file> --note-file <note> --wiki <vault-root> --json --no-interactive`
4. 文本通过 stdin 输入时必须提供可理解的文件名：
   `llm-wiki inbox add - --name <name> --source <source> --note-file <note> --wiki <vault-root> --json --no-interactive`
5. 多文件或目录输入先生成 batch manifest，再执行：
   `llm-wiki inbox add --batch-manifest <manifest.json> --wiki <vault-root> --json --no-interactive`

```json
{
  "schema_version": 1,
  "items": [
    { "input": "source/file.pdf", "note_file": "notes/file.md", "title": "明确标题", "source": "用户提供" }
  ]
}
```

相对路径以 batch manifest 所在目录为基准；每项必须有独立 `note_file`。CLI 拒绝敏感文件时停止并报告，不得自动添加 `--allow-sensitive`。

成功后核对 JSON 中每项的 Inbox ID、`item_path`、`payload_path`、`item_hash`、`payload_hash` 和 `pending` 状态再返回。Capture 不查询 Knowledge、不选择 category/type、不创建 Promotion、不写 `knowledge/`。保存授权不等于整理或发布授权。
