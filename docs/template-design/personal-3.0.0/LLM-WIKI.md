---
title: LLM Wiki 使用入口
aliases:
  - 知识库首页
tags:
  - llm-wiki
---

# LLM Wiki 使用入口

日常只需要两个入口：Add 保存输入，Query 查询可信知识。Vault Agent 根据 `content-pack.json` 选择领域、知识结构和 Workflow；CLI 负责安全写入、冻结批准和可信回读。

## Add

告诉 Agent“记住/保存/收集这段内容”。Capture Workflow 会完整保存原始输入到 pending Inbox，不会直接发布 Knowledge。需要整理时再执行 Organize 与 Publish。

```bash
llm-wiki inbox add ./article.pdf --note-file ./article-note.md --wiki <alias>
llm-wiki inbox list --status pending --wiki <alias> --json --no-interactive
```

## Query

直接询问已经沉淀的知识。Query Workflow 只使用 `query/show` 返回并回读验证后的 Knowledge 证据，不读取 Inbox 或草稿。

```bash
llm-wiki query "问题" --wiki <alias> --json --no-interactive
llm-wiki show <knowledge-id> --wiki <alias> --json --no-interactive
```

## 整理、发布与维护

要求 Agent 整理指定 Inbox 时，它会查重、选择独立的 category 和 type、在 `knowledge/` 外生成草稿，并准备 Promotion。你必须看到 promotion ID、plan hash 和完整 diff；只有明确批准该冻结计划后才会 apply。

维护已有知识同样通过 Promotion 完成。Agent 不会直接改 `knowledge/`，也不会把 SQLite、Inbox、模板或 Workflow 当成事实。

## 目录边界

| 目录 | 用途 | 事实属性 |
| --- | --- | --- |
| `inbox/` | 原始 payload 与初步整理 | 临时，不用于 Query 回答 |
| `knowledge/` | 明确批准的自包含知识 | 唯一可信事实源 |
| `.llm-wiki/promotions/` | 冻结计划、diff 与状态 | 审阅和恢复数据 |
| `.llm-wiki/index.sqlite` | 候选索引 | 可重建缓存 |
| `content-pack.json`、`rules/`、`templates/`、`workflows/` | 声明式治理与 Agent 资源 | 非用户事实 |

Obsidian、Bases、Properties 和 wikilink 都是可选展示增强，不是运行依赖。
