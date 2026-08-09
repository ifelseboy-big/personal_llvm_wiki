# LLM Wiki Tool — 技术设计与实现交接

## 1. 目标

在当前仓库实现一个可独立安装的 `llm-wiki` CLI。用户安装工具后，可以在任意目录初始化一个基于 Markdown、兼容 Obsidian 的个人知识库，并允许 Codex 或其他 AI CLI 通过命令行完成知识存入、整理、发布、构建和查询。

当前仓库是工具工程，不是知识库实例。工具源码、用户知识和 AI Skill 必须保持分离。

## 2. 已确认的产品边界

- 面向个人知识管理，但设计不能阻塞后续扩展。
- 不使用 MCP，不依赖常驻服务。
- CLI 是唯一稳定的操作接口和可信层写入通道，负责机械不变量，不负责解释知识语义。
- 所有新知识先进入 `raw/`。
- `knowledge/` 保存经过整理和发布的最终可信知识，是唯一事实来源。
- `llm-wiki/` 保存面向 AI 的派生知识，只能由 `knowledge/` 生成，可以随时重建。
- 人看的可信知识和 AI 使用的派生知识是两个独立层。
- 支持内置知识库模板及知识库内的自定义模板。
- 支持可选安装 Skill；Skill 只负责定位目标知识库、读取其 `AGENTS.md` 并调用 CLI，不复制治理规则。
- `AGENTS.md` 是当前知识库的语义治理入口和规则路由，不保存具体知识事实或元数据。
- SQLite 只做索引和运行状态，不成为事实来源。

## 3. 三层结构

### 3.1 工具工程

当前仓库：

```text
llvm_wiki_tool/
├── src/                       # CLI 与核心逻辑
├── resources/
│   ├── vault-templates/       # 内置知识库模板
│   │   └── personal/
│   └── skills/
│       └── llm-wiki/          # 可选安装的 Skill
├── tests/
└── ...
```

这里不能出现用户知识数据。具体源码目录可根据最终技术栈调整，但 `resources` 中的初始化模板和 Skill 必须随工具一起发布。

### 3.2 已安装工具

安装后提供全局命令：

```bash
llm-wiki
```

工具升级不能静默改写用户的 `raw/`、`knowledge/` 或 `llm-wiki/` 内容。

### 3.3 知识库实例

用户可以在任意位置初始化：

```bash
llm-wiki init ~/Knowledge/personal
```

初始化结果：

```text
personal/
├── AGENTS.md
├── llm-wiki.toml
├── raw/
├── knowledge/
├── llm-wiki/
├── templates/
└── .llm-wiki/
    ├── index.sqlite
    ├── changes/
    ├── locks/
    ├── logs/
    └── cache/
```

目录职责：

| 目录或文件 | 职责 |
|---|---|
| `raw/` | 原始输入、附件及未经确认的材料 |
| `knowledge/` | 已发布的可信知识，唯一事实来源 |
| `llm-wiki/` | 从可信知识生成的 AI 派生内容，禁止反向覆盖 `knowledge/` |
| `templates/` | 当前知识库的自定义模板或内置模板覆盖 |
| `AGENTS.md` | AI 维护当前知识库时必须遵守的规则 |
| `llm-wiki.toml` | 实例标识、路径、模板版本、Schema 版本和行为配置 |
| `.llm-wiki/` | 索引、变更集、锁、日志和缓存等运行数据 |

## 4. 元数据原则

知识的规范元数据写入 Markdown YAML frontmatter，不建立第二份中心化 metadata 文件。

`raw/` 至少包含：

```yaml
id: raw_xxx
type: note
origin: manual
captured_at: 2026-08-08T10:00:00+08:00
content_hash: sha256:...
status: raw
```

`knowledge/` 至少包含：

```yaml
id: know_xxx
type: concept
status: published
sources:
  - raw_xxx
published_at: 2026-08-08T11:00:00+08:00
updated_at: 2026-08-08T11:00:00+08:00
```

`llm-wiki/` 至少记录：

```yaml
derived_from:
  - know_xxx
compiler_version: ...
generated_at: ...
```

非 Markdown 原始文件采用“原文件 + 同名来源说明 Markdown”的方式，例如：

```text
raw/paper.pdf
raw/paper.source.md
```

## 5. 知识库定位规则

CLI 按以下优先级解析目标知识库：

1. 命令显式传入的 `--wiki <name|path>`。
2. 从当前目录向上寻找最近的 `llm-wiki.toml`。
3. 用户级配置中的默认知识库。

用户级配置建议位于：

```text
~/.config/llm-wiki/config.toml
```

这里只保存实例名称、路径和默认实例，不保存知识元数据。例如：

```toml
default = "personal"

[wikis.personal]
path = "/Users/example/Knowledge/personal"
```

这样 AI CLI 即使运行在其他代码工程中，也能查询默认或指定知识库。

## 6. 初始化行为

基础命令：

```bash
llm-wiki init <path>
```

交互初始化至少处理：

1. 实例名称。
2. 知识库模板，首个内置模板为 `personal`。
3. 是否注册并设为默认知识库。
4. 检测本机支持的 AI CLI。
5. 是否安装 `llm-wiki` Skill，并显示准确安装目标后确认。

初始化必须满足：

- 可以初始化空目录，也可以在已有 Obsidian Vault 根目录中初始化。
- 不移动已有文件。
- 不覆盖已有 `AGENTS.md`、`llm-wiki.toml` 或同名内容。
- 有冲突时显示计划或差异并要求用户明确处理。
- 支持非交互模式；缺少必要选择时返回结构化错误，不能等待输入。
- `llm-wiki.toml` 记录 `schema_version`、实例 ID 和模板版本，为后续迁移提供依据。
- 重复执行时结果可预测，不重复注册，不破坏已有数据。

## 7. CLI 功能面

### 7.1 实例与环境

```bash
llm-wiki init <path>
llm-wiki locate
llm-wiki status
llm-wiki doctor
llm-wiki migrate --plan
llm-wiki migrate --apply
```

### 7.2 原始知识

```bash
llm-wiki raw add <file|directory|->
llm-wiki raw list
llm-wiki raw show <raw-id>
```

所有“存入知识”操作都必须先落到 `raw/`。

### 7.3 整理与发布

```bash
llm-wiki publish propose --source <raw-id> --file <draft.md>
llm-wiki publish diff <change-id>
llm-wiki publish apply <change-id>
llm-wiki publish reject <change-id>
```

AI 可以生成发布提案，但不能绕过变更集直接把未经确认的内容写成可信知识。变更集保存到 `.llm-wiki/changes/`。

### 7.4 AI 层构建

```bash
llm-wiki build
llm-wiki build --full
llm-wiki build status
```

构建只允许从 `knowledge/` 读取并写入 `llm-wiki/`。生成过程应可重复，不能反向修改可信知识。

### 7.5 查询与溯源

```bash
llm-wiki query "问题"
llm-wiki query "问题" --wiki personal --json
llm-wiki show <knowledge-id>
llm-wiki trace <knowledge-id>
```

`query` 先使用 SQLite 检索候选 knowledge ID、路径、位置和分数，再打开并验证对应 `knowledge/` Markdown，从发布文档返回正文、元数据与来源。SQLite 中的 chunk 和 metadata 不能直接作为最终证据。CLI 不生成最终自然语言答案，归纳由 Codex 或其他 AI 完成。

### 7.6 索引

```bash
llm-wiki index status
llm-wiki index update
llm-wiki index rebuild
```

SQLite 索引必须可以从文件系统完整重建。删除 `index.sqlite` 不能造成知识丢失。

### 7.7 模板

```bash
llm-wiki template list
llm-wiki template show <name>
```

工具内置模板随安装包发布；初始化时只复制选中模板需要的实例文件，不把整套内置模板重复复制到每个知识库。

### 7.8 Skill 管理

```bash
llm-wiki skill status
llm-wiki skill install <client>
llm-wiki skill update <client>
llm-wiki skill uninstall <client>
```

初始化中的 Skill 安装询问本质上调用同一套 Skill 管理能力。

## 8. CLI 与目录的读写关系

| 操作 | 主要读取 | 主要写入 |
|---|---|---|
| `init` | 工具内置模板 | 新知识库结构 |
| `raw add` | 文件、目录或 stdin | `raw/`、索引 |
| `publish propose` | `raw/`、`knowledge/`、模板 | `.llm-wiki/changes/` |
| `publish apply` | 已确认变更集 | `knowledge/`、索引 |
| `build` | `knowledge/` | `llm-wiki/`、索引 |
| `query` | 索引及对应知识文件 | 不修改可信知识 |
| `index rebuild` | 三个知识层 | `.llm-wiki/index.sqlite` |
| `skill install` | 工具内置 Skill | 对应 AI CLI 的 Skill 目录 |

## 9. AGENTS、Skill 与 CLI 的边界

| 组件 | 作用 |
|---|---|
| CLI | 执行命令，强制来源、哈希、事务、路径和索引等机械不变量 |
| `AGENTS.md` | 当前知识库唯一语义治理入口，负责规则路由和内容边界 |
| Skill | 定位目标知识库、读取其 `AGENTS.md` 并正确调用 CLI |

Skill 中不得包含知识库绝对路径，也不得复制 Vault 的知识治理规则或 CLI 实现逻辑。Skill 至少说明：

- 每次工作流先 `locate --json` 并读取目标根目录的 `AGENTS.md`。
- 按 `AGENTS.md` 路由读取本次操作需要的 rules/templates。
- `knowledge/` 是唯一最终事实源，SQLite 只选择候选。
- 不直接修改派生层或绕过发布流程。
- 如何处理 CLI 的结构化错误和引用信息。

Codex Skill 默认安装位置为用户的 Codex Skills 目录；实际安装前必须展示解析后的目标路径。其他 AI CLI 使用独立安装适配器。

## 10. 面向 AI 的命令契约

所有需要被 AI 调用的命令必须支持：

- `--json`：稳定、带版本的结构化输出。
- `--no-interactive`：禁止提示，缺参时失败。
- 明确的退出码和机器可识别错误码。
- stdout 只输出结果，诊断信息写入 stderr。
- 显式返回知识库实例 ID、命令版本和受影响文件。
- 写操作支持预览或返回变更摘要。

Skill 只依赖这些稳定契约，不解析面向人的终端文案。

## 11. 下一阶段：先完成技术方案

技术方案需要落到仓库文档，并明确以下内容后再确定源码结构：

1. 实现语言、CLI 框架、构建方式和跨平台分发方式。
2. 命令模型、全局参数、退出码和 JSON Schema。
3. `llm-wiki.toml` 与用户级配置的完整 Schema。
4. Markdown frontmatter Schema、ID 生成和内容哈希规则。
5. SQLite 表结构、全文检索方案、增量更新和重建算法。
6. `raw → change set → knowledge → llm-wiki` 的状态机和原子写入策略。
7. 文件锁、并发调用、中断恢复和错误处理。
8. 内置模板格式、模板版本和升级策略。
9. Skill 打包、客户端检测、安装、升级和卸载机制。
10. 安全边界，包括路径穿越、符号链接、敏感文件、外部命令和日志脱敏。
11. 测试分层、兼容性矩阵和端到端验收用例。

技术选型需要比较后给出明确结论，不能因为追求快速实现而牺牲全局安装、跨平台、索引可靠性或 AI 调用稳定性。

## 12. 实现顺序

技术方案确认后按依赖关系实现：

1. 配置、实例发现、文件模型和公共错误协议。
2. `init`、实例注册、冲突检测和 `doctor`。
3. `raw add`、frontmatter、内容哈希和 SQLite 基础索引。
4. 查询、展示和来源追踪。
5. 发布变更集和可信知识写入。
6. AI 派生层构建。
7. 模板系统。
8. Codex Skill 及 Skill 管理命令。
9. 迁移、恢复、跨平台打包和完整端到端测试。

## 13. 首轮验收场景

至少验证以下完整流程：

```bash
llm-wiki init /tmp/personal-wiki
llm-wiki raw add ./fixtures/article.md --wiki /tmp/personal-wiki
llm-wiki publish propose --source <raw-id> --file ./fixtures/draft.md --wiki /tmp/personal-wiki
llm-wiki publish apply <change-id> --wiki /tmp/personal-wiki
llm-wiki build --wiki /tmp/personal-wiki
llm-wiki query "文章的核心结论" --wiki /tmp/personal-wiki --json
llm-wiki trace <knowledge-id> --wiki /tmp/personal-wiki --json
llm-wiki index rebuild --wiki /tmp/personal-wiki
```

验收标准：

- 工具源码和知识库实例完全分离。
- 初始化结构与本文一致。
- 所有可信知识都可追溯到 `raw` 来源。
- 删除 SQLite 索引后能够重建并得到等价查询结果。
- `llm-wiki/` 能够从 `knowledge/` 完整重建。
- AI 可只依赖 CLI 的 JSON 输出完成存入、查询和发布提案。
- 未安装 Skill 时 CLI 仍可完整工作；安装 Skill 后不改变知识库格式。
- 全程不需要 MCP 或常驻进程。

## 14. 当前未决策项

以下内容应在技术方案阶段决策，不应在实现中隐式决定：

- 实现语言及包管理方式。
- SQLite 使用 FTS5、向量扩展还是组合索引，以及首版是否包含向量检索。
- AI 派生内容的具体文件粒度和生成格式。
- 第一批内置知识库模板清单。
- 除 Codex 外首批支持哪些 AI CLI。
- 发布审批是纯 CLI 交互、Obsidian 内操作，还是两者都支持。
