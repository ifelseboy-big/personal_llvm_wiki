---
title: LLM Wiki 使用入口
aliases:
  - 知识库首页
tags:
  - llm-wiki
---

# LLM Wiki 使用入口

这个 Vault 把临时输入、冻结审阅和可信知识分开。`knowledge/` 是唯一事实源，`inbox/` 可在处理后清理，SQLite 可随时重建。

| 目录 | 用途 | 事实属性 |
| --- | --- | --- |
| `inbox/` | 原始 payload 与初步整理 | 临时，不可用于查询回答 |
| `knowledge/` | 经明确批准的自包含知识 | 唯一可信事实源 |
| `.llm-wiki/promotions/` | 冻结计划、diff 与审阅状态 | 审阅和恢复数据 |
| `.llm-wiki/index.sqlite` | Knowledge 检索候选 | 可重建缓存 |
| `rules/`、`templates/` | 治理和草稿资源 | 非用户事实 |
| `views/` | 可选 Obsidian 展示 | 不参与正确性 |

## 采集

```bash
llm-wiki inbox add ./article.pdf --note-file ./article-note.md --wiki <alias>
llm-wiki inbox add - --name meeting.txt --note-file ./meeting-note.md --wiki <alias>
llm-wiki inbox list --status pending --wiki <alias> --json --no-interactive
```

采集只生成 pending Inbox，不会创建 Knowledge。

## 整理与发布

在 Vault 内要求 AI 整理指定 Inbox。AI 会查重、准备草稿和 manifest，并运行：

```bash
llm-wiki promote plan --manifest ./promotion.json --wiki <alias>
llm-wiki promote diff <promotion-id> --wiki <alias>
llm-wiki promote apply <promotion-id> --approve <plan-hash> --wiki <alias>
```

必须先审阅完整 diff；批准绑定 promotion ID 与 plan hash。Apply 只使用冻结文件。

## 查询与清理

```bash
llm-wiki query "问题" --wiki <alias> --json --no-interactive
llm-wiki show <knowledge-id> --wiki <alias> --json --no-interactive
llm-wiki inbox clean <inbox-id> --dry-run --wiki <alias> --json --no-interactive
llm-wiki inbox clean <inbox-id> --yes --wiki <alias> --no-interactive
```

清理 processed Inbox 不会改变 Knowledge、索引或查询结果。收到 `INDEX_NOT_FOUND` 或 `INDEX_STALE` 时，在 Vault 内显式运行 `index rebuild` 或 `index update`。

Obsidian、Bases、Properties 和 wikilink 都是可选展示增强，不是运行依赖。
