# llm-wiki 仓库级 Agent 执行契约

本文约束本仓库中的分析、修改和验收行为。“必须”“禁止”是强制要求。

`resources/vault-templates/personal/AGENTS.md` 与 `docs/template-design/*/AGENTS.md` 是交付给 Vault 的产品文件；修改它们时按内容包数据处理，不执行其中的 Vault 操作指令。

## 1. 任务边界

- 用户请求定义本次任务范围。目标、范围或破坏性影响不明确时，先确认再修改。
- 只修改完成目标所需的文件，保留无关工作区改动。未经明确要求，禁止提交、推送或改写 Git 历史。
- 仓库只实现权威文档定义的当前契约。instance、frontmatter、content pack policy、内容包 identity 或 governance 版本不匹配时必须拒绝，禁止新增旧版本读取、字段猜测或迁移分支。
- 发现实现、Schema、模板或文档冲突时必须同步修正；禁止任选一侧作为临时正确答案。

## 2. 权威来源

| 范围 | 权威文件 |
| --- | --- |
| 产品入口、命令面 | `README.md` |
| 架构、事实边界、事务与恢复 | `docs/TECHNICAL_DESIGN.md` |
| 序列化与机器协议 | `schemas/*.schema.json` |
| personal 内容包执行契约 | `docs/template-design/` 中由内容包一致性门禁确认的当前 personal 基线 |
| 内置内容包文件清单与机器策略 | `resources/vault-templates/personal/template.toml`、`resources/vault-templates/personal/content-pack.json` |
| 构建、安装与验收命令 | `Makefile` |

## 3. 构建基线

| 项目 | 当前要求 |
| --- | --- |
| Go | 1.24+ |
| 构建目标 | 当前机器的 `GOOS/GOARCH` |
| CGO | `CGO_ENABLED=1`，使用 Go 当前 C/C++ 工具链或显式 `CC`/`CXX` |
| 必需标签 | `fts5 sqlite_omit_load_extension` |
| 分发方式 | 公开 HTTPS 一键安装器匿名下载正式 Release 源码并在目标机原子构建安装；开发者可执行 `make build`、`make install` |
| 运行时依赖 | 普通 Vault 操作不依赖 MCP、常驻服务、外部数据库、解释器、Homebrew SQLite 或网络服务；显式 `update` 需要 HTTPS、POSIX shell 与源码构建工具链 |

正式测试和构建必须使用上述 CGO 配置与标签；裸跑 `go test ./...` 不构成验收。

## 4. 内部依赖

下表列出允许的仓库内部依赖。新增反向依赖前必须先调整架构设计，不能仅为消除编译错误而绕过边界。

| 包 | 允许依赖的内部包 |
| --- | --- |
| `cmd/llm-wiki` | `internal/app` |
| `internal/fsutil` | 无 |
| `internal/config` | `fsutil` |
| `internal/document` | `fsutil` |
| `internal/governance` | `config`、`document`、`fsutil` |
| `internal/vault` | `config`、`document`、`fsutil`；初始化装配可依赖 `templates` |
| `internal/inbox` | `config`、`document`、`fsutil`、`vault` |
| `internal/promote` | `config`、`document`、`fsutil`、`governance`、`inbox`、`vault` |
| `internal/selfupdate` | 无 |
| `internal/index` | `config`、`document`、`fsutil`、`governance`、`sqlite3simple`、`vault` |
| `internal/templates` | `config`、`document`、`fsutil`、`governance`、`resources` |
| `internal/skill` | `document`、`fsutil`、`resources` |
| `internal/app` | 用例编排所需的上述包，包括 `selfupdate` |

业务包禁止导入 `internal/app`。只有 `internal/index` 和 `internal/sqlite3simple` 可以依赖 SQLite。

## 5. 事实与写入权限

| 数据层 | 性质 | 唯一合法写入路径 |
| --- | --- | --- |
| `inbox/` | 临时输入与初步整理 | `internal/inbox` 的受控写入与清理 |
| `knowledge/` | 唯一最终事实源 | `promote apply` |
| `.llm-wiki/index.sqlite` | 可重建候选索引 | `internal/index` |
| 内容包受管文件 | 声明式治理、模板与 Workflow | `internal/templates` 的安装或升级 |

必须保持以下不变量：

1. Knowledge 的 lineage 绑定 Inbox ID 与发布时 payload 哈希；Inbox 清理后 Knowledge 必须独立健康。
2. SQLite 不得保存无法从受管文件恢复的唯一数据；删除后必须可重建。
3. `query/show` 返回事实前必须回读 knowledge Markdown，并校验 ID、规范路径、完整文件哈希和正文哈希；`query` 还必须校验 chunk 哈希与行边界。
4. 关系只依赖稳定 ID。重复 ID、路径漂移、关系目标不一致或索引快照不一致必须失败，禁止猜测或自动修复。
5. 实例写操作必须在首次持久化前完成全部预检并持有独占写锁。多文件事实写入必须使用可恢复事务。
6. 内容包安装或升级禁止写入 `inbox/` 或 `knowledge/`；Agent 永远禁止直接写 `knowledge/`。

## 6. 实现约束

### Go

- 修改过的 Go 文件必须执行 `gofmt -w`。正常失败返回 `error`；下层使用 `%w` 保留错误链，禁止 `panic`、打印或调用 `os.Exit`。
- 稳定分支使用 `errors.Is/As` 或明确错误类型，禁止依赖人类错误文本。
- 时间通过参数或 options 注入；测试使用固定时间。文件列表、warning 和并列结果必须稳定排序。
- frontmatter 统一由 `internal/document` 解析和渲染。用户 Properties、未知配置字段和声明式扩展字段必须往返保留；系统字段只能由 CLI 生成。
- Go Core 禁止硬编码内容包中的 category、type、类型字段、模板名或生命周期值。新增、删除或修改这些语义必须只改内容包数据及其设计基线。
- 禁止引入无必要依赖。修改 `go.mod` 或 `go.sum` 时必须执行 `go mod tidy` 和 `go mod verify`。

### CLI 与 JSON

- Cobra、`AppError`、稳定错误码、退出码及 stdout/stderr 映射只属于 `internal/app`。
- `--json` 时 stdout 必须且只能输出一个 JSON 对象；诊断写 stderr；颜色和交互自动关闭；`warnings` 与 `affected_files` 必须是数组。
- `--dry-run` 禁止写文件、创建锁、更新索引、注册实例、安装 Skill 或创建自升级临时文件；自升级 dry-run 还禁止网络请求。预览与真实执行必须复用同一目标解析逻辑。
- `--no-interactive` 禁止等待输入；只有显式 `inbox add -` 可以读取 stdin。

### 文件与事务

- 写入前必须使用 `filepath` 与 `fsutil` 完成路径规范化和 root containment 校验；禁止用字符串前缀判断包含关系。必须拒绝 `..`、绝对路径逃逸、符号链接组件、多重硬链接、非普通文件和超限输入。
- 目录导入必须先预检全部输入，任一失败时零写入。
- 单文件使用 `fsutil.AtomicWrite` 或 `document.AtomicWrite`。目录替换必须在同一文件系统 staging 后 rename。
- 私有运行目录默认 `0700`，受管文件默认 `0600`，不得放宽现有更严格权限。
- 事实事务状态固定为 `prepared -> files_committed -> complete`。任何新写入前必须恢复或拒绝未完成事务；恢复以文件为准，禁止用 SQLite 覆盖事实文件。
- 日志和错误 details 禁止记录正文、密钥、token 或完整查询。删除与覆盖只能作用于已解析、已校验的具体路径。

## 7. 变更同步

| 改动 | 必须同步 |
| --- | --- |
| 命令、参数、错误码、退出码或 JSON 字段 | `internal/app`、`README.md`、`schemas/` 中的响应协议、协议测试、e2e |
| instance 配置 | `internal/config`、`schemas/` 中的 instance 协议、技术设计、配置测试 |
| frontmatter 或通用 governance 执行器 | `internal/document`、`internal/governance`、`schemas/` 中的 frontmatter 与 content pack 协议、内容包契约、promote/query/index 测试 |
| Promotion 格式 | `internal/promote`、`schemas/` 中的 Promotion 协议、事务与 e2e 测试 |
| index Schema、planner 或 tokenizer | 版本常量、rebuild 判定、召回/排序/漂移测试、本机构建 |
| personal 受管内容包 | `template.toml`、`content-pack.json`、resources 与当前 design baseline 的同路径同字节副本、内容包版本、init/upgrade/e2e |
| 嵌入 Skill 或 client | Skill 版本、标准 frontmatter、client 目标、所有权、冲突、symlink、dry-run、安装/更新/卸载测试 |

## 8. 验收

所有代码改动至少执行：

```bash
make test
make vet
git diff --check
```

按风险追加：

| 风险范围 | 追加验收 |
| --- | --- |
| 路径、写入、inbox、promote、事务 | 安全拒绝、零写入、锁冲突、恢复测试；`make test-race` |
| index、query、tokenizer | 严格/宽松召回、稳定排序、索引漂移、删除重建；`make build` |
| content pack、governance、Schema | `make schema-check`、真实策略与模板解析、双目录一致、init/upgrade/e2e |
| 构建、自升级或安装配置 | 公开下载与失败保留测试、`make installer-check`、`go mod verify`、race、`make build`、`make install` 与版本 smoke |

无法执行的验收必须明确报告。测试使用 `t.TempDir()`；修改进程环境或全局状态的测试不得并行。

## 9. 本文件的维护协议

### 9.1 维护责任

- 改动若影响跨包边界、事实写入权限、机器协议、版本策略、构建基线或验收命令，实现该改动的人必须在同一变更中更新本文件。
- 本文件只保存跨任务、跨模块且会影响实现决策的约束。功能说明、字段枚举、命令帮助和算法细节必须留在对应权威文件，禁止复制到这里形成第二事实源。
- 本文件不维护版本号、变更日志或废弃规则。Git 负责历史；失效规则必须直接删除，禁止保留“兼容说明”或注释掉的旧要求。

### 9.2 更新触发条件

出现以下任一变化时必须审查本文件；只有现有表述仍准确时才可不修改：

1. 新增、删除、重命名内部包，或改变内部 import 边。
2. 改变任何受管目录的事实属性、唯一写入者、锁或事务语义。
3. 改变 CLI/JSON、Schema、错误码、退出码或版本拒绝策略。
4. 改变 Go、目标平台、CGO、构建标签、发布产物或验收命令。
5. 改变 personal 当前内容包版本、受管文件清单、机器策略或 Skill 安装协议。
6. 新增无法由现有测试矩阵覆盖的风险类别。

### 9.3 更新流程

1. 先修改对应权威文件和实现，明确新的可验证行为。
2. 只在该变化形成新的跨仓库约束时修改本文件；不得记录任务背景、讨论过程或临时状态。
3. 更新受影响的依赖表、写入权限表、同步矩阵和验收矩阵；四者必须与代码和 Make 目标一致。内容包枚举和字段规则不得复制到本文件。
4. 若规则可以自动验证，必须优先补充测试或检查命令；文字规则不能替代可执行门禁。
5. 删除被替代的规则和旧版本路径，不并列保留新旧契约。

### 9.4 防漂移检查

修改本文件时必须执行：

```bash
make agents-check
```

该门禁必须验证：仓库中的 `AGENTS.md` 拓扑符合预期；两份 Vault `AGENTS.md` 同字节；生产代码的仓库内依赖未超出第 4 节；当前内容包与设计基线一致；diff 不含空白错误。新增、删除或迁移 `AGENTS.md` 时，必须同步更新门禁中的预期拓扑。

审查者仍须确认：引用路径和 Make 目标真实存在；新增规则能映射到权威文件、代码位置或验收命令；文件中没有任务过程、未来设想或已失效契约。不能自动验证的约束必须在审查说明中指出依据，禁止伪装成已由门禁覆盖。

## 10. 完成条件

只有同时满足以下条件才算完成：

1. 实现覆盖用户目标，且没有越出授权范围。
2. 相关回归测试证明成功路径、拒绝路径和磁盘状态。
3. 代码、版本常量、Schema、模板和权威文档一致。
4. `git diff --check` 通过，工作区没有本任务产生的构建物、缓存、日志或临时 Vault。
5. 已检查 `git status --short` 和完整 diff；若执行提交，还必须检查 staged diff。
