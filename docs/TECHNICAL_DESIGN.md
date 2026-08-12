# llm-wiki 技术设计

## 1. 目标与事实边界

llm-wiki 是单进程、本地文件优先、内容无关的知识库安全内核。Vault Agent 负责理解内容和选择语义，人负责批准，CLI 负责确定性、安全性与声明式策略执行。

| 层 | 属性 | 唯一写入者 |
| --- | --- | --- |
| `inbox/` | 临时输入、原始 payload、初步整理 | `internal/inbox` |
| `knowledge/` | 唯一可信事实源 | `internal/promote` 的 apply |
| `.llm-wiki/promotions/` | 冻结审阅与状态 | `internal/promote` |
| `.llm-wiki/index.sqlite` | 可重建 Knowledge 候选缓存 | `internal/index` |
| 内容包受管文件 | 策略、模板、Workflow 与说明 | `internal/templates` |

Knowledge 必须自包含。lineage 是历史元数据，不是对 Inbox 的运行时外键。删除 processed Inbox 不改变 Knowledge 健康性。Agent 禁止直接创建、修改或移动 `knowledge/`；语义判断不能绕过 Promotion。

Obsidian、Bases、Properties 与 wikilink 都是可选展示增强。CLI 不调用 Obsidian URI、插件 API、文件监听或可执行文件。

## 2. 分层与包边界

系统分为三层：

1. Core：文件安全、Schema、Promotion、事务、哈希、索引、证据回读和通用声明式策略执行。
2. Vault content pack：机器清单、分类、类型、字段规则、模板、Agent 路由与 Workflow。
3. Skills：Add 与 Query 普通入口，路由到 Vault 契约并调用 CLI。

```text
cmd/llm-wiki -> internal/app
internal/app -> config, document, governance, inbox, promote, index,
                templates, skill, vault
internal/governance -> config, document, fsutil
internal/inbox -> config, document, fsutil, vault
internal/promote -> config, document, fsutil, governance, inbox, vault
internal/index -> config, document, fsutil, governance, sqlite3simple, vault
```

只有 `internal/index` 与 `internal/sqlite3simple` 依赖 SQLite。业务包不依赖 `internal/app`；稳定错误码、退出码、Cobra 和 stdout/stderr 映射只在 app 层。

Go Core 不得出现内容包中的 category、type、模板名、类型字段或状态值枚举。内置内容包可以通过 `embed.FS` 分发，但只能由通用 template manifest 和内容包策略发现。

## 3. 版本边界

| 契约 | 当前版本 |
| --- | --- |
| instance | 3 |
| frontmatter | 3 |
| content pack policy | 1 |
| Promotion plan | 1 |
| JSON response | 2.0 |
| personal content pack | 3.0.1 |
| personal governance | personal-3.0 |
| Skill | 4.0.1 |
| index schema | 6 |
| query planner | 4 |

生产路径只读取当前契约。旧 instance、frontmatter、内容包策略、governance 或 template version 直接拒绝；不猜测字段、不静默转换、不提供迁移读取分支。内容包升级由 `template upgrade` 三方比较显式完成，版本不匹配的实例在升级完成前不能发布、索引或返回事实。

## 4. Vault 布局与内容包发现

```text
<vault>/
  llm-wiki.toml
  content-pack.json
  AGENTS.md
  LLM-WIKI.md
  workflows/
    capture.md
    organize.md
    publish.md
    maintain.md
    query.md
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

`llm-wiki.toml` 的 `template.name`、`template.version` 和 `template.content_pack` 绑定已安装包。内嵌 `template.toml` 是分发清单，声明包版本、机器策略文件和全部受管文件；Core 不根据 name 选择行为。

`content-pack.json` 是 category、type、类型字段、模板映射、关系、生命周期检索语义和 Workflow 路由的唯一机器权威来源。其契约为 `schemas/content-pack-v1.schema.json`：

- `schema_version`、`name`、`version`、`governance_version` 与实例和安装清单严格相等；
- `categories` 声明领域，`types` 声明文档结构，两者正交；
- 公共 `knowledge.fields` 与每个 type 的 `fields` 使用通用 `string`、`string_list`、`enum`、`date`、`boolean`、`integer` 规则，并可声明单字段等值条件 `required_when`；
- `relations` 声明稳定 ID 列表属性、可选 reciprocal 属性，并可指定唯一的 `default_for_create` 关系供 `template create --related` 使用；
- `lifecycle` 可声明字段、inactive/disputed 值和日期字段；默认召回只使用这些声明，不认识具体状态名；
- `quality` 声明 H1/title、模板变量、prompt 和脚注完整性检查；
- `workflows` 和 type `template` 路径必须指向包的受管普通文件。

加载策略时必须校验 root containment、逐组件 symlink、普通文件、单 hardlink、大小、严格 JSON、重复名、交叉引用和版本绑定。Markdown 说明只引用机器策略，不复制 category/type/字段枚举形成第二事实源。模板中的 type/category 占位与策略映射由模板测试验证。

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

## 6. Knowledge 与声明式治理

Knowledge frontmatter 包含 `know_` ID、type、title、published 状态、published_at、updated_at、content_hash、governance_version、lineage、通用用户属性和未知扩展属性。category、description、lifecycle 及所有类型专属属性均由当前内容包声明，不属于 Core 固定字段。

lineage 每项保存 Inbox ID、发布时 payload hash、source 和 captured_at。运行时不会查找 Inbox 来验证 Knowledge。

通用治理执行器按内容包数据完成：type/category membership、公共和类型字段规则、H1、未解析模板标记、脚注完整性、稳定 ID 关系与 reciprocal 关系校验。未知用户属性必须在 draft、Promotion、索引、query/show 全链路往返保留。

关系语义仅依赖稳定 Knowledge ID。路径、标题和 wikilink 不参与权威关系。生命周期评估按内容包声明生成 inactive、disputed、时间有效性和复核 warning；Core 不认识任何内容包状态值。

## 7. Promotion

### 7.1 Manifest 与 Plan

Manifest 包含：

- Inbox ID、预期 payload hash、完整 item file hash、consume 标记；
- 每个 target 的 create/update、draft_file、lineage Inbox 集合；
- update 的 Knowledge ID、正文基线 hash 和完整文件基线 hash；
- 可选 create Knowledge ID 与目标路径；内置 Workflow 使用 `template create` 返回的 CLI 生成 ID，以支持同计划 reciprocal 关系。

冻结 Plan 额外绑定内容包 name、version、governance version 与规范策略 hash；同版本策略内容漂移也必须使 apply 失败并标记 stale。

Create draft 必须显式提供内容包允许的 type；Core 不猜测默认类型。`template create` 为 Knowledge 草稿返回一个 `proposed_knowledge_id`，该 ID 尚未写入事实或保留，但由 CLI 使用加密随机源生成，Plan 会再次校验格式、唯一性和目标冲突；manifest 未提供时 Plan 仍可生成 ID。Plan 先校验内容包版本、全部 Inbox、draft、Knowledge baseline、声明式治理、关系、路径和重复目标。然后生成 `prm_` ID，将最终渲染文件复制到 Promotion 的 `files/`，生成覆盖所有 target 的 diff，并以规范 plan JSON 的 SHA-256 作为 plan hash 写入 state。

状态机固定为：

```text
planned -> applied
        -> rejected
        -> stale
```

Plan 不写 Knowledge、不修改 Inbox、不更新索引。创建后 Apply 不再读取工作草稿。

### 7.2 Diff、批准与 Apply 预检

Diff 返回冻结完整 diff 与 plan hash。人工批准对象是 promotion ID、完整 diff 和 plan hash。Apply 必须显式传入 `--approve <plan-hash>`；缺少或不一致直接冲突。

Apply 在实例独占写锁内重新验证 state/plan、内容包 identity 与策略 hash、Inbox hash、Knowledge baseline、冻结文件 hash、声明式治理、规范路径和跨目标关系。任一漂移将 Promotion 标记 stale，Knowledge 与 Inbox 零写入。

### 7.3 多文件事实事务

真实 Apply 生成 `op_` journal。journal 枚举全部 Knowledge target、被 consume Inbox 的 `item.md` 和 Promotion `state.json`，记录 staged file、backup、new hash 和是否原本存在。

```text
prepared -> files_committed -> complete
```

prepared 恢复只在当前文件等于 backup 或 new hash 时回滚；外部漂移拒绝恢复。files_committed 恢复以已提交文件为准，校验全部 new hash，重建索引后 complete。索引失败不会回滚 Knowledge 或 processed 状态。

`promote apply` 的 JSON 结果返回 `transaction_state`。dry-run 为 `preview`；事实文件提交后为 `files_committed`；索引更新与 journal 完成后为 `complete`。调用方必须保留 warning，禁止把 `files_committed` 报告为全流程完成。

## 8. 查询与索引

索引 schema 只含 Knowledge documents、files、chunks、FTS、完整可回读 metadata 和可重建 metadata；不含 Inbox 文档、正文或生命周期唯一数据。

Rebuild 在 runtime 同文件系统创建临时 SQLite，完整扫描、验证 Knowledge 与内容包策略，再原子替换。Update 从 Knowledge 文件集合推导 added/changed/deleted；schema、tokenizer、planner、wiki ID 或内容包 identity 不匹配时完整重建。

`inbox show` 在返回前验证 pending payload，并返回规范 payload path、payload hash 和完整 item hash。`show` 返回经回读验证的正文、content hash 与当前完整 file hash；Workflow 只使用这些值构造更新 baseline。

索引时由通用生命周期声明计算静态 `retrieval_active` 和可选生效/失效时间边界；默认查询按当前时间筛选这些派生列，`--include-inactive` 可用于审计。category、type 和扩展元数据完整存入 `metadata_json`，不受固定枚举限制。

Query 流程：

1. 校验 index schema、tokenizer、planner、wiki ID 与内容包 identity；
2. 比较索引与 Knowledge 完整文件集合及 file hash；
3. FTS 返回候选；
4. 按 candidate path 回读 Markdown；
5. 校验 ID、规范路径、file hash、content hash、chunk hash、行边界与当前内容包策略；
6. 返回证据、完整 Knowledge metadata 和通用生命周期评估。

Query 不自动更新索引。Inbox 永远不进入上述扫描或候选流程。

## 9. CLI、模板与 Workflow

命令面保持：

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

内容模板只从已安装内容包的 `templates/` 目录或所选内嵌包的 manifest 发现，不直接引用 personal 路径。模板安装/升级只管理 manifest 声明文件，不写 Inbox 或 Knowledge。

Vault 必须包含 Capture、Organize、Publish、Maintain、Query 五个可执行 Workflow。Workflow 负责语义判断；Publish 和 Maintain 只能通过 Promotion 写事实。Add/Query Skill 读取对应 Workflow，仍只把 CLI 当安全底座。

## 10. 平台、安全与隐私

项目通过 Private GitHub 仓库的正式 Release 源码分发，不提供预构建二进制。一键安装器复用 GitHub CLI 身份或显式 Token，解析最新版本或接受显式 `MAJOR.MINOR.PATCH`，通过 GitHub API 下载标签归档，拒绝危险归档路径、链接和非本项目 Go module，并在临时目录构建。它使用 Go 1.25、CGO、`fts5 sqlite_omit_load_extension` 与固定 simple tokenizer；新二进制通过版本自检后，才在安装目录内原子替换旧版本。安装失败不得破坏现有 CLI，也不得读取、修改或迁移 Vault。

安装器不提升权限、不修改 shell 配置，默认目标是 `~/.local/bin/llm-wiki`。运行时不依赖外部 SQLite、解释器、MCP、常驻服务或网络服务。

- 所有目标通过 filepath canonicalization、root containment 和逐组件 symlink 检查；不使用字符串前缀判断 containment。
- 受管文件拒绝多重 hardlink、非普通文件和大小超限。
- 写操作在首次持久化前完成全量预检并持有独占锁。
- 私有目录默认 0700，受管文件默认 0600，保留更严格权限。
- 错误、journal、Promotion state、日志和 SQLite 不保存 Inbox/Knowledge 正文、查询全文、token 或密钥。
- 删除只作用于已经解析并验证的具体 Inbox 目录。
