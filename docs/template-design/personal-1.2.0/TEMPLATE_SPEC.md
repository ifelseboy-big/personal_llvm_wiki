# personal 1.2.0 模板执行契约

本文定义设计稿落地时 CLI、模板和校验器必须共同遵守的行为。

## 1. 模板变量

内容模板只允许以下 Obsidian 核心变量：

| 变量 | 含义 |
|---|---|
| `{{title}}` | 草稿标题或当前 Obsidian 文件名 |
| `{{date:YYYY-MM-DD}}` | 创建日期；显式格式不受 Obsidian Templates 全局设置影响 |
| `{{time:HH:mm}}` | 创建时间；显式格式不受 Obsidian Templates 全局设置影响 |

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

CLI 不解析 `AGENTS.md` 的自然语言语义，但 personal 1.2.0 的 `publish` 会执行通用、确定性的结构检查：模板残留、title/H1、公共字段类型、raw-ID 脚注完整性和知识链接可解析性。Agent 与审批者仍负责判断关键事实是否真的被附近引用支持、内容是否重复、推断是否越界等语义质量。

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
build_fingerprint, generated_at, governance_version
```

`id` 只允许在更新草稿中用来定位既有 knowledge；最终值仍由 CLI 决定。

## 5. Lifecycle 行为

| 值 | 含义 | 默认查询行为 |
|---|---|---|
| `current` | 当前可采用 | 正常返回 |
| `disputed` | 来源或结论存在未解决分歧 | 返回并强制警告 |
| `superseded` | 已被新知识替代 | 默认排除，可显式包含 |
| `retracted` | 已确认不应继续使用 | 默认排除，可显式包含并强警告 |

`valid_until` 已过、`valid_from` 尚未到达或 `review_after` 到期时，查询结果必须携带 warning。升级到 1.2.0 时，CLI 在根目录 `.llm-wiki-governance.json` 记录既有 knowledge 的完整文件哈希；只有与该基线完全一致且没有 `governance_version` 的文档按 legacy 读取。legacy 文档一律作为 `current` 候选并给迁移 warning，不解释升级前可能同名的 lifecycle 和日期自定义属性；重新发布后写入受保护的 `governance_version: personal-1.2` 并执行严格检查。该基线必须随知识库备份或提交。SQLite 只执行候选过滤，最终判断依据仍是 Markdown Properties。

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

CLI 机械校验器必须保证：

- 脚注中的 raw ID 存在于系统 `sources`；
- 引用标签、定义和 locator 完整，且 raw ID 位于系统 `sources`；
- 冲突观点分别引用各自来源；
- 脚注只提供定位信息，不复制来源哈希；哈希仍以 `sources` 为准。

“每个事实性关键结论附近都有足够证据”涉及语义判断，必须由 Agent 与审批者按 `rules/citations.md` 检查，CLI 不声称仅靠正则或 Markdown 结构即可证明。

## 8. 模板专属属性

| 模板 | 属性 |
|---|---|
| `claim` | 无额外必填属性 |
| `concept` | 无额外必填属性 |
| `guide` | `applies_to`、`last_verified` |
| `tutorial` | `learning_outcome`、`estimated_time`、`applies_to`、`last_verified` |
| `reference` | `reference_version`、`applies_to`、`last_verified` |
| `decision` | `decision_state`（`proposed`、`accepted`、`deprecated`）、`decided_at`、`decision_makers`、`consulted`、`informed` |
| `project` | `project_state`（`active`、`paused`、`completed`、`cancelled`）、`as_of`、`owner`、`started_at`、`target_date` |

所有日期使用 `YYYY-MM-DD`，日期时间使用 RFC 3339。相同属性名在整个 Vault 中必须保持相同类型。

## 9. 查询事实边界

SQLite 只返回候选 knowledge ID、路径、匹配行号、chunk hash、文件 hash 和排序分数。CLI 必须根据候选重新读取 `knowledge/` Markdown，校验 ID、文件 hash 和正文 hash，再从发布文档返回正文、metadata 与发布时绑定的 sources。raw 当前字节是否仍与发布时哈希一致由 `trace` 和 `doctor` 报告；raw 后续漂移不会悄悄改写已经发布的事实快照。

`query` 不自动更新索引。索引与发布文件不一致时返回 `INDEX_STALE`，由调用方显式执行 `llm-wiki index update`；不得回退使用 SQLite 缓存内容。
