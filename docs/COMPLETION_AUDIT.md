# llm-wiki 完成度审计

审计日期：2026-08-09  
规范基线：`HANDOFF.md`、`docs/TECHNICAL_DESIGN.md`

## 结论

当前实现已覆盖既定 CLI 功能面、文件事实源约束、模板体系、Codex Skill、Apple Silicon 单二进制发布和首轮端到端验收。`knowledge/` 是唯一可信知识层；`raw/` 保留可验证来源；`llm-wiki/` 和 SQLite 均可删除重建。SQLite 中的 metadata JSON、来源关系和全文内容全部是文件解析缓存，不参与事实裁决。

## 功能映射

| 领域 | 已实现能力 | 验收证据 |
|---|---|---|
| 实例 | `init`、`locate`、`status`、`doctor`、`migrate` | CLI 工作流测试、冲突/注册/路径测试 |
| 采集 | `raw add/list/show`，stdin、文件、目录、二进制 sidecar | raw 单元测试、目录预检、敏感文件与大小限制测试 |
| 发布 | `propose/diff/apply/reject`，不可变 proposal、独立 state | 发布恢复测试、更新冲突与来源变化端到端测试 |
| 事实校验 | frontmatter Schema、ULID、正文/附件 SHA-256、来源哈希 | document、publish、doctor 和 trace 测试 |
| 检索 | SQLite FTS5、`simple` 中文 tokenizer、严格/宽松两级召回、证据/行号/来源返回 | 中文/英文/混合查询、排序、索引删除重建等价测试 |
| 展示溯源 | `show` 校验可信文件与来源，`trace` 比较实际字节哈希 | 实际 raw 篡改测试 |
| 派生层 | 增量 `build`、`--full`、`build status`、manifest 校验 | 删除派生目录、字节与指纹重建等价测试 |
| 索引 | `status/update/rebuild`，临时数据库原子替换，查询前完整 knowledge 快照校验 | 新增/删除/改名/未命中修改检测、SQLite 删除重建与 frontmatter-only 增量测试 |
| 模板 | 内置 `personal`、知识类型模板、自定义覆盖、三方升级 | 模板冲突、用户修改保留、安装预检测试 |
| Skill | Codex 检测、目标解析、安装/升级/卸载、所有权 manifest | Skill 冲突、篡改保留、symlink、dry-run 测试 |
| 协议 | JSON v1 envelope、错误码、退出码、wiki ID、affected files | 成功/失败 envelope 与完整命令面测试 |
| 分发 | Apple Silicon 单二进制、原生 ARM64 GoReleaser、GitHub Actions、Homebrew 模板 | `darwin/arm64` snapshot 归档、校验和、SBOM 与可执行验证 |

## 事实源与恢复不变量

1. `publish apply` 是工具内唯一写入 `knowledge/` 的路径。
2. proposal 同时绑定 raw 实际内容哈希、知识正文基线、完整知识文件基线和提案文件哈希。
3. 发布事务使用 `prepared → files_committed → complete` journal；恢复时以文件为准，不使用 SQLite 覆盖文件。
4. 恢复发现事务后的外部文件变化时保留外部文件并报冲突，不静默覆盖。
5. 查询保持只读，在 FTS 前比较完整 `knowledge/` 路径与文件 SHA-256；不一致时返回 `INDEX_STALE`，由调用方显式执行 `index update`。
6. 删除 `index.sqlite` 后可从文件完整重建，并得到等价 evidence。
7. 删除 `llm-wiki/` 后可完整构建；派生正文和构建指纹保持等价。

## 模板产物

`personal` 模板包含：

- 根级 `AGENTS.md`：事实层级、强制命令流程和禁止行为。
- `rules/capture.md`、`metadata.md`、`publish.md`、`derived.md`、`quality.md`。
- raw `note`、`source` 模板。
- knowledge `concept`、`guide`、`reference`、`decision`、`project` 模板。
- knowledge `claim`、`tutorial` 模板，生命周期、引用、类型规则和三个 Obsidian Bases 视图。
- `template create` 草稿生成、稳定关系链接和 personal 1.1.x → 1.2.0 三方升级及 legacy 治理基线。
- Obsidian Properties 在 raw 采集、knowledge 发布更新和 derived 构建中无损保留并进入可重建全文索引；系统字段始终由 CLI 重建。
- 模板版本、安装基线、用户覆盖和三方升级机制。

## 安全边界

- 拒绝路径穿越、重叠受管目录、符号链接、受管文件多重硬链接和非普通文件。
- 默认拒绝凭据、密钥、token、密码库、环境变量和浏览器认证类文件；显式覆盖会返回警告。
- Markdown frontmatter、Markdown 文件和输入文件均有限额。
- change ID、operation ID、proposal、state、journal 和派生 manifest 使用严格格式与路径校验。
- `--dry-run` 不创建知识、索引、Skill 目标或锁；存在待恢复事务时拒绝给出误导性预览。

## 验证记录

以下检查均已通过：

```text
make verify
make eval
make release-build
make release-snapshot
```

唯一发布目标构建成功：

```text
darwin/arm64
```

实际编译二进制完成了 `init → raw add → publish propose/diff/apply → build → query → trace → 索引删除后 rebuild → doctor`。重建前后 evidence 等价，trace 有效，doctor 健康。

## 版本与发布说明

实例、frontmatter、索引、模板、编译器和 JSON 协议均独立版本化。当前实例 Schema 是首个版本 `1`，不存在旧 Schema 的数据变换，因此 `migrate --plan/--apply` 明确返回空迁移；未来 Schema 必须增加显式迁移步骤，不能静默改写。

Homebrew 模板中的仓库地址保持参数化，因为仓库尚未提供发布地址。它只影响向公共渠道实际提交包，不影响本地构建、单二进制安装或功能完整性。
