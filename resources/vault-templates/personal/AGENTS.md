# 知识库治理入口

本文件是人和 Agent 管理当前知识库的规则入口。`llm-wiki` CLI 不解析本文件；Agent 必须先读取本文件，再按操作路由读取必要规则并调用 CLI。

## 不可变事实边界

- `knowledge/` 中经过发布的 Markdown 是唯一最终事实源，正文、Properties、关系和来源绑定均以这些文件为准。
- `raw/` 保存原始证据，不是整理后的知识结论。
- `.llm-wiki/index.sqlite` 只用于检索候选。查询命中后必须读取对应 `knowledge/` 文件，不能直接把 SQLite 缓存当作事实。
- `llm-wiki/` 是从 `knowledge/` 生成的 AI 派生视图，可以删除重建，冲突时无条件以 `knowledge/` 为准。
- `templates/`、`rules/`、`views/`、`AGENTS.md` 和 `LLM-WIKI.md` 是治理材料，不是用户事实。

不得直接创建、修改或移动 `knowledge/` 中的发布文件，不得直接编辑 `llm-wiki/`，不得根据 SQLite 或派生内容反向恢复事实。

## 启动规则

每次开始知识库工作流时，先运行 `llm-wiki locate --json --no-interactive`，再读取返回根目录中的本文件。切换 wiki 或本文件发生变化后必须重新读取。

同一任务、同一 wiki 且相关规则未变化时，不必在每条命令前重复读取。

## 操作路由

| 操作 | 调用前必须读取 |
| --- | --- |
| `raw add` | [[rules/capture|采集规则]]、[[rules/metadata|元数据规则]] |
| `raw list`、`raw show` | 无额外规则 |
| `query`、`show`、`trace` | 无额外规则；CLI 必须从 `knowledge/` 返回事实 |
| 使用非当前、过期、有争议或被替代的查询结果 | [[rules/lifecycle|生命周期规则]] |
| `publish propose` | [[rules/types|类型规则]]、[[rules/metadata|元数据规则]]、[[rules/citations|引用规则]]、[[rules/publish|发布规则]]、[[rules/quality|质量规则]]及选用模板 |
| `publish diff`、`publish apply`、`publish reject` | [[rules/publish|发布规则]]；`apply` 必须获得用户明确批准 |
| `build`、`build status` | [[rules/derived|派生层规则]] |
| `index`、`status`、`doctor` | 无额外语义规则 |

## 受控工作流

1. 新输入先按采集规则执行 `llm-wiki raw add`。
2. 使用 `llm-wiki query` 查找已有知识；SQLite 只决定候选，返回内容必须来自发布 Markdown。
3. Agent 读取 raw、已有 knowledge、相关规则和模板，判断新增、更新、拆分、合并或保留冲突。
4. 使用 `llm-wiki publish propose` 创建变更集并展示 `publish diff`。
5. 只有用户明确批准时执行 `publish apply`。
6. 发布后执行 `llm-wiki build`；索引失败不改变 knowledge 已经成为事实的状态。

AI 调用始终使用 `--json --no-interactive`，只处理版本化 JSON 字段和 `error.code`。stale 或冲突提案必须重新生成，不能强制覆盖。

面向人的使用入口见 [[LLM-WIKI]]。
