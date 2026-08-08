# 知识库维护约定

本目录是由 `llm-wiki` 管理的可信知识库。维护者和 AI 必须遵守以下边界。

## 事实层级

- `raw/` 保存原始材料和未经确认的记录，只能通过 `llm-wiki raw add` 导入。
- `knowledge/` 保存经过发布确认的可信知识，是最终事实依据。
- `llm-wiki/` 是从 `knowledge/` 生成的 AI 派生视图，可以删除和重建，不得反向覆盖可信知识。
- `.llm-wiki/index.sqlite` 仅为索引。不得把数据库内容当作知识或元数据来源。

## 必须使用的流程

1. 存入材料：`llm-wiki raw add <file|directory|-> --json --no-interactive`。
2. 查询证据：`llm-wiki query "<问题>" --json --no-interactive`。
3. 整理知识：准备 Markdown 草稿后执行 `llm-wiki publish propose`。
4. 发布确认：检查 `llm-wiki publish diff`，由用户明确执行 `llm-wiki publish apply`。
5. 派生构建：发布后执行 `llm-wiki build`。

不得直接创建或修改 `knowledge/` 中的发布文件，不得直接编辑 `llm-wiki/`，不得绕过变更集，也不得根据 SQLite 反向恢复知识文件。

## 质量要求

- 区分事实、推断、意见和待验证内容。
- 每条可信知识必须保留 raw 来源 ID 与内容哈希。
- 优先更新已有知识，避免同义重复；保留冲突观点及各自来源。
- 标题必须具体，正文必须能脱离对话上下文独立理解。
- 查询回答应引用 `query --json` 返回的 knowledge ID、相对路径和来源。

详细规则见：

- [采集规则](rules/capture.md)
- [元数据与 Obsidian Properties 规则](rules/metadata.md)
- [发布规则](rules/publish.md)
- [派生层规则](rules/derived.md)
- [知识质量规则](rules/quality.md)

各类草稿结构见 `templates/`。CLI 返回结构化错误时，应根据 `error.code` 处理，不解析终端文案。
