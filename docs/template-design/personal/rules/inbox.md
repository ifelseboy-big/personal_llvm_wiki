# Inbox 规则

- `inbox/` 是可自由整理的临时工作区，不是可信事实源，不进入查询索引。
- 新建笔记使用 `YYYY-MM-DD-初步功能名称.md`，同名追加序号；ID 仅在元数据中。之后可改名、在 Inbox 内移动、编辑正文和用户属性、删除材料。
- 文本直接存入笔记；二进制或独立 note 的来源存入共享 `attachments/`。附件引用 `payload` 相对笔记所在目录；相对位置改变时须更新引用，CLI 不猜路径。共享 `attachments/` 和各级 `payload/` 目录中的原文不作为登记笔记扫描。
- 新笔记不写入正文/附件哈希；已有版本、哈希和字节数不构成读取或发布门槛，无须修改或删除。ID、Schema 和发布状态由 CLI 生成，只有 pending 条目进入新 Promotion，成功 consume 后标为 processed。
- 已有 `YYYY/MM/<id>/item.md` 与 `payload/` 可直接继续整理发布，不迁移、不强制改名、不重新导入；新命名规则只用于新建笔记。
- Organize 用 `inbox list/show` 确认授权范围，阅读当前正文及返回路径的附件；无附件时 `payload_path` 就是笔记。允许先按授权直接整理 Inbox，之后重新 show 获取当前 `item_hash`、`payload_hash`。
- 严格内容基线从 Promotion 开始。冻结计划后仍可编辑 Inbox，但内容变化或删除会让旧计划无法 apply，必须重新生成并审阅。
- 清理是独立授权：明确 ID 可删 pending 或 processed，`--processed` 只批量选择 processed。CLI 只删所选笔记，不递归删除目录或共享附件。
- 未登记的原始文件可暂存，使用 `inbox add` 登记后参与发布。回答知识问题只使用 Knowledge，不读取 Inbox。
