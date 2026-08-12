# Query Workflow

触发：用户询问已经沉淀的可信知识。

1. 按 `AGENTS.md` 固定 `<vault-root>`，读取当前 `content-pack.json` 与 `rules/index.md`。
2. 运行：
   `llm-wiki query <question> --wiki <vault-root> --json --no-interactive`
   只有用户明确要求审计历史、失效或过期知识时才添加 `--include-inactive`。
3. 对实际用于回答的每个 Knowledge ID 运行：
   `llm-wiki show <id> --wiki <vault-root> --json --no-interactive`
4. 只依据 `show` 回读的完整 Markdown 组织回答；清楚区分直接事实、基于事实的推断和知识库未覆盖内容，并显著表达争议、有效期、过期、复核及其他 warning。
5. 返回 Knowledge ID 与规范路径。没有证据时明确回答“知识库中没有足够的已发布证据”，不得用 Inbox、草稿、模板、SQLite 行或外部常识补成知识库结论。

Query 零写入，禁止自动修复或维护索引。遇到 `INDEX_NOT_FOUND`、`INDEX_STALE`、`KNOWLEDGE_INVALID`、`KNOWLEDGE_READ_FAILED`、`RECOVERY_REQUIRED` 或任何无法获得完整可信证据的错误时停止并原样报告稳定错误码。
