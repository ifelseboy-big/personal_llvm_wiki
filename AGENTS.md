# llm-wiki 工程规范

本文件适用于整个代码仓库。`resources/vault-templates/personal/AGENTS.md` 和 `docs/template-design/*/AGENTS.md` 是交付给 Vault 的产品内容，编辑时按模板数据处理，不把其中的知识库操作规则当作本工程指令。

## 1. 协作原则

1. 少说废话，少拆碎点，回答准确、明确。
2. 优先完成用户的真实目标，不轻易缩减为 MVP，也不擅自扩大产品或平台范围。
3. 目标、范围或破坏性影响不明确时先确认，不直接修改代码、文件或 Git 历史。
4. 只修改任务所需内容，保留工作区中的无关改动；未经明确要求，不提交、不推送、不改写历史。

## 2. 工程基线

| 项目 | 规范 |
| --- | --- |
| 语言 | Go 1.25+ |
| 正式平台 | macOS Apple Silicon（`darwin/arm64`） |
| 构建 | `CGO_ENABLED=1`，`CC=clang`，`CXX=clang++` |
| 必需标签 | `fts5 sqlite_omit_load_extension` |
| 分发 | 单个 `llm-wiki` 二进制、校验和、SBOM |
| 运行时 | 不依赖 MCP、常驻服务、外部数据库、解释器、Homebrew SQLite 或网络服务 |

所有正式测试和构建必须使用上述 CGO 配置与标签；不带标签的 `go test ./...` 不能作为验收结果。除非用户明确改变范围，不增加 Windows、Linux 或 Intel Mac 的兼容分支。

权威文档按职责划分：`README.md` 定义产品入口和命令面，`docs/TECHNICAL_DESIGN.md` 定义架构、不变量和恢复语义，`schemas/` 定义稳定序列化契约，`docs/template-design/<name-version>/TEMPLATE_SPEC.md` 定义模板执行契约。实现、测试、Schema 与文档冲突时必须同步修正，不能自行选择其中一侧。

## 3. 目录与依赖边界

| 路径 | 职责 | 禁止事项 |
| --- | --- | --- |
| `cmd/llm-wiki` | 进程入口、退出码 | 业务逻辑、文件操作、SQL |
| `internal/app` | Cobra 命令装配、参数校验、交互、输出协议、错误映射 | 实现领域规则、直接操作 SQLite |
| `internal/fsutil` | 原子写、路径、符号链接和硬链接安全原语 | 依赖任何上层业务包 |
| `internal/config` | 实例配置、注册表、定位、未知字段保留 | 依赖索引或业务用例 |
| `internal/document` | ID、frontmatter、Markdown、哈希、扫描和序列化 | 依赖 app、governance 或 index |
| `internal/governance` | personal 治理版本、生命周期、引用和关系的机械校验 | 输出 CLI 文案、写索引 |
| `internal/vault` | Vault 初始化、受管路径、写锁和安全检查 | 承载 CLI 输出协议 |
| `internal/raw` | 原始资料预检、导入和读取 | 写 `knowledge/` 或派生层 |
| `internal/publish` | proposal、diff、apply/reject、事务和恢复 | 绕过 proposal 直接发布 |
| `internal/index` | SQLite/FTS5、增量索引、重建、候选检索 | 成为事实源或返回未回读的最终事实 |
| `internal/build` | 从 knowledge 确定性生成派生层和 manifest | 反向修改 knowledge |
| `internal/templates` | 内置/自定义模板、草稿生成、安装和三方升级 | 修改用户事实目录 |
| `internal/skill` | Codex Skill 的检测、安装、升级和所有权管理 | 复制知识治理逻辑到 Skill |
| `resources` | `go:embed` 的模板与 Skill 资源 | 保存用户数据或运行时状态 |
| `tests/e2e` | 跨包完整工作流和重建等价性 | 替代各包的边界单测 |

依赖基线为：`cmd` 只进入 `app`；`app` 负责用例编排；`fsutil` 不依赖其他内部包；`config`、`document` 只依赖基础设施；`governance` 依赖 config/document/fsutil；业务包不得反向导入 `app`。`vault.Init -> templates` 是现有初始化装配例外，不能据此让 vault 的锁和路径安全代码依赖更多业务包。只有 `internal/index` 和 `internal/sqlite3simple` 可以依赖 SQLite，`config`、`document` 和 `fsutil` 必须保持与 SQLite 无关。

## 4. 不可破坏的不变量

1. `knowledge/` 中经发布流程生成的 Markdown 是唯一最终事实源；生产代码只有 `publish apply` 可以写入该层。
2. `raw/` 是原始证据。发布必须绑定 raw ID 与发布时内容哈希；raw 后续漂移由 `trace` 报告，不反向改写已发布事实。
3. `llm-wiki/` 是可删除重建的确定性派生层，只能从已验证 knowledge 生成，不能反向覆盖事实。
4. `.llm-wiki/index.sqlite` 是可删除重建的候选索引。`query/show` 必须回读 knowledge Markdown 并验证 ID、路径、文件哈希和正文哈希；`query` 还必须校验 chunk 哈希与边界后才能返回 evidence。
5. 关系依赖稳定 ID，不依赖标题或 slug；同一 ID 重复、路径漂移或索引快照不一致必须失败，不能猜测目标。
6. 所有实例写操作在首次持久化前必须完成完整预检并持有独占写锁；多文件事实写入必须使用可恢复事务，不允许出现半提交状态。
7. 模板升级不得写入 `raw/`、`knowledge/` 或 `llm-wiki/`；实例/frontmatter 迁移必须先 plan，再由用户显式 apply。
8. 删除 SQLite 或派生目录后必须能仅凭受管文件恢复；任何无法由文件重建的数据都不能只存在缓存中。

## 5. Go 编码规范

- 遵循标准 Go 风格；修改过的 `.go` 文件必须执行 `gofmt -w`。导入分为标准库、第三方、仓库内部三组。
- 正常失败返回 `error`，不 `panic`。下层包使用 `%w` 保留错误链；需要分支判断时使用 `errors.Is/As` 或明确错误类型，不依赖人类错误文本。
- `AppError`、稳定错误码、退出码和 stdout/stderr 映射只属于 `internal/app`；下层包不打印、不调用 `os.Exit`。
- 时间会影响结果时，通过 options 或参数传入 `time.Time`；测试使用固定时间。输出、文件列表、warning、查询并列结果必须稳定排序，保证可复现。
- 路径使用 `filepath` 处理并在写入前做 root containment、受管目录和 symlink/hardlink 检查；不能只做字符串前缀判断。
- 单文件写入使用 `fsutil.AtomicWrite`/`document.AtomicWrite`；目录级替换必须在同一文件系统 staging 后 rename。私有运行目录默认 `0700`，受管文件默认 `0600`，不得放宽已有更严格权限。
- 解析和渲染 frontmatter 统一通过 `internal/document`。用户 Properties、未知配置字段和明确允许的扩展字段必须往返保留；系统字段只能由 CLI 生成。
- 不新增无必要依赖。修改 `go.mod`/`go.sum` 时说明原因，执行 `go mod tidy` 和 `go mod verify`，不得引入运行时外部服务。

## 6. CLI 与机器协议

- 所有命令通过 `internal/app` 装配；业务包返回结构化结果和错误，不感知 Cobra、终端颜色或 JSON 输出。
- `--json` 时 stdout 只能输出一个 JSON 对象，诊断进入 stderr，并自动关闭颜色和交互。`warnings` 与 `affected_files` 必须输出数组，不能用 `null`。
- 同一 JSON major 内字段和错误码只增不改；人类文案不是机器契约。新增错误必须选择明确的稳定 code 和既有退出码类别。
- 支持 `--dry-run` 的写命令不得写文件、创建锁、更新索引、注册实例或安装 Skill；预览结果必须与真实执行使用同一套校验和规划逻辑。
- `--no-interactive` 不得等待用户输入；显式 `raw add -` 读取 stdin 是唯一数据输入例外。
- 新增或修改命令时必须同时覆盖参数冲突、人类输出、JSON 成功/失败 envelope、退出码、dry-run、非交互行为和帮助文本。

## 7. 文件、事务与安全

写入前必须先规范化并验证所有源和目标：拒绝 `..`、绝对路径逃逸、符号链接组件、受管文件多重硬链接、非普通文件和大小超限。目录导入必须先预检全部输入，任一失败时零写入。

可信层多文件事务保持 `prepared -> files_committed -> complete` 状态机：stage 与目标位于同一文件系统，文件和目录按需要 fsync，替换前保留恢复材料。恢复以文件为准，禁止用 SQLite 覆盖事实文件。任何写命令开始前必须处理或明确拒绝待恢复事务。

日志和错误 details 不记录正文、密钥、token 或完整用户查询；敏感输入默认拒绝，只有显式授权才允许，并返回清晰 warning。删除或覆盖只能作用于已解析、已校验的具体受管路径，禁止宽泛 glob 和未解析变量。

## 8. 版本与兼容

| 版本 | 变化时的处理 |
| --- | --- |
| CLI major | 允许破坏机器协议前必须显式升 major |
| JSON protocol | 同 major 只做向后兼容扩展，并同步 `schemas/response-*.schema.json` |
| instance/frontmatter Schema | 提供显式 plan/apply 迁移；更高未知版本拒绝写入 |
| index Schema / planner / tokenizer | 升版并要求 rebuild，不对缓存做事实迁移 |
| template SemVer | 内置受管内容变化即评估升版；升级使用旧基线、用户文件、新模板三方比较 |
| compiler version | 派生算法、正文或 fingerprint 输入变化时升版并使旧 manifest stale |
| Skill version | 嵌入 Skill 内容或安装协议变化时升版，并验证所有权 manifest 的升级/卸载 |

兼容逻辑必须有稳定、可验证的版本依据，不能通过用户可编辑字段是否存在来猜测旧版本。升级失败要可重试；状态文件、配置和模板基线的落盘顺序不能制造无法恢复的半升级状态。

## 9. 模板、Schema 与嵌入资源

`resources/vault-templates/personal/template.toml` 的 `managed_files` 是内置模板清单。每个条目必须在 `resources/vault-templates/personal/` 与对应的 `docs/template-design/<version>/` 中保持同路径、同字节；新增、删除或改名必须同步 manifest、安装/升级逻辑、版本和测试。

模板必须同时满足 YAML、Obsidian Properties 和 CLI renderer：`.base` 是合法 YAML 且禁止 TAB；日期使用明确格式；变量必须能由 CLI 或 Obsidian 核心模板确定展开；模板不得执行脚本或依赖社区插件。Schema、Go validator、模板默认值和文档中的字段枚举必须一致。

`resources/skills/llm-wiki-{query,add,publish,maintain}/` 按查询、采集、受控发布和维护拆分，只负责定位 Vault、读取该 Vault 的 `AGENTS.md` 并调用职责内 CLI，不复制 knowledge 类型、生命周期或发布语义。修改嵌入 Skill 时同步 `internal/skill` 版本和安装、冲突、更新、卸载、symlink、dry-run 测试。

## 10. 测试规范

| 改动 | 最低验证 |
| --- | --- |
| 任意 Go 代码 | 相关包回归测试、`make test`、带正式标签的 `go vet`、`git diff --check` |
| CLI/JSON/错误码 | `internal/app` 协议与 workflow 测试、`tests/e2e` |
| 路径/写入/raw/publish | 安全拒绝、零写入、并发冲突、事务恢复测试；追加 `make test-race` |
| index/tokenizer/query | 严格/宽松召回、稳定排序、索引漂移、删除重建等价；追加 release build |
| build/lifecycle | 增量/no-op、漂移、跨日期 stale、删除重建字节与 fingerprint 等价 |
| template/governance/Schema | 真实 `document.Parse`、格式解析、双目录一致、init/upgrade/legacy/e2e |
| Skill | 所有权、用户改动保留、冲突、symlink、dry-run、安装/升级/卸载 |
| 发布配置 | `go mod verify`、race、`make release-build`、GoReleaser 配置和产物 smoke |

基础验收命令：

```bash
make test
CGO_ENABLED=1 CC=clang CXX=clang++ go vet -tags "fts5 sqlite_omit_load_extension" ./...
git diff --check
```

按上表追加：

```bash
make test-race
make release-build
go mod verify
goreleaser check
```

测试使用 `t.TempDir()` 隔离文件系统、固定时间验证日期逻辑，并覆盖失败前后磁盘状态。修改进程环境或全局状态的测试不得并行。不能用纯手工构造对象代替至少一个真实文件解析和完整命令路径。

## 11. 完成与提交

任务完成必须同时满足：实现覆盖目标；相关回归测试能证明修复；代码、Schema、模板、版本和文档保持一致；相关验证真实通过；工作区无本任务产生的临时产物。无法执行的验证要明确列出，不能声称通过。

暂存前检查 `git status --short` 和完整 diff，提交前检查 staged diff。一次性 handoff、审计草稿、调试输出、构建产物、缓存、日志和临时 Vault 不得自行提交。首次推送到新远程前检查完整分支历史，而不是只看最后一个提交。
