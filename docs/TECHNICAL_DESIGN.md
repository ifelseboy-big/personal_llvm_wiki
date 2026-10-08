# llm-wiki 技术设计

## 1. 目标与事实边界

llm-wiki 是单进程、本地文件优先、内容无关的知识库安全内核。Vault Agent 负责理解内容和选择语义，人负责批准，CLI 负责确定性、安全性与声明式策略执行。

| 层 | 属性 | 写入边界 |
| --- | --- | --- |
| `inbox/` | 可自由整理的临时材料 | 用户或获授权 Agent 可直接整理；CLI 采集/清理由 `internal/inbox` 执行 |
| `knowledge/` | 唯一可信事实源 | `internal/promote` 的 apply |
| `.llm-wiki/promotions/` | 冻结审阅与状态 | `internal/promote` |
| `.llm-wiki/index.sqlite` | 可重建 Knowledge 候选缓存 | `internal/index` |
| 内容包受管文件 | 策略、模板、Workflow 与说明 | `internal/templates` |

Knowledge 必须自包含。lineage 是历史元数据，不是对 Inbox 的运行时外键。删除 Inbox 不改变 Knowledge 健康性。Agent 禁止直接创建、修改或移动 `knowledge/`；语义判断不能绕过 Promotion。

Obsidian、Bases、Properties 与 wikilink 都是可选展示增强。CLI 不调用 Obsidian URI、插件 API、文件监听或可执行文件。

## 2. 分层与包边界

系统分为三层：

1. Core：文件安全、Schema、Promotion、事务、哈希、索引、证据回读和通用声明式策略执行。
2. Vault content pack：机器清单、分类、类型、字段规则、模板、Agent 路由与 Workflow。
3. Skills：Codex 与 Claude Code 的 Add/Query 普通入口，路由到 Vault 契约并调用 CLI。

```text
cmd/llm-wiki -> internal/app
internal/app -> config, document, governance, inbox, promote, index,
                selfupdate, templates, skill, vault
internal/governance -> config, document, fsutil
internal/inbox -> config, document, fsutil, vault
internal/promote -> config, document, fsutil, governance, inbox, vault
internal/index -> config, document, fsutil, governance, sqlite3simple, vault
internal/selfupdate -> standard library only
```

只有 `internal/index` 与 `internal/sqlite3simple` 依赖 SQLite。业务包不依赖 `internal/app`；稳定错误码、退出码、Cobra 和 stdout/stderr 映射只在 app 层。

Go Core 不得出现内容包中的 category、type、模板名、类型字段或状态值枚举。内置内容包可以通过 `embed.FS` 分发，但只能由通用 template manifest 和内容包策略发现。

## 3. 契约边界

各机器协议的当前版本只由对应 Schema、实现常量和内容包 manifest 定义，文档不复制数值形成第二事实源。不匹配的 instance、Knowledge frontmatter、内容包策略、governance、template、Skill 安装清单或索引快照直接拒绝或按其可重建属性重建，不猜测字段、不静默转换、不提供迁移读取分支。Inbox 是可编辑输入，不按版本字段拒绝材料，不需要迁移后才能发布。内容包升级只通过 `template upgrade` 三方比较显式完成，版本不匹配的实例在升级完成前不能发布、索引或返回事实。

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
  inbox/
    YYYY-MM-DD-初步功能名称/
      index.md
      docs/<original-stem>.md
      attachments/<original>  # only when needed
  knowledge/<type>/<slug>--<knowledge-id>.md
  knowledge/<type>/<slug>--<knowledge-id>.assets/<inbox-id>/<original>  # only when needed
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

`content-pack.json` 是 category、type、类型字段、模板映射、关系、生命周期检索语义和 Workflow 路由的唯一机器权威来源。其契约为 `schemas/content-pack.schema.json`：

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

Inbox 是可变工作区，不是事实。新增主题目录按 `YYYY-MM-DD-初步功能名称` 命名；日期来自采集时间，名称优先使用显式 title、正文一级标题、原文件名。批量采集中相同 title 的输入共用目录，不同 title 分开；再次添加同名主题或文档重名时追加人可读序号。主题根部生成普通 Markdown `index.md`，记录本次添加的简述及文档、附件链接；索引不含受管 ID，不作为登记笔记扫描。登记笔记放在主题目录的 `docs/`，附件按需放在并列的 `attachments/`。预览与实际写入复用相同目标解析，不覆盖已有文件。读取不要求固定目录或文件名，创建后的改名与 Inbox 内移动不构成漂移。

新笔记 frontmatter 保存 schema_version、稳定 `inbox_` ID、title、source、captured_at、media_type、original_name、可选 payload 与 pending/processed 状态。processed 包含 processed_at 和关联 Knowledge ID。Inbox 不校验 schema_version 是否为当前值；已有正文 hash、payload hash/bytes 仅作为历史属性原样保留，不与当前内容比较，新笔记不生成这些字段。用户属性往返保留。Knowledge frontmatter 的严格版本和哈希契约不变。

文本输入默认完整保存为规范化 Markdown 正文，原文中的 frontmatter 也作为输入内容保留，不要求原文符合任何元数据 Schema；显式 note_file 的用户 frontmatter 属性并入笔记。二进制输入或显式提供 note 的输入按原始字节复制到所属主题的 `attachments/`，没有附件时不创建该目录。payload 统一相对笔记所在目录，可使用 `..` 指向同级附件目录，但规范化后必须位于 Inbox 内；相对位置变化时须更新引用。各级 attachments 与 payload 目录只保存原始材料，不作为 Inbox 笔记扫描。已有 `YYYY/MM/<id>/item.md` 与 `payload/` 使用相同解析逻辑直接发布，不要求转换布局或重写元数据。

用户与获授权 Agent 可直接编辑正文、标题、用户属性和附件，改名、移动或删除材料。稳定 ID、Schema 与发布状态仍由 CLI 生成。未登记的普通文件可留在 Inbox；使用 add 登记后才参与基于 ID 的发布，不从路径或内容猜测身份。

### 5.2 Add

单文件或 stdin Add 在首次持久化前读取并预检全部输入与输出路径，写入时持有实例独占锁。stdin 只允许显式 `-`，且必须提供 name。简述可由 `--summary` 提供；缺省时索引按标题和文件名生成概览，不替代完整原始输入。note_file 可选。

目录批量输入通过 batch manifest，每项 input 可带可选 note、summary 和元数据。全部输入先校验重复、类型、大小、敏感文件、symlink/hardlink，再在同一文件系统暂存完整文件。逐文件以不可覆盖的安装操作提交；任一失败回滚本次新增文件与空目录，不删相邻文件。同一批次按 title 归入主题目录，每个主题只生成一份索引。

dry-run 复用相同规划和校验，但不创建锁、目录或事务。

### 5.3 List、Show 与 Clean

List 根据 Markdown frontmatter 的显式身份识别登记笔记，允许 Inbox 内除附件目录外的任意位置，重复 ID 必须报错，版本字段不作准入门槛。普通未登记文件不构成损坏。Show 从同次读取的笔记字节获取正文与完整 item hash，按安全路径读取附件并计算当前 payload hash，不与采集时的值比较、不写回文件。没有附件时 payload_path 指向笔记，payload hash 是规范化正文的 hash；有附件时是附件原始字节的 hash。缺失附件只影响该材料的快照读取和发布，不阻止明确清理其笔记。

Clean 接受明确 ID 或 `--processed`。明确 ID 可选 pending/processed，批量选择仅取 processed；必须先完成全部笔记的路径、文件类型、重复 ID 与链接安全预检。真实删除要求 `--yes`。不检查旧哈希，不删除父目录，也不删除可能共享或已修改的附件。删除活动计划输入后，apply 因输入缺失而拒绝。

批量 Clean 先把明确选定的笔记文件 rename 到同文件系统事务目录；中途失败按相反顺序 rename 回去；全部移动成功后删除事务目录。它不写 Knowledge 或索引。

## 6. Knowledge 与声明式治理

Knowledge frontmatter 包含 `know_` ID、type、title、published 状态、published_at、updated_at、content_hash、governance_version、lineage、可选 attachments、通用用户属性和未知扩展属性。category、description、lifecycle 及所有类型专属属性均由当前内容包声明，不属于 Core 固定字段。

lineage 每项保存 Inbox ID、发布时 payload hash、source 和 captured_at。运行时不会查找 Inbox 来验证 Knowledge。

每个 target 自动复制其引用的 Inbox 原始附件到该 Knowledge Markdown 相邻的 `.assets/` 目录；同一 Inbox 被多个 target 使用时各自保存副本。attachments 逐项记录来源 Inbox ID、原文件名、相对路径、字节数与 SHA-256。更新 target 保留已有附件并校验其字节，新增附件不得覆盖已有副本。Knowledge 的读取、查询、索引和诊断需验证已声明附件的规范路径、文件类型、链接数、大小与哈希；清理 Inbox 不影响这些副本。

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

Create draft 必须显式提供内容包允许的 type；Core 不猜测默认类型。`template create` 为 Knowledge 草稿返回一个 `proposed_knowledge_id`，该 ID 尚未写入事实或保留，但由 CLI 使用加密随机源生成，Plan 会再次校验格式、唯一性和目标冲突；manifest 未提供时 Plan 仍可生成 ID。Plan 先校验内容包版本、全部 Inbox、draft、Knowledge baseline、已有附件、声明式治理、关系、路径和重复目标。然后生成 `prm_` ID，将最终渲染文件和本次新增附件复制到 Promotion 的 `files/`，diff 列出全部 target 与附件的路径、大小和哈希，并以规范 plan JSON 的 SHA-256 作为 plan hash 写入 state。

状态机固定为：

```text
planned -> applied
        -> rejected
        -> stale
```

Plan 不写 Knowledge、不修改 Inbox、不更新索引。创建后 Apply 不再读取工作草稿。

查询活动 Promotion 时，先校验每份记录的 state 与冻结 plan 文件哈希；只有 `planned` 状态才按当前 Plan Schema 完整解码。已应用、已拒绝或已失效的历史 plan 不参与 Inbox 活动占用判断。`promote reject` 的真实执行只允许在独占写锁内将哈希匹配的 `planned` 记录改为 `rejected`，即使其 plan 已不符合当前 Schema；它不解析 plan、不写 Inbox 或 Knowledge。`promote diff/apply` 始终严格拒绝不符合当前 Schema 的 plan。

### 7.2 Diff、批准与 Apply 预检

Diff 返回冻结完整 diff 与 plan hash。人工批准对象是 promotion ID、完整 diff 和 plan hash。Apply 必须显式传入 `--approve <plan-hash>`；缺少或不一致直接冲突。

Apply 在实例独占写锁内重新验证 state/plan、内容包 identity 与策略 hash、Inbox hash、Knowledge 与已有附件 baseline、全部冻结文件 hash、声明式治理、规范路径和跨目标关系。任一漂移将 Promotion 标记 stale，Knowledge 与 Inbox 零写入。

### 7.3 多文件事实事务

真实 Apply 生成 `op_` journal。journal 枚举全部 Knowledge target、新附件、被 consume Inbox 的当前笔记路径和 Promotion `state.json`，记录 staged file、backup、new hash 和是否原本存在。

```text
prepared -> files_committed -> complete
```

prepared 恢复只在当前文件等于 backup 或 new hash 时回滚；外部漂移拒绝恢复。files_committed 恢复以已提交文件为准，校验全部 new hash，重建索引后 complete。索引失败不会回滚 Knowledge 或 processed 状态。

`promote apply` 的 JSON 结果返回 `transaction_state`。dry-run 为 `preview`；事实文件提交后为 `files_committed`；索引更新与 journal 完成后为 `complete`。调用方必须保留 warning，禁止把 `files_committed` 报告为全流程完成。

## 8. 查询与索引

索引 schema 只含 Knowledge documents、files、chunks、FTS、完整可回读 metadata 和可重建 metadata；不含 Inbox 文档、正文或生命周期唯一数据。

Rebuild 在 runtime 同文件系统创建临时 SQLite，完整扫描、验证 Knowledge 与内容包策略，再原子替换。Update 从 Knowledge 文件集合推导 added/changed/deleted；schema、tokenizer、planner、wiki ID 或内容包 identity 不匹配时完整重建。

`inbox show` 读取当前材料并返回安全 payload path、当前 payload hash 和完整 item hash；这些值只在 Promotion 中成为不可漂移的基线。`show` 返回经回读验证的正文、content hash 与当前完整 file hash；Workflow 只使用这些值构造更新 baseline。

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
init, locate, status, doctor, update
inbox add|list|show|clean
promote plan|diff|apply|reject
query, show
index status|update|rebuild
template list|show|create|upgrade
skill status|install|update|uninstall
```

JSON stdout 恰好一个对象；warnings 与 affected_files 永远是数组。诊断写 stderr。JSON 自动关闭颜色与交互。dry-run 不创建 Vault、锁、Promotion、事务、索引、注册或 Skill 文件。

`update` 是独立的 CLI 自升级用例，不解析或写入 Vault。它定位并解析当前真实可执行文件，限定 macOS/Linux 与 `llm-wiki` 文件名，通过受限 HTTPS 请求下载公开 `main` 的 `install.sh`，限制响应大小并校验 shell 入口和 NUL，然后在 `0700` 临时目录执行。安装器失败时现有二进制保持不变；成功后必须再次执行目标二进制并校验语义版本。JSON 返回 `check|current|updated`、前后版本、规范路径和 dry-run；dry-run 不访问网络或创建临时文件。

内容模板只从已安装内容包的 `templates/` 目录或所选内嵌包的 manifest 发现，不直接引用 personal 路径。模板安装/升级只管理 manifest 声明文件，不写 Inbox 或 Knowledge。

Vault 必须包含 Capture、Organize、Publish、Maintain、Query 五个可执行 Workflow。Workflow 负责语义判断；Publish 和 Maintain 只能通过 Promotion 写事实。Add/Query Skill 读取对应 Workflow，仍只把 CLI 当安全底座。

Skill client 名固定为 `codex` 与 `claude-code`。Codex 个人目标是 `~/.agents/skills`，并包含 `agents/openai.yaml`；Claude Code 个人目标按 Agent Skills 契约使用 `~/.claude/skills`，只安装跨客户端 Skill 文件。每个 client 在自己的目标内维护独立 manifest、锁、所有权哈希、冲突保护和安装/更新/卸载生命周期。`init --install-skill --skill-client <client>` 只安装显式选择的一个 client。

## 10. 平台、安全与隐私

项目通过 Public GitHub 仓库的正式 Release 源码匿名分发，不提供预构建二进制。一键安装器通过公开 HTTPS 解析最新版本或接受显式 `MAJOR.MINOR.PATCH`，下载标签归档，拒绝危险归档路径、链接、隐式降级和非本项目 Go module，并在临时目录构建。它使用 Go 1.24+、CGO、`fts5 sqlite_omit_load_extension` 与固定 simple tokenizer；新二进制通过版本自检后，才在安装目录内原子替换旧版本。安装失败不得破坏现有 CLI，也不得读取、修改或迁移 Vault。

安装器不提升权限、不修改 shell 配置，默认目标是 `~/.local/bin/llm-wiki`。普通 Vault 运行时不依赖外部 SQLite、解释器、MCP、常驻服务或网络服务；只有显式 `update` 需要公开 HTTPS、POSIX shell 以及源码构建工具链。

- 所有目标通过 filepath canonicalization、root containment 和逐组件 symlink 检查；不使用字符串前缀判断 containment。
- 受管文件拒绝多重 hardlink、非普通文件和大小超限。
- CLI 写操作在首次持久化前完成全量预检并持有独占锁。
- 私有目录默认 0700，受管文件默认 0600，保留更严格权限。
- 错误、journal、Promotion state、日志和 SQLite 不保存 Inbox/Knowledge 正文、查询全文、token 或密钥。
- 删除只作用于已经解析并验证的具体 Inbox 笔记文件，禁止递归删除其父目录或附件。
