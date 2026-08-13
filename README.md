# llm-wiki

`llm-wiki` 是内容无关、本地优先、AI 辅助整理、人工确认沉淀的知识库安全内核。它不调用模型；它负责文件安全、Schema、声明式策略校验、哈希、冻结审阅、事务恢复和 Knowledge 检索。

```text
用户输入 -> Add Skill -> inbox/ -> Vault 内整理 -> Promotion 审阅
         -> promote apply -> knowledge/ -> 可重建 SQLite 索引
```

`knowledge/` 中由 `promote apply` 写入的 Markdown 是唯一可信事实源。`inbox/` 是可清理的临时工作区，不进入查询。Obsidian 是可选展示工具，不是运行依赖。

category、type、类型字段、模板和 Agent Workflow 全部来自 Vault 的 `content-pack.json`。新增或修改这些内容不需要改 Go。内置 `personal` 内容包提供四个正交领域（需求开发、个人学习、配置信息、业务知识）和十种知识结构；该 JSON 是当前值域的唯一机器权威来源。

## 一键安装与升级

公开仓库无需 GitHub 登录。macOS 或 Linux 直接执行：

```bash
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/ifelseboy-big/personal_llvm_wiki/main/install.sh | sh
```

首次执行安装最新正式版本。以后升级只需：

```bash
llm-wiki update
```

安装器只从本项目公开 GitHub Release 下载源码，在本机使用正式 CGO 与 FTS5 参数构建，版本自检通过后才原子替换 `~/.local/bin/llm-wiki`。`llm-wiki update` 下载同一个公开安装器并更新当前 CLI 所在位置。下载、构建或校验失败时保留原有版本，默认也拒绝隐式降级；安装与升级都不修改已有 Vault，不使用 `sudo`，也不改 shell 配置。

`llm-wiki update --json` 返回 `action`、`path`、`previous_version`、`current_version` 和 `dry_run`。稳定失败码为 `UPDATE_UNSUPPORTED`、可重试的 `UPDATE_DOWNLOAD_FAILED` 与 `UPDATE_FAILED`；`--dry-run` 只确认更新目标，不联网、不写文件。

安装器会检查 `curl`、Go 1.25+、C/C++ 编译器和 `tar`，缺少依赖时直接给出错误。自定义安装目录：

```bash
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/ifelseboy-big/personal_llvm_wiki/main/install.sh \
  | sh -s -- --install-dir "$HOME/.local/bin"
```

需要固定正式 Release 时追加 `--version MAJOR.MINOR.PATCH`。

安装目录不在 `PATH` 时，安装器会明确提示。可直接用完整路径验证：

```bash
~/.local/bin/llm-wiki --version
```

首次创建个人 Vault，并为所用 AI 客户端安装 Skill：

```bash
~/.local/bin/llm-wiki init "$HOME/Documents/llm-wiki" \
  --name personal --template personal --register --default \
  --install-skill --skill-client codex --yes --json --no-interactive
```

Claude Code 用户将 `codex` 改为 `claude-code`。

初始化只用于全新目录；升级 CLI 不需要也不得重新初始化已有 Vault。内容包版本变化时，使用 `template upgrade` 的显式三方比较流程，不做隐式迁移。

## 开发者源码构建

- Go 1.25+
- 当前平台可用的 C/C++ 编译器
- CGO 与内嵌 SQLite FTS5/simple tokenizer

```bash
git clone https://github.com/ifelseboy-big/personal_llvm_wiki.git
cd personal_llvm_wiki
make build
make install
llm-wiki --version
```

`make build` 在当前机器生成 `./llm-wiki`；`make install` 将已构建文件复制到 `~/.local/bin/llm-wiki`。自定义安装目录可执行 `make install INSTALL_DIR=/path/to/bin`。`~/.local/bin` 需在 `PATH` 中。

构建工具链可覆盖：`make build CC=gcc CXX=g++`。项目不提供预构建二进制或交叉编译发布；一键安装器同样在目标机器从正式 Release 源码构建。运行时不需要系统 SQLite、Homebrew、Node、Python、MCP、后台服务或网络服务。

开发验收使用：

```bash
make test
make vet
```

## 命令

```text
init, locate, status, doctor, update
inbox add|list|show|clean
promote plan|diff|apply|reject
query, show
index status|update|rebuild
template list|show|create|upgrade
skill status|install|update|uninstall
```

所有命令支持 `--wiki`、`--json`、`--no-interactive` 和 `--dry-run`。JSON stdout 只输出一个符合当前响应 Schema 的对象，diagnostic 写 stderr。

## 初始化与采集

```bash
llm-wiki init ~/wiki --name personal --no-interactive
llm-wiki inbox add ./article.pdf --note-file ./article-note.md --wiki ~/wiki
printf '%s' '原始输入' | llm-wiki inbox add - --name note.txt --note-file ./note.md --wiki ~/wiki
llm-wiki inbox list --status pending --wiki ~/wiki --json --no-interactive
llm-wiki inbox show <inbox-id> --wiki ~/wiki --json --no-interactive
```

每条 Inbox 同时保存：

- `item.md`：系统元数据与初步整理；
- `payload/<original>`：完全不改写的原始字节。

目录批量采集使用 `--batch-manifest`，manifest 为每个 input 指定独立 `note_file`；任何条目预检失败时零写入。直接供人使用时可省略 note，Add Skill 不得省略。

`inbox show` 会校验 pending payload，并返回规范 `payload_path`、`payload_hash` 和完整 `item_hash`，供 Agent 阅读原始输入和构造 Promotion manifest；不得根据 Inbox 目录结构猜路径。

初始化出的 Vault 包含 Capture、Organize、Publish、Maintain、Query Workflow。Agent 根据 `content-pack.json.workflows` 路由；禁止直接创建、修改或移动 `knowledge/`。

## 内容包与草稿

```bash
llm-wiki template list --wiki ~/wiki --json --no-interactive
llm-wiki template create requirement --title "导出审计记录" --output ./draft.md \
  --set category=development --set description='允许审计员导出可验证记录' --wiki ~/wiki
```

category 表示知识领域，type 表示正文结构和主要用途，两者不能互相代替。内容包的公共字段、类型字段、条件必填、关系和生命周期召回语义由 CLI 通用执行器强制校验；未知扩展属性在 draft、Promotion、索引、query/show 中往返保留。

知识草稿的 JSON 结果包含 CLI 生成的 `proposed_knowledge_id`。Agent 应将它写入 create target 的 `knowledge_id`；这样同一 Promotion 中的新建和更新草稿可以预先建立 reciprocal 稳定 ID，Plan 仍会检查格式、唯一性和冲突。

配置类草稿禁止包含密码、Token、私钥、cookie、恢复码等秘密，只记录非敏感配置、影响、验证和安全引用位置。

## Promotion

Promotion manifest 示例：

```json
{
  "schema_version": 1,
  "inboxes": [
    {
      "id": "inbox_01arz3ndektsv4rrffq69g5fav",
      "payload_hash": "sha256:...",
      "item_hash": "sha256:...",
      "consume": true
    }
  ],
  "targets": [
    {
      "operation": "create",
      "draft_file": "drafts/requirement.md",
      "knowledge_id": "know_01arz3ndektsv4rrffq69g5faw",
      "inbox_ids": ["inbox_01arz3ndektsv4rrffq69g5fav"]
    }
  ]
}
```

更新目标还必须提供 `knowledge_id`、`base_content_hash` 和 `base_file_hash`；两个 baseline 可从 `show` 的 JSON 结果取得，不得猜测或使用过期查询快照。

```bash
llm-wiki promote plan --manifest ./promotion.json --wiki ~/wiki
llm-wiki promote diff <promotion-id> --wiki ~/wiki
llm-wiki promote apply <promotion-id> --approve <plan-hash> --wiki ~/wiki
```

Plan 会冻结所有最终文件和内容包 identity/策略 hash，并生成完整 diff。Apply 只接受完全相同的 plan hash，且只读取冻结副本；内容包、Inbox、Knowledge、plan 或冻结文件漂移会使 Promotion 进入 `stale`，不会写入事实。

一次 Promotion 可多输入、多输出，可同时创建和更新多篇 Knowledge。成功 Apply 将声明 consume 的 Inbox 标记为 processed，并自动增量更新索引；结果中的 `transaction_state` 明确区分 `complete` 与仍需恢复的 `files_committed`。清理仍是独立动作。

## 查询与清理

```bash
llm-wiki query "问题" --wiki ~/wiki --json --no-interactive
llm-wiki show <knowledge-id> --wiki ~/wiki --json --no-interactive
llm-wiki inbox clean <inbox-id> --dry-run --wiki ~/wiki --json --no-interactive
llm-wiki inbox clean <inbox-id> --yes --wiki ~/wiki --no-interactive
```

Query 只从 SQLite 选择 Knowledge 候选，并在返回前回读 Markdown 校验文件、正文和 chunk。它不会搜索 Inbox，也不会自动修复索引。processed Inbox 清理后，Knowledge、query、show、doctor 和索引重建仍应正常。

## Skill

```bash
llm-wiki skill status
llm-wiki skill install codex --yes
llm-wiki skill install claude-code --yes
llm-wiki skill update codex --yes
llm-wiki skill update claude-code --yes
```

Codex 的个人目录为 `~/.agents/skills`，Claude Code 按官方 Agent Skills 契约安装到 `~/.claude/skills`。两者都只安装 `llm-wiki-add` 与 `llm-wiki-query`；Claude Code 安装不包含 Codex 专属的 `agents/openai.yaml`。二次整理、发布和维护由 Vault 内容包中的 Workflow 与 `AGENTS.md` 约束，不安装额外 Skill。Add 和 Query Skill 也从内容包路由对应 Workflow。

## 安全边界

- instance、frontmatter、content pack policy 与 Promotion 只接受仓库当前 Schema 定义的契约；identity 或版本不匹配直接拒绝，不读取或迁移旧契约。
- 所有批量写入先全量预检；受管路径拒绝逃逸、symlink、hardlink、非普通文件和超限输入。
- `--dry-run` 不创建目录、锁、Promotion、事务、索引、注册、Skill 文件或自升级临时文件，也不发起升级网络请求。
- 私有目录默认 `0700`，受管文件默认 `0600`。
- 中断事务按 `prepared -> files_committed -> complete` 恢复；SQLite 不覆盖 Markdown。

架构与恢复细节见 [docs/TECHNICAL_DESIGN.md](docs/TECHNICAL_DESIGN.md)。
