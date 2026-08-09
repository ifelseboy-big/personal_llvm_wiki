# llm-wiki 工程 Agent 规则

本文件约束当前工具工程的开发、审查和发布。`resources/vault-templates/personal/AGENTS.md` 与 `docs/template-design/personal-1.2.0/AGENTS.md` 是交付给知识库实例的产品文件，不是本工程的开发规则。

## 工作原则

1. 优先满足用户的完整目标，不擅自缩减为 MVP。
2. 目标、范围或破坏性影响不明确时，先确认，不直接修改代码或历史。
3. 回答简洁、结论明确；评价与修复必须以代码、测试或文档证据为依据。
4. 保留用户已有修改，不覆盖无关内容，不使用 `git reset --hard`、`git checkout --` 等破坏性命令。
5. 未经用户明确要求，不提交、不推送、不改写 Git 历史。

## 产品边界

- 唯一正式支持平台是 macOS Apple Silicon（`darwin/arm64`）。不要为 Windows、Linux 或 Intel Mac 增加兼容复杂度，除非用户明确改变范围。
- 构建依赖 Go 1.25+、CGO 和 Xcode Command Line Tools 的 `clang/clang++`。
- `knowledge/` Markdown 是知识库唯一最终事实源；SQLite 仅选择检索候选，`llm-wiki/` 仅是可重建派生层。
- `publish apply` 是 CLI 写入可信知识的唯一通道。任何改动不得削弱文件哈希、来源绑定、事务恢复、路径安全和提案并发校验。
- 工具升级不得静默改写用户的 `raw/`、`knowledge/` 或 `llm-wiki/` 内容。

## 代码与协议

- Go 文件修改后执行 `gofmt`，错误必须携带稳定错误码或可判定类型，不依赖错误文本驱动逻辑。
- 新增或修改 CLI 行为时，同步覆盖人类输出、`--json --no-interactive`、`--dry-run`、退出码和回归测试。
- JSON 响应、配置、frontmatter、索引、编译器或模板的兼容性发生变化时，显式评估并更新对应版本；不得靠静默兼容掩盖协议变化。
- 读路径必须把索引结果重新映射并校验到真实 Markdown；禁止直接把 SQLite 缓存正文作为最终 evidence。
- 写路径必须保持原子性、可恢复性和符号链接/路径穿越防护。macOS 默认文件系统的大小写不敏感边界必须纳入测试。
- 不引入常驻服务、MCP、Node、Python、Homebrew 运行时依赖或可动态加载的任意 SQLite 扩展。

## 模板修改

- `resources/vault-templates/personal/` 是二进制实际嵌入内容；`docs/template-design/personal-1.2.0/` 是对应设计副本。
- 两棵目录中由 manifest 管理的同名模板必须保持字节一致。修改一侧时同步另一侧，并更新 `template.toml`、managed files、版本说明和测试。
- `.base` 文件必须是合法 YAML，只使用空格缩进，不得包含 TAB。
- 既有 Vault 升级必须走显式 plan/apply、三方比较和 legacy 基线；不得用某个可由用户编辑的 frontmatter 字段猜测文档版本。
- 根目录本文件不得复制到 Vault。Vault 内的 `AGENTS.md` 只描述知识治理和 CLI 操作路由。

## 验证要求

按改动风险执行，相关检查必须真实通过后才能声称完成：

```bash
make test
make test-race
CGO_ENABLED=1 CC=clang CXX=clang++ go vet -tags "fts5 sqlite_omit_load_extension" ./...
gofmt -d .
git diff --check
```

涉及发布、CGO、SQLite tokenizer 或构建配置时，额外执行：

```bash
make release-build
```

涉及 Schema、模板或 Bases 时，还需验证 JSON/YAML 可解析、设计副本与嵌入资源一致，并覆盖真实 `document.Parse`、CLI 或端到端路径，不能只测手工构造对象。

## Git 与临时文件

- 提交前检查 `git status --short`、`git diff --check` 和 staged diff，确认每个文件都属于用户批准的范围。
- `HANDOFF*.md`、临时审计报告、调试输出、测试产物和个人工作笔记默认不得加入或提交；只有用户明确要求将其作为正式文档时例外。
- `dist/`、根目录二进制、coverage、SQLite、缓存、日志和本地 Vault 数据不得提交。
- 首次推送新远程前检查完整分支历史，而不只检查最后一次提交，避免把早期临时文件一并发布。
- 独立审计必须由未参与实现的 Agent 执行；审计结论不能替代本地测试证据。
