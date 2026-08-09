# Handoff：macOS SQLite FTS5 中文检索改造

## 任务结论

继续使用 SQLite 作为 `knowledge/` Markdown 的可重建检索索引，引入 [`wangfenjin/simple`](https://github.com/wangfenjin/simple) 作为 FTS5 中文 tokenizer。

不引入 Bleve、QMD、Meilisearch、向量数据库或本地模型；不改变 `knowledge/` 是唯一事实来源的原则；不让 SQLite 直接提供最终事实。

目标平台只考虑 macOS Apple Silicon（`darwin/arm64`）。

## 为什么这样改

当前检索质量差的主要原因不在 SQLite，而在现有查询实现：

- `internal/index/index.go` 的 `tokenize()` 将中文拆为单字；
- `matchQuery()` 将所有 token 用 `OR` 连接；
- “LLVM 的核心结论”近似变成 `llvm OR 的 OR 核 OR 心 OR 结 OR 论`，造成大量无关候选；
- 当前测试只验证唯一字符串能否命中，没有验证中文自然查询的排序质量。

无数据库 LLM Wiki 通常使用 `index.md`、目录扫描或每次查询临时构建 BM25。我们的 SQLite 只是把这一步持久化，避免每次扫描全部 Markdown。Markdown 仍是知识存储，SQLite 只代替目录索引和全文检索。

## 必须保持的查询边界

```text
用户/Agent 查询
  -> SQLite FTS5 选择候选 knowledge ID、路径和 chunk
  -> 重新打开 knowledge Markdown
  -> 校验路径、ID、文件 hash、content hash、chunk hash
  -> 从 Markdown 返回 evidence
```

禁止直接把 FTS 表中的正文、标题或 metadata 作为最终 evidence。现有 `hydrateQueryCandidates()` 的回读校验必须保留。

## 技术选型

### SQLite 集成

当前工程使用 `modernc.org/sqlite` 和 `CGO_ENABLED=0`。为了注册 C++ FTS5 tokenizer，改为 CGO SQLite：

- 推荐使用 `github.com/mattn/go-sqlite3`；
- 编译启用 FTS5；
- 将 `simple` 固定到明确 commit，不跟随浮动分支；
- macOS 使用 Xcode 的 `clang/clang++`；
- 优先通过 `sqlite3_auto_extension(sqlite3_simple_init)` 静态注册到每个 SQLite 连接；
- 如果静态注册与 Go SQLite driver 的符号集成出现实质阻碍，可以退到随二进制发布并自动加载 `libsimple.dylib`，但不得要求用户手工安装扩展或 Homebrew SQLite；
- 运行时不得允许加载任意用户提供的 SQLite 扩展。

建议构建环境：

```text
CGO_ENABLED=1
GOOS=darwin
GOARCH=arm64
CC=clang
CXX=clang++
```

`simple` 默认不启用 Jieba，不启用拼音索引。这里排除 Jieba 的原因是 CLI 冷启动延迟，不是 C++ 集成成本。

### FTS5 表

保持当前 `chunks_fts` 总体结构，先不引入第二套搜索引擎：

```sql
CREATE VIRTUAL TABLE chunks_fts USING fts5(
  document_id UNINDEXED,
  chunk_id UNINDEXED,
  title,
  headings,
  properties,
  body,
  tokenize='simple 0',
  prefix='2 3 4'
);
```

注意：插入 FTS5 时必须传入原始的 title、heading、properties 和 body。删除当前插入前调用 `tokenize()` 的逻辑，否则 `simple` 看不到原始中文。

Schema version 必须递增。SQLite 是派生索引，旧索引不迁移，直接要求完整重建。

索引 meta 至少记录：

```text
schema_version
wiki_id
tokenizer = simple
tokenizer_version = <pinned commit/version>
query_planner_version = 1
```

## 查询实现

### 核心规则

删除当前“所有 token 全量 OR”的 `matchQuery()`。

默认查询使用：

```sql
chunks_fts MATCH simple_query(?, 0)
```

第二个参数必须为 `0`，关闭拼音查询展开。

在 Go 层先做轻量规范化：

- 保留英文、数字、下划线和中文；
- 去除标点和重复空白；
- 删除确定无检索价值的中文虚词与问句包装，例如“请问、是什么、有什么、为什么、怎么、如何、是否、吗、呢、的、了”；
- 不维护大规模中文词典；
- 如果清理后没有有效内容，返回无可检索词错误，不执行宽泛查询。

调用 Skill 时，Agent 应先把自然问题改写为短检索表达式。例如：

```text
原问题：为什么新增语言不需要重写整个编译器？
检索式：LLVM 稳定 IR 前端 后端 解耦
```

CLI 仍需能处理普通自然问题，但 Agent 查询改写是无本地模型条件下的语义召回来源。

### 召回与排序

第一版使用两级退让，不做复杂模型重排：

1. 对规范化后的检索式执行 `simple_query(..., 0)`，即严格 AND 检索；
2. 严格查询结果不足时，才用中文连续二字短语和英文完整 token 构造宽松 OR 查询补足；
3. 严格结果永远排在宽松结果之前，不混合两种查询的原始 BM25 分数；
4. 每一级内部继续使用 FTS5 `bm25()` 排序；
5. title、properties/aliases、headings、body 的权重建议继续为 `8/6/4/1`，不要在没有证据时反复调权重；
6. 每级先获取 `limit * 4` 候选，按 `knowledge_id` 去重；
7. 同一 knowledge 最多返回两个 chunk；
8. 最终再截断到用户 `--limit`。

宽松查询不得退回中文单字全量 OR。中文连续片段应生成二字短语，例如：

```text
核心结论 -> "核心" OR "心结" OR "结论"
```

FTS5 引号短语会约束字符顺序与相邻关系。

JSON 输出增加可调试信息，但不改变现有 evidence 字段语义：

```json
{
  "query": "原始输入",
  "normalized_query": "规范化检索式",
  "retrieval_modes": ["strict", "relaxed"],
  "facts_from": "knowledge_markdown"
}
```

每条 evidence 可增加 `retrieval_mode`，便于判断它来自严格召回还是宽松补足。

## Chunk 处理

本任务优先修 tokenizer 和 query，不扩大为完整 Markdown 解析器重写。

必须修正以下行为：

- chunk 开始于某个标题下面时，要继承该标题，而不是只有 chunk 内再次出现标题才记录 heading；
- 保持当前可配置的大小和 overlap；
- 不在本任务中引入 embedding token chunker。

如果实现过程中需要默认值，可继续使用当前 `1800/180`，不要因缺少评测随意更改。

## 索引更新与失败行为

- `index rebuild` 从 `knowledge/` 重建 FTS；
- `index update` 继续增量处理文件变化；
- tokenizer、Schema 或 wiki ID 不匹配时返回 `INDEX_STALE`/`INDEX_REBUILD_REQUIRED`；
- 查询不能在索引过期时使用 SQLite 缓存正文冒充事实；
- 删除 `index.sqlite` 后，重建前后的有效 evidence 内容必须等价；
- `doctor` 增加 tokenizer 探针，例如执行 `SELECT simple_query('中文检索', 0)` 并验证 FTS 表可以使用 `simple` tokenizer。

## 代码改动范围

预期涉及：

```text
go.mod / go.sum
Makefile
.github/workflows/ci.yml
internal/index/index.go
internal/app/index_commands.go
新增 SQLite/simple CGO 注册包
simple 第三方源码或固定构建依赖
相关 docs 和测试
```

当前 Windows/Linux packaging 文件可以暂时保留，但本任务不要求其构建成功；CI 和发布验收只以 `darwin/arm64` 为准。不要删除无关 packaging 文件。

## 必须增加的测试

不建设独立检索评测平台，只增加确定性的回归测试：

1. `LLVM 的核心结论`：LLVM 文档排在无关中文文档之前；
2. `稳定 IR`：中英文混合命中正确文档；
3. `核心结论`：连续中文词组可以命中；
4. `的`、`是什么`：不得因为高频虚词返回大量候选；
5. title 精确命中优先于仅 body 命中；
6. alias/property 仍可检索；
7. 同一文档最多返回两个 chunk；
8. 严格结果不足时宽松查询可以补足；
9. 修改 knowledge 后未更新索引，query 返回 stale；
10. SQLite 中正文被篡改时，最终 evidence 仍来自 knowledge Markdown；
11. 删除并重建 SQLite 后，查询 evidence 等价；
12. `CGO_ENABLED=1 GOOS=darwin GOARCH=arm64` 构建通过。

## 验收标准

完成必须同时满足：

- macOS Apple Silicon 可构建并运行；
- 用户不需要安装 SQLite、Homebrew 或 tokenizer；
- `simple` tokenizer 在每个数据库连接上可用；
- 当前中文单字 OR 查询逻辑彻底移除；
- 中文、英文和混合查询通过上述回归测试；
- SQLite 仍是可删除索引；
- 所有返回事实仍从校验后的 `knowledge` Markdown 读取；
- 不引入本地模型、服务进程或网络依赖；
- 不修改 `raw/`、`knowledge/`、发布审批和溯源语义。

## 明确不做

- 不实现向量检索；
- 不部署本地 embedding/reranker；
- 不引入 QMD、Bleve、Meilisearch；
- 不把 SQLite 变成事实来源；
- 不处理 Windows/Linux 构建；
- 不在本任务中重构或删除 `llm-wiki/` 派生目录；
- 不建设大规模人工标注评测集。

## 外部参考

- `simple`：<https://github.com/wangfenjin/simple>
- `simple` tokenizer 实现：<https://github.com/wangfenjin/simple/blob/master/src/simple_tokenizer.cc>
- SQLite FTS5：<https://sqlite.org/fts5.html>
