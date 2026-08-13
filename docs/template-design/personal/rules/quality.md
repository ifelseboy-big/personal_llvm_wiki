# 质量检查

Promotion plan 前确认：

- Knowledge 自包含结论、条件、范围、例外与必要引用，不要求回看 Inbox 或使用 Obsidian。
- 观察、原话、推断和建议没有混写；未知内容没有被猜测补齐。
- category、type、公共字段和类型字段均符合 `content-pack.json`；标题与第一个 H1 一致。
- 一篇文档只有一个主要用途；需要时已拆分或合并。
- `content-pack.json` 声明的关系字段只使用真实稳定 Knowledge ID；新建目标使用 CLI 返回的 `proposed_knowledge_id`，声明 reciprocal 的关系必须在同一 Promotion 双向一致。
- 所有草稿均在 `knowledge/` 外，且没有未解析变量或 `llm-wiki:prompt` 注释。
- Promotion 覆盖全部目标，consume 的 Inbox 与 lineage 对应，diff 可完整审阅。
- 配置知识不含密码、Token、私钥、cookie、恢复码或其他可直接使用的秘密。
