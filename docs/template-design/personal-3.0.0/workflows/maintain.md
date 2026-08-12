# Maintain Workflow

触发：用户要求检查或处理过期、冲突、重复、错误、需要替代或需要更新的 Knowledge。

1. 只用 `query/show` 和 `doctor` 获取可信事实与治理诊断；按 `content-pack.json` 的生命周期声明识别失效、争议、未生效、过期和复核到期。
2. 对重复项判断合并主文档与保留边界；对冲突保留证据和分歧，不静默覆盖。
3. 准备更新或新建草稿；替代关系必须在同一 Promotion 中形成策略声明的 reciprocal 稳定 ID。
4. 通过 Publish Workflow 冻结、展示完整 diff并等待批准。

Maintain 不直接重命名、移动、修改或删除 `knowledge/`。删除历史事实不能代替生命周期或替代关系。
