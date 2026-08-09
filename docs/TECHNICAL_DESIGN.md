# llm-wiki 技术设计

状态：已确认，作为实现与验收的规范基线。

## 1. 不可破坏的不变量

1. `knowledge/` 中通过发布流程产生的 Markdown 是最终可信知识和唯一事实依据。
2. `raw/` 保存原始证据。可信知识必须记录来源 ID 与发布时的来源内容哈希。
3. `llm-wiki/` 是 `knowledge/` 的确定性派生视图，禁止反向写入可信层。
4. `.llm-wiki/index.sqlite` 只保存可重建索引和运行状态。删除数据库不得导致知识、元数据或溯源关系丢失。
5. `AGENTS.md` 和 `rules/` 是语义治理依据；CLI 是唯一稳定写入接口，只强制来源、哈希、事务、路径与索引等机械不变量。
6. 不依赖 MCP、常驻服务、外部数据库、解释器或网络服务。

## 2. 技术选型

### 2.1 实现与构建

- 语言：Go 1.25 兼容语法，CI 使用当前稳定 Go。
- CLI：Cobra，负责子命令、全局参数、帮助和参数验证。
- SQLite：纯 Go SQLite 驱动，构建不依赖 CGO；启用 FTS5。
- 配置：TOML；规范文档使用 JSON Schema 表达约束。
- Frontmatter：YAML；正文保持 UTF-8 Markdown。
- Markdown：CommonMark/GFM 解析，额外识别 Obsidian wikilink，但不改写用户正文。
- 内置资源：使用 `go:embed` 编译进二进制。
- 锁：跨平台 advisory file lock；所有可信层写操作持有实例独占锁。

Go 与 Rust 都满足单文件分发。选 Go 是因为当前构建环境可直接验证，且纯 Go SQLite 可以同时满足 Apple Silicon、FTS5 和无系统依赖要求。不得改为依赖本机 `sqlite3` 命令或动态 SQLite 库。

### 2.2 分发

唯一发布目标：

| 系统 | 架构 |
|---|---|
| macOS | Apple Silicon（arm64） |

产物包含单个 `llm-wiki` 可执行文件、校验和与 SBOM。发布渠道为 GitHub Release 和 Homebrew tap；`go install` 只作为开发者安装方式。版本通过构建参数注入，开发构建为 `dev`。其他平台不进入发布和验收范围。

## 3. 工程结构

```text
cmd/llm-wiki/              程序入口
internal/
  app/                     命令装配、输出协议
  config/                  实例配置、用户注册表、定位
  document/                frontmatter、ID、哈希、验证
  vault/                   路径、安全检查、锁、事务恢复
  raw/                     原始资料导入
  publish/                 变更集、diff、apply/reject
  index/                   SQLite、FTS5、增量更新、重建
  build/                   AI 派生层编译
  templates/               模板发现、渲染、版本
  skill/                   AI CLI 适配器
resources/
  vault-templates/personal/
  skills/llm-wiki/
schemas/                   稳定 JSON Schema
tests/e2e/                 安装后二进制端到端测试
```

包依赖方向为 `app -> use case -> document/config/vault`。`document`、`config` 不得依赖 SQLite；索引模块只能读取它们的解析结果。

## 4. 知识库配置

### 4.1 `llm-wiki.toml`

```toml
schema_version = 1
instance_id = "wiki_01..."
name = "personal"
created_at = "2026-08-08T10:00:00+08:00"

[template]
name = "personal"
version = "1.1.1"

[paths]
raw = "raw"
knowledge = "knowledge"
derived = "llm-wiki"
templates = "templates"
rules = "rules"
runtime = ".llm-wiki"

[publish]
require_sources = true

[index]
chunk_max_chars = 1800
chunk_overlap_chars = 180
chinese_tokenizer = "unicode-han"

[security]
follow_symlinks = false
max_input_bytes = 52428800
block_sensitive_files = true
```

路径必须是相对知识库根目录的规范路径，不能为空，不能包含 `..`，不能互相重叠，不能指向符号链接。运行时解析后的真实路径必须仍位于知识库根目录。

未知字段保留以便前向兼容；未知的更高 `schema_version` 拒绝写入，只允许安全的只读诊断。

### 4.2 用户注册表

macOS 默认位于 `$XDG_CONFIG_HOME/llm-wiki/config.toml`，未设置时为 `~/.config/llm-wiki/config.toml`。`LLM_WIKI_CONFIG` 可显式覆盖。

```toml
schema_version = 1
default = "personal"

[wikis.personal]
instance_id = "wiki_01..."
path = "/absolute/path/to/personal"
```

注册表不保存知识元数据。别名和实例 ID 均唯一；重复注册同一实例是幂等操作。

## 5. 文档模型

### 5.1 ID

使用 Crockford Base32 ULID，并增加固定前缀：`wiki_`、`raw_`、`know_`、`chg_`、`drv_`、`op_`。ID 创建后不可修改，文件移动不改变 ID。

### 5.2 哈希

- Markdown：去除 UTF-8 BOM，将 CRLF/CR 归一为 LF，对 frontmatter 结束分隔线之后的正文原样计算 SHA-256。
- 非 Markdown：对原始字节计算 SHA-256。
- 表示形式固定为小写 `sha256:<64 hex>`。
- 不 trim 行尾空格、不补末尾换行，避免掩盖真实变更。

### 5.3 Raw frontmatter

```yaml
schema_version: 1
id: raw_01...
type: note
title: 示例
status: raw
origin: manual
captured_at: 2026-08-08T10:00:00+08:00
content_hash: sha256:...
media_type: text/markdown
original_name: example.md
description: 原始材料说明
tags: []
aliases: []
cssclasses: []
```

非 Markdown 文件放入 `raw/YYYY/MM/<raw-id>/`，同目录创建 `<stem>.source.md`。sidecar 是该 raw ID 的规范元数据，原文件是其内容载荷。

### 5.4 Knowledge frontmatter

```yaml
schema_version: 1
id: know_01...
type: concept
title: 示例概念
description: 一句话摘要
status: published
sources:
  - id: raw_01...
    content_hash: sha256:...
published_at: 2026-08-08T11:00:00+08:00
updated_at: 2026-08-08T11:00:00+08:00
content_hash: sha256:...
tags: []
aliases: []
cssclasses: []
related: []
```

`sources` 至少一个且全部可解析。缺失来源、来源哈希变化或正文哈希不匹配均使文档失去可发布状态，`doctor` 和 `index update` 必须报告，不得静默修复。

frontmatter 同时兼容 Obsidian Properties。系统属性由 CLI 重建，草稿值不能覆盖；用户属性允许扩展并在发布时无损保留。更新时省略用户属性表示保持原值，同名值表示覆盖，`null` 表示删除；`tags: []` 和 `aliases: []` 表示明确清空。Obsidian 不支持在 Properties 界面编辑嵌套对象，因此 `sources` 只允许由 CLI 管理，不能为适配界面而拍平或迁移到 SQLite。

### 5.5 Derived frontmatter

```yaml
schema_version: 1
id: drv_01...
derived_from:
  id: know_01...
  content_hash: sha256:...
compiler: standard
compiler_version: 2
build_fingerprint: sha256:...
generated_at: 2026-08-08T11:00:00+08:00
```

`generated_at` 是运行信息，不参与 `build_fingerprint`。指纹包含 knowledge 完整文件哈希、确定性编译器版本和构建配置，因此仅修改用户 Properties 也会使派生层变为 stale。派生正文只由可信正文生成，用户 Properties 确定性复制到派生 frontmatter。

## 6. 文件布局

```text
raw/YYYY/MM/<raw-id>/...
knowledge/<type>/<slug>--<knowledge-id>.md
llm-wiki/documents/<knowledge-id>.md
llm-wiki/manifest.json
templates/raw/*.md
templates/knowledge/*.md
rules/*.md
.llm-wiki/
  index.sqlite
  changes/<change-id>/
  transactions/<operation-id>/
  locks/write.lock
  logs/
  cache/
```

Slug 只用于可读路径，所有关系依赖稳定 ID。扫描器忽略不在三个管理根目录中的已有 Obsidian 文档；它们必须通过 `raw add` 显式导入。

## 7. 命令与全局协议

全局参数：

```text
--wiki <alias|path>
--json
--no-interactive
--dry-run
--quiet
--verbose
--color <auto|always|never>
```

`--json` 时 stdout 只允许一个 JSON 对象，日志和诊断进入 stderr；自动禁用颜色和交互。`--no-interactive` 缺少选择时返回错误，不能等待 stdin，但显式 `raw add -` 除外。

成功响应：

```json
{
  "schema_version": "1.0",
  "ok": true,
  "command": "query",
  "tool_version": "1.0.0",
  "wiki": {"id": "wiki_...", "name": "personal", "path": "/..."},
  "data": {},
  "warnings": [],
  "affected_files": []
}
```

失败响应：

```json
{
  "schema_version": "1.0",
  "ok": false,
  "command": "publish.apply",
  "tool_version": "1.0.0",
  "error": {
    "code": "PUBLISH_BASE_CHANGED",
    "message": "target knowledge changed after proposal",
    "details": {},
    "retryable": false
  }
}
```

退出码：`0` 成功；`2` 参数；`3` 配置/定位；`4` 不存在；`5` 冲突；`6` 内容校验；`7` 安全拒绝；`8` 锁冲突；`9` I/O；`10` 索引；`11` 迁移；`12` 不支持；`70` 内部错误。

字符串错误码和 JSON Schema 在同一 major 版本内只增不改。人类文案不属于机器契约。

## 8. 发布状态机

```text
raw(captured) -> change(proposed) -> change(applied) -> knowledge(published)
                              \-> change(rejected)
                              \-> change(stale/conflict)
knowledge(published) -> derived(fresh|stale|missing)
```

Change set：

```text
.llm-wiki/changes/<change-id>/
  proposal.json       不可变，包含操作、source hash、base hash、目标路径
  files/              待发布文件
  diff.patch          可读审阅材料
  state.json          唯一允许变化的状态
```

`propose` 不写 `knowledge/`。`apply` 是唯一审批提交点。若 source hash、base hash、proposal hash 任一变化，进入 `stale` 并以退出码 5 失败。`reject` 不删除审计材料。

## 9. 锁、事务和恢复

所有会修改实例文件的命令获取 `.llm-wiki/locks/write.lock`。只读命令不持锁，但只读取已经原子提交的文件和 SQLite 事务快照。

多文件写入协议：

1. 验证路径、Schema、来源和基线哈希。
2. 在同一文件系统 `.llm-wiki/transactions/<op-id>/stage` 写入全部新文件。
3. `fsync` 文件和目录，写入 `journal.json` 状态 `prepared`。
4. 保存被替换文件的恢复副本，然后逐个原子替换。
5. 所有事实文件完成后将 journal 标记为 `files_committed`。
6. 在单个 SQLite 事务中更新派生索引。
7. 标记业务状态和 journal 为 `complete`。

启动任何写命令前执行恢复：`prepared` 回滚；`files_committed` 以文件为准补建索引；`complete` 清理缓存。不得用 SQLite 状态反向覆盖文件。

SQLite 使用 rollback journal 而不是 WAL，降低同步盘和可移动知识库残留 sidecar 文件的问题。数据库损坏时重命名隔离并执行 rebuild。

## 10. SQLite 与检索

规范表：

```sql
meta(key PRIMARY KEY, value)
files(path PRIMARY KEY, layer, size, mtime_ns, file_hash, document_id, indexed_at)
documents(id PRIMARY KEY, layer, path UNIQUE, type, title, status, content_hash, updated_at, metadata_json)
source_links(knowledge_id, raw_id, raw_content_hash, PRIMARY KEY(knowledge_id, raw_id))
chunks(id PRIMARY KEY, document_id, ordinal, heading_path, body, body_hash, start_line, end_line)
chunks_fts(document_id UNINDEXED, chunk_id UNINDEXED, title, headings, properties, body)
operations(id PRIMARY KEY, kind, state, started_at, finished_at, detail_json)
```

`metadata_json`、`chunks.body` 和 FTS 内容都是解析缓存，不是事实源。`properties` 由 `tags`、`aliases` 和用户自定义 Properties 确定性生成，仅用于全文检索。完整重建必须先扫描文件、校验所有文档和引用，在临时数据库完成后原子替换旧数据库。

增量更新仍对候选文件计算内容哈希，不能只依赖 mtime/size。删除、重命名和来源关系均从本次文件扫描推导。

检索：

- 标题权重 8、标签 6、标题路径 4、正文 1。
- 中文文本和查询使用内置 Unicode Han 字符分词；英文使用 Unicode token。算法版本写入索引元数据，避免外部词典导致重建漂移。
- FTS 查询始终参数化并转义；不默认暴露 FTS5 查询语法。
- 按 BM25、文档 ID、chunk ordinal 进行确定性排序。
- SQLite 只返回候选 knowledge ID、路径、行号、chunk hash、文件 hash 和相关性分数。
- CLI 根据候选路径重新读取 `knowledge/` Markdown，校验 ID、完整文件 hash 和正文 hash，再从 Markdown 提取 evidence。
- 查询不自动同步索引，也不修改知识库；索引与发布文件不一致时返回 `INDEX_STALE`，由调用方显式执行 `index update`。
- evidence 的正文、metadata 和 sources 只能来自已验证的 `knowledge/` 文件；SQLite 只影响召回与排序。
- `raw/` 的来源完整性只由 `trace` 报告，不阻断 `query` 或 `show` 读取已经发布的事实。

首个正式版本不内置 embedding 模型或 SQLite 向量扩展。预留检索后端和 embedding 版本字段，但任何未来向量索引仍必须可从文件和显式模型配置重建。

## 11. 派生构建

标准编译器一对一输出 `llm-wiki/documents/<knowledge-id>.md`，内容包括规范标题、类型、标签、标题层级、正文和来源引用。SQLite chunk 是检索实现细节，不生成海量 chunk 文件。

`build` 只重建指纹变化、缺失或漂移的派生文件；`build --full` 在 staging 中生成完整目录并原子切换。用户手工修改派生文件时，普通 build 报告 drift；`--full` 明确覆盖，因为该层没有事实权威。

`manifest.json` 记录 compiler/version、config hash、每个输入输出 hash。删除整个 `llm-wiki/` 后的完整构建，除 `generated_at` 外必须产生字节等价正文和相同指纹。

## 12. 模板系统

内置模板包：

```text
resources/vault-templates/personal/
  template.toml
  AGENTS.md
  rules/capture.md
  rules/metadata.md
  rules/lifecycle.md
  rules/publish.md
  rules/derived.md
  templates/raw/note.md
  templates/raw/source.md
  templates/knowledge/concept.md
  templates/knowledge/guide.md
  templates/knowledge/reference.md
  templates/knowledge/decision.md
  templates/knowledge/project.md
```

模板 manifest 记录模板版本、兼容的实例 Schema、每个受管文件的初始 hash。初始化只复制实例必需规则和用户可编辑模板。

升级时比较“旧内置版本、用户当前文件、新内置版本”：未修改的受管文件可更新；用户修改过的文件只生成三方 diff 和升级提案，绝不静默覆盖。`raw/`、`knowledge/`、`llm-wiki/` 永远不属于模板升级写入目标。

`AGENTS.md` 只描述政策、命令和规则入口；各类知识字段要求放在 `rules/` 与 `templates/`，避免重复和漂移。

## 13. Skill

首批支持 Codex。客户端适配器接口负责 `Detect/ResolveTarget/Install/Status/Update/Uninstall`，后续客户端不能进入核心知识逻辑。

Codex 用户目标目录为 `$HOME/.agents/skills/llm-wiki`。安装前显示规范绝对路径，安装目录内写入 llm-wiki 所有权 manifest。升级只替换 manifest 声明的文件；发现未知用户文件时保留。卸载只删除本工具拥有且 hash 匹配的文件。

Skill 只负责启动和调用约束：先 `locate`，再读取目标知识库根目录的 `AGENTS.md`，按其中的操作路由加载规则。知识类型、采集细节和生命周期等语义不得复制到 Skill；切换 wiki 或规则变化后必须重新读取。

## 14. 安全边界

- 所有目标路径 canonicalize 后验证位于实例根目录。
- 默认拒绝管理目录中的符号链接、硬链接逃逸和路径穿越。
- 导入复制源文件内容，不在知识库中保留指向外部的链接。
- 默认拒绝私钥、`.env`、凭据数据库、认证 token 等敏感文件；显式 `--allow-sensitive` 才能导入，并产生高等级告警。
- 默认单文件上限 50 MiB；目录导入先完整规划并显示总量。
- 不自动解压归档、不执行模板脚本、不运行外部命令。
- 日志只记录相对路径、ID、hash、错误码和字节数，不记录正文、密钥或用户查询全文。
- 文件权限：运行目录和 changes 默认仅当前用户可读写；继承限制更严格的现有权限。

## 15. 迁移

版本分为：CLI major、实例 Schema、frontmatter Schema、索引 Schema、模板版本、compiler version、JSON 协议版本。

- 索引版本不匹配：允许自动 rebuild，因为它不是事实源。
- 实例/frontmatter 迁移：只允许 `migrate --plan` 后显式 `migrate --apply`。
- 模板升级：走三方 diff，不与实例 Schema 迁移混合。
- CLI 不支持更高 Schema 时拒绝写入。

## 16. 测试与完成门槛

1. 单元测试：路径、哈希、frontmatter、ID、配置、排序、错误码。
2. 属性测试：任意路径输入不能逃逸；序列化/解析保持语义；相同输入构建指纹一致。
3. 集成测试：每个命令的人类输出与 JSON 输出、幂等和冲突。
4. 故障注入：事务每一步中断并验证回滚或续作。
5. 重建测试：删除 SQLite 后 query/trace 结果与删除前等价；删除派生层后 build 指纹等价。
6. 安全测试：symlink、`..`、敏感文件、超大文件、恶意 YAML/FTS 输入。
7. Golden test：JSON Schema 和模板产物。
8. 端到端：执行 `HANDOFF.md` 的完整 init/raw/propose/apply/build/query/trace/rebuild 场景。
9. CI 在 macOS 上执行测试，并构建 `darwin/arm64` 发布目标。

只有上述门槛、所有命令面、模板质量检查和发布产物验证全部通过，目标才算完成。
