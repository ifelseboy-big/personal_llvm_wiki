# llm-wiki

`llm-wiki` 是内容无关、本地优先、AI 辅助整理、人工确认沉淀的知识库安全内核。它不调用模型；它负责文件安全、Schema、声明式策略校验、哈希、冻结审阅、事务恢复和 Knowledge 检索。

```text
用户输入 -> Add Skill -> inbox/ -> Vault 内整理 -> Promotion 审阅
         -> promote apply -> knowledge/ -> 可重建 SQLite 索引
```

`knowledge/` 中由 `promote apply` 写入的 Markdown 是唯一可信事实源。`inbox/` 是可清理的临时工作区，不进入查询。Obsidian 是可选展示工具，不是运行依赖。

category、type、类型字段、模板和 Agent Workflow 全部来自 Vault 的 `content-pack.json`。新增或修改这些内容不需要改 Go。内置 `personal` 内容包提供四个正交领域（需求开发、个人学习、配置信息、业务知识）和十种知识结构；该 JSON 是当前值域的唯一机器权威来源。

## 源码安装

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

构建工具链可覆盖：`make build CC=gcc CXX=g++`。项目不提供预构建归档或交叉编译发布；运行时不需要系统 SQLite、Homebrew、Node、Python、MCP、后台服务或网络服务。

开发验收使用：

```bash
make test
make vet
```

## 命令

```text
init, locate, status, doctor
inbox add|list|show|clean
promote plan|diff|apply|reject
query, show
index status|update|rebuild
template list|show|create|upgrade
skill status|install|update|uninstall
```

所有命令支持 `--wiki`、`--json`、`--no-interactive` 和 `--dry-run`。JSON 协议版本为 `2.0`；stdout 只输出一个 JSON 对象，diagnostic 写 stderr。

## 初始化与采集

```bash
llm-wiki init ~/wiki --name personal --no-interactive
llm-wiki inbox add ./article.pdf --note-file ./article-note.md --wiki ~/wiki
printf '%s' '原始输入' | llm-wiki inbox add - --name note.txt --note-file ./note.md --wiki ~/wiki
llm-wiki inbox list --status pending --wiki ~/wiki --json --no-interactive
```

每条 Inbox 同时保存：

- `item.md`：系统元数据与初步整理；
- `payload/<original>`：完全不改写的原始字节。

目录批量采集使用 `--batch-manifest`，manifest 为每个 input 指定独立 `note_file`；任何条目预检失败时零写入。直接供人使用时可省略 note，Add Skill 不得省略。

初始化出的 Vault 包含 Capture、Organize、Publish、Maintain、Query Workflow。Agent 根据 `content-pack.json.workflows` 路由；禁止直接创建、修改或移动 `knowledge/`。

## 内容包与草稿

```bash
llm-wiki template list --wiki ~/wiki --json --no-interactive
llm-wiki template create requirement --title "导出审计记录" --output ./draft.md \
  --set category=development --set description='允许审计员导出可验证记录' --wiki ~/wiki
```

category 表示知识领域，type 表示正文结构和主要用途，两者不能互相代替。内容包的公共字段、类型字段、条件必填、关系和生命周期召回语义由 CLI 通用执行器强制校验；未知扩展属性在 draft、Promotion、索引、query/show 中往返保留。

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
      "inbox_ids": ["inbox_01arz3ndektsv4rrffq69g5fav"]
    }
  ]
}
```

更新目标还必须提供 `knowledge_id`、`base_content_hash` 和 `base_file_hash`。

```bash
llm-wiki promote plan --manifest ./promotion.json --wiki ~/wiki
llm-wiki promote diff <promotion-id> --wiki ~/wiki
llm-wiki promote apply <promotion-id> --approve <plan-hash> --wiki ~/wiki
```

Plan 会冻结所有最终文件和内容包 identity/策略 hash，并生成完整 diff。Apply 只接受完全相同的 plan hash，且只读取冻结副本；内容包、Inbox、Knowledge、plan 或冻结文件漂移会使 Promotion 进入 `stale`，不会写入事实。

一次 Promotion 可多输入、多输出，可同时创建和更新多篇 Knowledge。成功 Apply 将声明 consume 的 Inbox 标记为 processed，并自动增量更新索引；清理仍是独立动作。

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
llm-wiki skill install --client codex --yes
llm-wiki skill update --client codex --yes
```

只安装 `llm-wiki-add` 与 `llm-wiki-query`。二次整理、发布和维护由 Vault 内容包中的 Workflow 与 `AGENTS.md` 约束，不安装额外 Skill。Add 和 Query Skill 也从内容包路由对应 Workflow。

## 安全与版本

- instance/frontmatter 只接受 v3，content pack policy 只接受 v1，Promotion 只接受 v1；identity 或版本不匹配直接拒绝，不读取或迁移旧契约。
- 所有批量写入先全量预检；受管路径拒绝逃逸、symlink、hardlink、非普通文件和超限输入。
- `--dry-run` 不创建目录、锁、Promotion、事务、索引、注册或 Skill 文件。
- 私有目录默认 `0700`，受管文件默认 `0600`。
- 中断事务按 `prepared -> files_committed -> complete` 恢复；SQLite 不覆盖 Markdown。

架构与恢复细节见 [docs/TECHNICAL_DESIGN.md](docs/TECHNICAL_DESIGN.md)。
