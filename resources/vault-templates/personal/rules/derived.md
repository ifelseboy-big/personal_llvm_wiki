# AI 派生层规则

1. `llm-wiki/` 只能读取 `knowledge/` 构建。
2. 派生文件没有事实权威，任何冲突以 `knowledge/` 为准。
3. 派生内容必须记录 knowledge ID、来源内容哈希、编译器版本和构建指纹。
4. 不手工修复派生文件；发现漂移时运行 `llm-wiki build --full`。
5. 删除派生目录后必须能够完整重建。
