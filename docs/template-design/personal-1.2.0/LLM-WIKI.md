---
title: LLM Wiki 使用入口
aliases:
  - 知识库首页
tags:
  - llm-wiki
cssclasses:
  - llm-wiki-home
---

# LLM Wiki 使用入口

这个 Vault 将原始证据、可信知识、AI 派生内容和搜索索引严格分开。`knowledge/` 中已发布 Markdown 是唯一最终事实源，SQLite 只选择检索候选。

## 目录说明

| 目录 | 用途 | 是否可信事实 |
|---|---|---|
| `raw/` | 原始文件、摘录、会议记录和人工记录 | 原始证据，不是整理结论 |
| `knowledge/` | 经过 `publish apply` 审批的知识 | 是，唯一权威层 |
| `llm-wiki/` | 从 knowledge 构建的 AI 视图 | 否，可删除重建 |
| `templates/` | raw 与 knowledge 草稿模板 | 否 |
| `rules/` | 采集、发布、引用和质量规则 | 否 |
| `views/` | Obsidian Bases 视图 | 否，只读取 Properties |
| `.llm-wiki/` | 索引、变更集和运行状态 | 否 |

## 第一次使用

1. 在 Obsidian 中启用核心 **Templates** 与 **Bases** 插件。
2. 将 Templates 的模板目录设置为 `templates`。
3. 不修改 `.obsidian/` 也能使用 CLI；上述设置只影响当前 Obsidian Vault。
4. 运行 `llm-wiki doctor --json --no-interactive` 检查实例。

## 记录一份材料

```bash
llm-wiki raw add ./article.md --wiki <alias>
llm-wiki raw add - --name meeting.md --origin meeting --wiki <alias>
```

网页、论文、书籍或会议材料应填写 `source_url`、`authors`、`source_date`、`retrieved_at` 等 Properties。详见 [[rules/capture|采集规则]]。

## 发布一条知识

```bash
llm-wiki query "准备整理的问题" --wiki <alias> --json --no-interactive
llm-wiki template create claim --kind knowledge --title "明确的结论" --output ./draft.md
llm-wiki publish propose --source <raw-id> --file ./draft.md --wiki <alias>
llm-wiki publish diff <change-id> --wiki <alias>
llm-wiki publish apply <change-id> --wiki <alias>
llm-wiki build --wiki <alias>
```

草稿可以放在 Vault 中任意非受管工作目录或 Vault 外部，但不能直接放入 `knowledge/`、`raw/` 或 `llm-wiki/`。事实陈述应使用 [[rules/citations|raw ID 命名脚注]]。

`query` 根据 SQLite 返回的候选 ID 和位置打开对应 `knowledge/` 文件，最终 evidence 的正文、Properties 与来源均来自发布 Markdown。收到 `INDEX_STALE` 时显式运行 `llm-wiki index update`，不能继续使用 SQLite 缓存。

## 选择知识类型

| 需求 | 模板 |
|---|---|
| 保存一个原子、可验证的结论 | `claim` |
| 解释概念、原理、边界或原因 | `concept` |
| 完成一个具体任务 | `guide` |
| 通过完整练习学习一项能力 | `tutorial` |
| 提供稳定、结构化的查询资料 | `reference` |
| 记录选择、理由、备选项和后果 | `decision` |
| 保存某个日期的项目事实快照 | `project` |

详细边界见 [[rules/types|知识类型规则]]。

## 浏览知识

### 当前知识

![[views/knowledge.base#Current knowledge]]

### 待复查或非当前知识

![[views/review.base#Needs review]]

### 最近采集的 raw

![[views/raw.base#Raw evidence]]

## 生命周期

`lifecycle` 表示知识当前是否还能采用：`current`、`disputed`、`superseded`、`retracted`。它不替代系统 `status: published`。过期、被替代和撤回知识仍保留文件与来源，但查询不得把它们当作当前结论。详见 [[rules/lifecycle|知识生命周期规则]]。

## 模板升级

```bash
llm-wiki template upgrade --plan --wiki <alias>
llm-wiki template upgrade --apply --wiki <alias>
```

升级使用安装基线、用户文件和新模板进行三方判断，不会静默覆盖用户修改。
