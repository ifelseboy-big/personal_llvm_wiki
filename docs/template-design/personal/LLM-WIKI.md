---
title: LLM Wiki 使用入口
aliases:
  - 知识库首页
tags:
  - llm-wiki
---

# LLM Wiki 使用入口

日常只需要 Add 和 Query。Vault Agent 根据 `content-pack.json` 选择领域、知识结构和 Workflow；CLI 负责路径安全、哈希、冻结批准、事务恢复和可信回读。

## 首次使用

```bash
llm-wiki status --wiki <vault-root> --json --no-interactive
llm-wiki doctor --wiki <vault-root> --json --no-interactive
```

两条命令应返回当前内容包 identity，且 doctor 为 healthy。不要手工初始化 `knowledge/`，也不要把模板中的 prompt 注释当成正式内容。

## Add：先可靠保存

告诉 Agent“记住/保存/收集这段内容”。Capture 会逐字节保存原始输入到 pending Inbox，不会查询、整理或直接发布 Knowledge。

```bash
llm-wiki inbox add ./article.pdf --note-file ./article-note.md --wiki <vault-root> --json --no-interactive
llm-wiki inbox list --status pending --wiki <vault-root> --json --no-interactive
```

文本从 stdin 输入时必须提供 `--name`。多文件使用 batch manifest，为每个 payload 配置独立 note；任一输入预检失败时零写入。

## Query：只回答已发布事实

```bash
llm-wiki query "问题" --wiki <vault-root> --json --no-interactive
llm-wiki show <knowledge-id> --wiki <vault-root> --json --no-interactive
```

Query 只使用 CLI 回读验证后的 Knowledge。没有足够证据时会明确说明，不读取 Inbox、草稿或 SQLite 行兜底。

## 整理与发布

要求 Agent 整理指定 Inbox 时，它会读取原始 payload、查重、独立选择 category/type，在 `knowledge/` 外生成草稿并准备 N:M Promotion。

发布分为两个明确阶段：

1. Agent 运行 plan/diff，展示 promotion ID、plan hash、目标、consume 决策和完整 diff，然后停止。
2. 你针对该冻结计划明确批准后，Agent 才能 apply。计划发生任何变化都必须重新展示和批准。

## 维护

“检查知识库”是只读操作；“处理这些问题”才允许先采集维护依据、生成草稿和 Promotion。失效、替代、合并和更正都保留历史并经过同样的冻结批准，不直接修改或删除 `knowledge/`。

## 目录边界

| 目录 | 用途 | 事实属性 |
| --- | --- | --- |
| `inbox/` | 原始 payload 与初步整理 | 临时，不用于 Query 回答 |
| `knowledge/` | 明确批准的自包含知识 | 唯一可信事实源 |
| `.llm-wiki/promotions/` | 冻结计划、diff 与状态 | 审阅和恢复数据 |
| `.llm-wiki/index.sqlite` | 候选索引 | 可重建缓存 |
| `content-pack.json`、`rules/`、`templates/`、`workflows/` | 声明式治理与 Agent 资源 | 非用户事实 |

Obsidian、Bases、Properties 和 wikilink 都是可选展示增强，不是正确性或运行依赖。
