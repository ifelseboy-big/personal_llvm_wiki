# personal 1.2.0 模板设计稿

状态：已落地。此目录保留 personal 1.2.0 的设计基线；同版本资源已编译进当前二进制。旧 Vault 仍需显式执行 `template upgrade --apply`，不会被安装新二进制静默改写。

## 设计目标

1. 同一份内容模板可以被 Obsidian 核心 Templates 插件和 `llm-wiki template create` 使用。
2. `knowledge/` 中已发布 Markdown 是唯一最终事实源；`raw/`、`llm-wiki/` 和 SQLite 均不能替代它。
3. 知识具备明确类型、生命周期、稳定关联、来源元数据和结论级引用。
4. 初始化后同时为人和 AI 提供入口，不依赖 Obsidian 社区插件，不写入 `.obsidian/`。
5. Skill 每次工作流先定位并读取目标 Vault 的 `AGENTS.md`；CLI 不复制或解释语义治理规则。

## 参考设计及取舍

- [Obsidian Templates](https://obsidian.md/help/plugins/templates)：只采用核心变量 `{{title}}`、`{{date}}`、`{{time}}`，其余写作提示使用 Obsidian 注释。
- [Obsidian Properties](https://obsidian.md/help/properties)：用户属性保持原子值和列表；系统 `sources` 继续使用嵌套结构并由 CLI 管理。
- [Obsidian Bases](https://obsidian.md/help/bases)：只提供 Markdown Properties 的视图，不建立第二份事实数据库。
- [Diátaxis](https://diataxis.fr/)：采用 explanation、how-to、tutorial、reference 的职责边界。
- [MADR](https://adr.github.io/madr/)：decision 模板采用背景、驱动因素、备选方案、结果、后果、确认和复查结构。
- [Evergreen Notes](https://notes.andymatuschak.org/About_these_notes)：claim 强调原子结论，标题表达可引用的清晰主张。
- [Schema.org CreativeWork](https://schema.org/CreativeWork)：raw source 采用作者、发布者、来源日期、URL、许可和语言等字段。
- [W3C PROV-O](https://www.w3.org/TR/prov-o/)：保留 `raw → publish → knowledge` 的来源关系，但不引入完整本体。

## 最终目录

```text
personal-1.2.0/
├── AGENTS.md
├── LLM-WIKI.md
├── rules/
│   ├── capture.md
│   ├── metadata.md
│   ├── types.md
│   ├── lifecycle.md
│   ├── citations.md
│   ├── publish.md
│   ├── derived.md
│   └── quality.md
├── templates/
│   ├── raw/
│   │   ├── note.md
│   │   └── source.md
│   └── knowledge/
│       ├── claim.md
│       ├── concept.md
│       ├── guide.md
│       ├── tutorial.md
│       ├── reference.md
│       ├── decision.md
│       └── project.md
├── views/
│   ├── knowledge.base
│   ├── review.base
│   └── raw.base
└── template.toml
```

`DESIGN.md` 和 `TEMPLATE_SPEC.md` 是仓库设计材料，不进入用户 Vault。

## 不采用的设计

- 不建立 Notion 式中心数据库，也不把 SQLite 变成元数据事实源。
- 不使用 PARA 目录作为知识语义；目录只区分受管层和知识类型。
- 不依赖 Templater、Dataview 或其他社区插件。
- 不默认创建或修改 `.obsidian/`。
- 不把 `sources` 拍平成并行 ID/hash 列表。
- 不让 `project` 模板承担实时任务管理；它只记录有来源、有时间点的项目知识快照。

## 后续观察重点

1. 七类知识是否覆盖目标场景，是否需要删除或新增类型。
2. `lifecycle` 与 decision/project 专属状态是否容易理解。
3. raw 来源字段是否足够且不过度。
4. 命名脚注引用规则是否适合人和 AI。
5. Bases 是否应该默认进入初始化模板。
