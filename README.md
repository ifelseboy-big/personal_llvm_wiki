# llm-wiki

`llm-wiki` 是一个本地优先、基于 Markdown、兼容 Obsidian 的可信个人知识库 CLI。它允许人和 AI CLI 通过同一套受控协议采集、整理、发布、构建、检索和追溯知识，无需 MCP 或常驻服务。

## 事实边界

- `raw/`：原始证据，内容变化可检测。
- `knowledge/`：经 `publish apply` 明确确认的可信知识，唯一最终事实源。
- `llm-wiki/`：从可信知识确定性构建的 AI 派生视图，可删除重建。
- `.llm-wiki/index.sqlite`：可删除、可重建的全文检索索引，不是知识或元数据来源。
- `.llm-wiki-governance.json`：仅在 1.1.x 升级时创建的旧知识完整文件基线，必须随 Vault 备份或提交。

## 构建

目标平台为 macOS Apple Silicon。构建需要 Go 1.25 或更高版本，以及 Xcode Command Line Tools 提供的 `clang/clang++`：

```bash
make build
make test
make verify
```

构建过程使用 CGO，将固定版本的 SQLite 和 `simple` tokenizer 静态集成到二进制；用户运行时不需要 Node、Python、Homebrew 或单独安装 SQLite/tokenizer。完整开发验收 `make verify` 还需要 `jq` 和 GoReleaser v2。

## 快速使用

```bash
llm-wiki init ~/Knowledge/personal --name personal --register --default
llm-wiki raw add ./article.md --wiki personal
llm-wiki publish propose --source <raw-id> --file ./draft.md --wiki personal
llm-wiki publish diff <change-id> --wiki personal
llm-wiki publish apply <change-id> --wiki personal
llm-wiki build --wiki personal
llm-wiki query "文章的核心结论" --wiki personal --json --no-interactive
llm-wiki trace <knowledge-id> --wiki personal --json --no-interactive
```

AI 调用应先通过 `locate --json --no-interactive` 定位知识库，读取该知识库根目录的 `AGENTS.md`，再按其路由加载相关规则。后续命令始终添加 `--json --no-interactive`，并只依赖版本化 JSON 字段与错误码。

`query` 只使用 SQLite 选择候选知识和排序，检索前会将完整 `knowledge/` Markdown 路径与文件 SHA-256 和索引快照比较，返回 evidence 前还会重新读取并验证候选文件。新增、删除、改名或修改任意知识文件都会返回 `INDEX_STALE`，不会把旧索引的空结果或 SQLite 缓存冒充事实。

## 命令面

```text
init, locate, status, doctor, migrate
raw add|list|show
publish propose|diff|apply|reject
build [--full], build status
query, show, trace
index status|update|rebuild
template list|show|create|upgrade
skill status|install|update|uninstall
```

所有命令支持 `--wiki` 和 `--json`；写命令支持 `--dry-run`。`publish apply` 是唯一可信知识提交点。

## 初始化模板

内置 `personal` 模板会生成：

- `.gitignore`：自动忽略可重建的 `llm-wiki/`、SQLite 索引和本地运行目录；保留 `.llm-wiki/changes/`、模板状态及模板基线供 Git 追踪。已有 `.gitignore` 时只追加缺失规则，不覆盖用户内容。
- `AGENTS.md`、`LLM-WIKI.md`：AI 治理入口和人的使用首页。
- `rules/`：采集、类型、元数据、生命周期、引用、发布、派生层和质量规则。
- `templates/raw/`：人工记录和文件来源模板。
- `templates/knowledge/`：claim、concept、guide、tutorial、reference、decision、project 模板。
- `views/`：可选的 Obsidian Bases 当前知识、复核和 raw 视图。

personal 1.2.0 模板 frontmatter 兼容 Obsidian Properties，并加入 lifecycle、有效期、复核日期、稳定关系和 raw-ID 命名脚注。`template create` 可展开 Obsidian 核心变量并生成可编辑草稿；`tags`、`aliases`、`cssclasses`、`description`、`related` 以及其他自定义属性在采集、发布和派生构建时保留并可检索。更新草稿省略属性表示保持，写为 `null` 表示删除。ID、状态、来源、时间和哈希仍由 CLI 管理，草稿不能覆盖。

从 personal 1.1.x 升级时，既有 knowledge 不会被直接改写；其完整文件哈希记录在根目录 `.llm-wiki-governance.json`。原文件保持不变时按 legacy 读取并提示迁移，下一次通过提案发布后写入 `governance_version: personal-1.2` 并进入严格校验。

`template upgrade --plan` 使用安装时基线、用户当前文件和新内置版本进行三方判断；用户修改的文件不会被静默覆盖。

## Codex Skill

```bash
llm-wiki skill status codex
llm-wiki skill install codex --dry-run
llm-wiki skill install codex --yes
```

默认目标为 `$HOME/.agents/skills/llm-wiki`。安装、升级和卸载只处理所有权 manifest 中记录且哈希匹配的文件。

## 设计与协议

- [技术设计](docs/TECHNICAL_DESIGN.md)
- [产品与实现交接](HANDOFF.md)
- `schemas/`：实例、frontmatter、发布提案和 CLI 响应 JSON Schema。

## 发布

唯一发布目标是 macOS Apple Silicon（arm64）。`.goreleaser.yaml` 和 GitHub Actions 生成单文件归档、SHA-256 校验和及 SBOM；Homebrew 模板在确定真实仓库和发布地址后填充，避免写入虚构地址。源码中的可移植实现不构成其他平台支持承诺。

发布和普通 CI 均使用原生 `macos-15` ARM64 runner。普通 CI 通过 `make release-snapshot` 提前验证 GoReleaser 归档、校验和、SBOM、二进制架构和可执行性，tag 工作流只负责正式生成 draft release。

## 检索评测

`make eval` 对固定多文档语料分别统计自然语言查询和关键词改写查询的 Recall@5、Precision@5、MRR 与 nDCG@5。`make benchmark-index` 显式运行 1k/10k 文档的完整索引一致性检查与检索性能基准；性能基准不放入普通 CI，避免共享 runner 波动形成错误门禁。
