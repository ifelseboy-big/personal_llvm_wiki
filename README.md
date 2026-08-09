# llm-wiki

`llm-wiki` 是一个本地优先、基于 Markdown、兼容 Obsidian 的可信个人知识库 CLI。它允许人和 AI CLI 通过同一套受控协议采集、整理、发布、构建、检索和追溯知识，无需 MCP 或常驻服务。

## 事实边界

- `raw/`：原始证据，内容变化可检测。
- `knowledge/`：经 `publish apply` 明确确认的可信知识，唯一最终事实源。
- `llm-wiki/`：从可信知识确定性构建的 AI 派生视图，可删除重建。
- `.llm-wiki/index.sqlite`：可删除、可重建的全文检索索引，不是知识或元数据来源。

## 构建

目标平台为 macOS Apple Silicon。构建需要 Go 1.25 或更高版本，以及 Xcode Command Line Tools 提供的 `clang/clang++`：

```bash
make build
make test
```

构建过程使用 CGO，将固定版本的 SQLite 和 `simple` tokenizer 静态集成到二进制；用户运行时不需要 Node、Python、Homebrew 或单独安装 SQLite/tokenizer。

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

`query` 只使用 SQLite 选择候选知识和排序，返回 evidence 前会重新读取并验证对应 `knowledge/` Markdown。索引与发布文件不一致时返回 `INDEX_STALE`，不会使用 SQLite 缓存冒充事实。

## 命令面

```text
init, locate, status, doctor, migrate
raw add|list|show
publish propose|diff|apply|reject
build [--full], build status
query, show, trace
index status|update|rebuild
template list|show|upgrade
skill status|install|update|uninstall
```

所有命令支持 `--wiki` 和 `--json`；写命令支持 `--dry-run`。`publish apply` 是唯一可信知识提交点。

## 初始化模板

内置 `personal` 模板会生成：

- `AGENTS.md`：AI 和维护者必须先读取的知识库治理入口及操作路由。
- `rules/`：采集、元数据、生命周期、发布、派生层和质量规则。
- `templates/raw/`：人工记录和文件来源模板。
- `templates/knowledge/`：concept、guide、reference、decision、project 模板。

模板 frontmatter 兼容 Obsidian Properties。`tags`、`aliases`、`cssclasses`、`description`、`related` 以及其他自定义属性在采集、发布和派生构建时保留并可检索；更新草稿省略属性表示保持，写为 `null` 表示删除。ID、状态、来源、时间和哈希仍由 CLI 管理，草稿不能覆盖。

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
