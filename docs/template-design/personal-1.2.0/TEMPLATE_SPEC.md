# personal 1.2.0 模板执行契约

本文定义设计稿落地时 CLI、模板和校验器必须共同遵守的行为。

## 1. 模板变量

内容模板只允许以下 Obsidian 核心变量：

| 变量 | 含义 |
|---|---|
| `{{title}}` | 草稿标题或当前 Obsidian 文件名 |
| `{{date}}` | 创建日期，格式 `YYYY-MM-DD` |
| `{{time}}` | 创建时间，格式 `HH:mm` |

写作提示统一写成：

```markdown
%% llm-wiki:prompt 用一句话描述要填写的内容；完成后删除本注释。 %%
```

Agent 在创建提案前必须处理：

- 未替换的 `{{...}}`；
- 残留的 `llm-wiki:prompt` 注释；
- `title` 与正文第一个 H1 不一致；
- 模板要求的章节为空；
- 空白或仍为提示语的 `description`。

这些属于目标 Vault 的写作治理，不由 CLI 解析 `AGENTS.md` 后执行。CLI 可以提供通用模板 lint，但 `publish` 的硬约束只负责来源、哈希、事务、路径和结构完整性。

## 2. 建议命令

```text
llm-wiki template create <name> \
  --kind <raw|knowledge> \
  --title <title> \
  --output <draft.md> \
  [--set <property=value>]... \
  [--related <knowledge-id>]...
```

行为要求：

1. 输出只能写入用户明确指定的文件，不得写入 `knowledge/` 或 `llm-wiki/`。
2. 默认拒绝覆盖已有文件；显式覆盖仍需确认并支持 `--dry-run`。
3. `--related` 根据 knowledge ID 解析真实相对路径，生成带显示标题的 Wikilink。
4. JSON 输出返回模板版本、输出路径、未填写字段和下一步 `publish propose` 命令提示。
5. Obsidian 用户可以直接把 `templates/` 配置为核心 Templates 插件目录，不需要 CLI 渲染。

## 3. Knowledge 公共用户属性

```yaml
type: claim
title: "{{title}}"
description: ""
lifecycle: current
valid_from:
valid_until:
review_after:
aliases: []
tags: []
cssclasses:
  - llm-wiki-knowledge
related: []
supersedes: []
superseded_by: []
```

| 属性 | 类型 | 规则 |
|---|---|---|
| `type` | text | 七类知识之一；创建后允许通过发布更新，但不自动移动文件 |
| `title` | text | 具体、独立可理解；必须与第一个 H1 一致 |
| `description` | text | 一句话摘要，不引入正文和来源没有支持的新事实 |
| `lifecycle` | text | `current`、`disputed`、`superseded`、`retracted` |
| `valid_from` | date | 可选；事实开始适用的日期 |
| `valid_until` | date | 可选；超过后不能作为当前事实使用 |
| `review_after` | date | 可选；到期后 `doctor` 报告待复查 |
| `aliases` | list[text] | Obsidian 默认别名 |
| `tags` | list[tag] | 少量主题标签，不复制 `type` 和 `lifecycle` |
| `cssclasses` | list[text] | Obsidian 样式类 |
| `related` | list[link] | CLI 根据 knowledge ID 生成并验证的真实 Wikilink |
| `supersedes` | list[link] | 当前知识替代的旧知识 |
| `superseded_by` | list[link] | 替代当前知识的新知识 |

空日期在发布后省略。空列表表示明确清空。更新草稿省略属性表示保留现值，写 `null` 表示删除。

## 4. 系统属性

以下属性不得由模板、Obsidian 或草稿覆盖：

```text
schema_version, id, status, sources,
captured_at, published_at, updated_at,
content_hash, media_type, original_name, asset,
derived_from, compiler, compiler_version,
build_fingerprint, generated_at
```

`id` 只允许在更新草稿中用来定位既有 knowledge；最终值仍由 CLI 决定。

## 5. Lifecycle 行为

| 值 | 含义 | 默认查询行为 |
|---|---|---|
| `current` | 当前可采用 | 正常返回 |
| `disputed` | 来源或结论存在未解决分歧 | 返回并强制警告 |
| `superseded` | 已被新知识替代 | 默认排除，可显式包含 |
| `retracted` | 已确认不应继续使用 | 默认排除，可显式包含并强警告 |

`valid_until` 已过或 `review_after` 到期时，查询结果必须携带 warning；SQLite 只执行过滤，判断依据仍是 Markdown Properties。

## 6. 关联格式

发布后的关联必须包含真实路径和显示标题：

```yaml
related:
  - "[[knowledge/concept/llvm-ir--know_01abc...|LLVM IR]]"
```

发布校验要求：

- 目标位于同一 Vault 的 `knowledge/`；
- 文件名中的 knowledge ID 与目标 frontmatter ID 一致；
- 不允许只写 `[[标题]]`；
- 不允许链接 `llm-wiki/` 派生文件作为知识关系。

## 7. 结论级引用

正文使用 Obsidian 命名脚注，标签格式为 `<raw-id>` 或 `<raw-id>-<序号>`：

```markdown
LLVM IR 为多语言前端提供共同的优化边界。[^raw_01abc-1]

[^raw_01abc-1]: locator: 第 3 节，第 2 段
```

校验器必须保证：

- 脚注中的 raw ID 存在于系统 `sources`；
- 每个事实性关键结论附近至少有一个来源脚注；
- 冲突观点分别引用各自来源；
- 脚注只提供定位信息，不复制来源哈希；哈希仍以 `sources` 为准。

## 8. 模板专属属性

| 模板 | 属性 |
|---|---|
| `claim` | 无额外必填属性 |
| `concept` | 无额外必填属性 |
| `guide` | `applies_to`、`last_verified` |
| `tutorial` | `learning_outcome`、`estimated_time`、`applies_to`、`last_verified` |
| `reference` | `reference_version`、`applies_to`、`last_verified` |
| `decision` | `decision_state`、`decided_at`、`decision_makers`、`consulted`、`informed` |
| `project` | `project_state`、`as_of`、`owner`、`started_at`、`target_date` |

所有日期使用 `YYYY-MM-DD`，日期时间使用 RFC 3339。相同属性名在整个 Vault 中必须保持相同类型。

## 9. 查询事实边界

SQLite 只返回候选 knowledge ID、路径、匹配行号、chunk hash、文件 hash 和排序分数。CLI 必须根据候选重新读取 `knowledge/` Markdown，校验 ID、文件 hash、正文 hash 和 raw 来源，再从发布文档返回正文、metadata 与 sources。

`query` 不自动更新索引。索引与发布文件不一致时返回 `INDEX_STALE`，由调用方显式执行 `llm-wiki index update`；不得回退使用 SQLite 缓存内容。
