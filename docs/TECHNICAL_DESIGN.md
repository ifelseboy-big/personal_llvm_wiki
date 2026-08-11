# llm-wiki 技术设计

## 1. 目标与事实边界

llm-wiki 是单进程、本地文件优先的知识库 CLI。AI 负责自然语言整理，人负责批准，CLI 负责确定性与安全性。

| 层 | 属性 | 唯一写入者 |
| --- | --- | --- |
| `inbox/` | 临时输入、原始 payload、初步整理 | `internal/inbox` |
| `knowledge/` | 唯一可信事实源 | `internal/promote` 的 apply |
| `.llm-wiki/promotions/` | 冻结审阅与状态 | `internal/promote` |
| `.llm-wiki/index.sqlite` | 可重建 Knowledge 候选缓存 | `internal/index` |
| 模板受管文件 | 治理与草稿资源 | `internal/templates` |

Knowledge 必须自包含。lineage 是历史元数据，不是对 Inbox 的运行时外键。删除 processed Inbox 不改变 Knowledge 健康性。

Obsidian、Bases、Properties 与 wikilink 都是可选展示增强。CLI 不调用 Obsidian URI、插件 API、文件监听或可执行文件。

## 2. 包边界

```text
cmd/llm-wiki -> internal/app
internal/app -> config, document, governance, inbox, promote, index,
                templates, skill, vault
internal/inbox -> config, document, fsutil, vault
internal/promote -> config, document, fsutil, governance, inbox, vault
internal/index -> config, document, fsutil, governance, sqlite3simple, vault
```

只有 `internal/index` 与 `internal/sqlite3simple` 依赖 SQLite。业务包不依赖 `internal/app`；稳定错误码、退出码、Cobra 和 stdout/stderr 映射只在 app 层。

## 3. 版本边界

| 契约 | 当前版本 |
| --- | --- |
| instance | 2 |
| frontmatter | 2 |
| Promotion plan | 1 |
| JSON response | 2.0 |
| personal template | 2.0.0 |
| personal governance | personal-2.0 |
| Skill | 3.0.0 |
| index schema | 5 |
| query planner | 3 |

生产路径只读取当前契约。旧 `paths.raw`、frontmatter v1、change、旧 governance 或旧 instance 直接拒绝；不猜测字段、不静默转换。

## 4. Vault 布局

```text
<vault>/
  llm-wiki.toml
  AGENTS.md
  LLM-WIKI.md
  inbox/YYYY/MM/<inbox-id>/
    item.md
    payload/<original>
  knowledge/<type>/<slug>--<knowledge-id>.md
  templates/
  rules/
  views/
  .llm-wiki/
    promotions/<promotion-id>/
      plan.json
      state.json
      diff.patch
      files/<knowledge-id>.md
    transactions/<operation-id>/
    locks/
    template-state.json
    template-base/<version>/
    index.sqlite
```

Promotion、模板状态和模板基线是审阅或恢复数据，可进入版本控制。SQLite、锁、事务、日志和缓存在 `.gitignore` 中忽略。

## 5. Inbox

### 5.1 数据模型

`item.md` frontmatter 包含：`inbox_` ID、title、source、captured_at、media_type、original_name、payload 相对路径、payload bytes/hash、初步整理正文 hash，以及 pending/processed 状态。processed 还包含 processed_at 与关联 Knowledge ID 列表。

payload 永远按原始字节复制。Markdown、文本和二进制都使用同一路径；初步整理只进入 `item.md`。

### 5.2 Add

单文件或 stdin Add 在持锁前读取并预检输入，在首次持久化前生成完整计划。stdin 只允许显式 `-`，且必须提供 name。

目录批量输入只能通过 batch manifest；manifest 将每个 input 映射到独立 note 和元数据。全部输入先校验重复、类型、大小、敏感文件、symlink/hardlink，再在同一文件系统 staging，最后逐目录 rename。任一提交失败删除本次新目录，形成零写入结果。

dry-run 复用相同规划和校验，但不创建锁、目录或事务。

### 5.3 List、Show 与 Clean

List 只读取固定 `item.md`，不遍历 payload Markdown 作为受管文档。Show 校验 item 正文 hash、payload hash、字节数、路径和文件类型。

Clean 只接受明确 ID 或 `--processed`。所有目标必须 processed、无 planned Promotion 引用且通过 item/payload/path/symlink/hardlink 校验。真实非交互删除要求 `--yes`。

批量 Clean 先把所有目标原子 rename 到同文件系统事务目录；中途失败按相反顺序 rename 回去；全部移动成功后删除事务目录。它不写 Knowledge 或索引。

## 6. Knowledge 与治理

Knowledge frontmatter 包含 `know_` ID、type、title、published 状态、published_at、updated_at、content_hash、governance_version、description、lifecycle、lineage 和类型专属用户属性。

lineage 每项保存 Inbox ID、发布时 payload hash、source 和 captured_at。运行时不会查找 Inbox 来验证 Knowledge。

`related`、`supersedes`、`superseded_by` 保存稳定 Knowledge ID。校验通过 ID 回读目标，检查重复、自引用和 supersedes 双向一致；路径、标题和 wikilink 不参与权威关系。

治理校验还要求：H1 与 title 一致、无模板变量或 prompt 注释、生命周期与类型字段有效。外部 URL、书目、会议来源或脚注由内容类型决定，不强制 Inbox-ID 脚注。

## 7. Promotion

### 7.1 Manifest 与 Plan

Manifest 包含：

- Inbox ID、预期 payload hash、完整 item file hash、consume 标记；
- 每个 target 的 create/update、draft_file、lineage Inbox 集合；
- update 的 Knowledge ID、正文基线 hash 和完整文件基线 hash；
- 可选 create Knowledge ID 与目标路径。

Plan 先校验所有 Inbox、draft、Knowledge baseline、governance、关系、路径和重复目标。然后生成 `prm_` ID，将最终渲染文件复制到 Promotion 的 `files/`，生成覆盖所有 target 的 diff，并以规范 plan JSON 的 SHA-256 作为 plan hash 写入 state。

状态机固定为：

```text
planned -> applied
        -> rejected
        -> stale
```

Plan 不写 Knowledge、不修改 Inbox、不更新索引。创建后 Apply 不再读取工作草稿。

### 7.2 Diff 与批准

Diff 返回冻结完整 diff 与 plan hash。人工批准对象是 promotion ID、完整 diff 和 plan hash。

Apply 必须显式传入 `--approve <plan-hash>`。缺少或不一致直接冲突；JSON/no-interactive 不等待输入。

### 7.3 Apply 预检

Apply 在实例独占写锁内重新验证：

- state 仍为 planned，plan hash 未变；
- Inbox 仍 pending，item/payload hash 未变；
- create target 仍不存在；update target 的 ID、正文 hash 和完整文件 hash 未变；
- 冻结文件 path、完整 hash、正文 hash、ID、governance 和跨目标关系仍一致。

任一漂移将 Promotion 标记 stale，Knowledge 与 Inbox 零写入。

### 7.4 多文件事实事务

真实 Apply 生成 `op_` journal。journal 枚举全部 Knowledge target、被 consume Inbox 的 `item.md` 和 Promotion `state.json`，记录每个目标的 staged file、backup、new hash 和是否原本存在。

```text
prepared -> files_committed -> complete
```

1. prepared：staging、backup 和 journal 已持久化，事实文件尚未保证完整提交。
2. 依固定路径顺序 AtomicWrite 全部文件。
3. files_committed：Markdown、Inbox 状态和 Promotion 状态已提交。
4. app 增量更新索引；成功后标记 complete。

prepared 恢复：只有当前文件等于 backup 或 new hash 时才回滚；外部漂移拒绝恢复。新增文件只有等于 new hash 才删除。

files_committed 恢复：以已提交文件为准，校验所有 new hash，重建索引后 complete。索引失败不会回滚 Knowledge 或 processed 状态，保留 journal 并返回 warning。

## 8. 查询与索引

索引 schema 只含 Knowledge documents、files、chunks、FTS 和可重建 metadata；不含 Inbox 文档、正文或生命周期唯一数据。

Rebuild 在 runtime 同文件系统创建临时 SQLite，完整扫描、验证 Knowledge 与 governance，再原子替换。Update 从 Knowledge 文件集合推导 added/changed/deleted；schema、tokenizer、planner 或 wiki ID 不匹配时完整重建。

Query 流程：

1. 校验 index schema、tokenizer、planner、wiki ID；
2. 比较索引与 Knowledge 完整文件集合及 file hash；
3. FTS 返回候选；
4. 按 candidate path 回读 Markdown；
5. 校验 ID、规范路径、file hash、content hash、chunk hash 和行边界；
6. 返回证据与 Knowledge metadata。

Query 不自动更新索引。Inbox 永远不进入上述扫描或候选流程。

## 9. CLI 与 JSON

命令面：

```text
init, locate, status, doctor
inbox add|list|show|clean
promote plan|diff|apply|reject
query, show
index status|update|rebuild
template list|show|create|upgrade
skill status|install|update|uninstall
```

JSON stdout 恰好一个对象；warnings 与 affected_files 永远是数组。诊断写 stderr。JSON 自动关闭颜色与交互。dry-run 不创建 Vault、锁、Promotion、事务、索引、注册或 Skill 文件。

稳定错误码至少区分 WIKI_NOT_FOUND、WIKI_LOCKED、INBOX_INPUT_REJECTED、INBOX_CLEAN_REJECTED、PROMOTION_PLAN_INVALID、PROMOTION_STALE、INDEX_NOT_FOUND、INDEX_STALE、KNOWLEDGE_INVALID 与 RECOVERY_REQUIRED。

## 10. 模板与 Skill

personal 2.0.0 模板将整理和批准流程放在 Vault `AGENTS.md` 与 `rules/promote.md`。resources 与 `docs/template-design/personal-2.0.0` 的受管同路径文件必须同字节。

模板安装/升级只管理 manifest 声明的文件，不写 Inbox 或 Knowledge。Obsidian views 可删除且不影响 CLI。

只嵌入 Add 与 Query 两个 Skill。Skill 3.0.0 更新可读取旧 manifest；仅删除旧 manifest 拥有且 hash 匹配的 Publish/Maintain 文件，修改文件保留并报告，未知目录不接管。

## 11. 平台与源码安装

项目通过源码分发，不提供预构建归档、交叉编译产物或 GitHub Release 自动发布。使用者拉取仓库后，在当前机器依次执行 `make build` 与 `make install`。

本机构建使用 Go 1.25、CGO、`fts5 sqlite_omit_load_extension` 与固定 simple tokenizer。`CC`/`CXX` 默认读取 Go 当前工具链配置，也可由 Make 参数覆盖。`make install` 只复制已经生成的本机二进制，默认目标是 Go 的 bin 目录。

CI 在 macOS 与 Linux 原生运行 test/vet，Linux 运行 race，并对 `make build -> make install -> llm-wiki --version` 做 smoke 验证。运行时不依赖外部 SQLite 或 tokenizer。

## 12. 安全与隐私

- 所有目标通过 filepath canonicalization、root containment 和逐组件 symlink 检查；不使用字符串前缀判断 containment。
- 受管文件拒绝多重硬链接、非普通文件和大小超限。
- 写操作在首次持久化前完成全量预检并持有独占锁。
- 私有目录默认 0700，受管文件默认 0600，保留更严格权限。
- 错误、journal、Promotion state、日志和 SQLite 不保存 Inbox/Knowledge 正文、查询全文、token 或密钥。
- 删除只作用于已经解析并验证的具体 Inbox 目录。
