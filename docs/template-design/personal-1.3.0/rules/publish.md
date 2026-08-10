# 发布流程

Agent 根据 `AGENTS.md`、相关规则和模板判断知识语义；CLI 不解析自然语言治理规则，只保证来源、哈希、事务、路径和写入完整性。知识文件只能通过提案发布，SQLite、搜索结果、模型输出和模板草稿都不能直接改写已发布事实。

## 创建提案

1. 先查询已有知识，确认不是重复条目或应更新旧知识。
2. 使用模板创建草稿并补齐标题、类型、正文、引用和关系。
3. 运行模板检查，清除所有 `llm-wiki:prompt` 注释和未解析变量。
4. 创建提案：

```bash
llm-wiki publish propose \
	--source <raw-id> \
	--file draft.md
```

新建知识必须有 `type` 和 `title`。系统生成稳定 `id`、`published_at`、`updated_at`、`sources` 和内容指纹。

## 更新提案

更新已有知识时以稳定 `id` 定位，不以文件名或标题猜测目标。草稿中省略的用户属性表示保留原值；显式修改必须出现在 diff 中。系统属性仍由工具维护。

## 应用前检查

`publish apply` 前必须检查：

- 提案基于的旧内容指纹仍然有效，过期提案不得覆盖新版本。
- 标题与一级标题一致，没有 `{{...}}` 或模板提示残留。
- 必填章节有实质内容，`description` 不是占位文本。
- raw 引用存在且定位格式有效。
- 知识链接可解析；替代关系的对端在两份文档都发布后由 `doctor` 检查成对一致。
- 生命周期、有效期和类型专属字段合法。
- diff 只包含本次提案声明的变更。

确认 diff 后再执行：

```bash
llm-wiki publish apply <proposal-id>
```

应用成功后 CLI 自动增量更新 AI 派生物和 SQLite 索引。刷新失败会在成功响应的 warnings 中明确报告；此时 knowledge 已经发布，Agent 应按 warning 运行 `build` 或 `index update` 修复可重建层。禁止绕过提案直接编辑 `knowledge/` 后再把索引当作事实补救。

`publish apply` 成功后，`knowledge/` 中的发布 Markdown 立即成为唯一最终事实。后续派生构建或索引更新失败只能产生运行警告，不能让 SQLite 或 `llm-wiki/` 取代该文件。
