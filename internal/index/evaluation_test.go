package index

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/governance"
	"llm-wiki/internal/vault"
)

type retrievalEvalDocument struct {
	Key     string
	Title   string
	Body    string
	Tags    []string
	Aliases []string
}

type retrievalEvalQuery struct {
	Natural   string
	Rewritten string
	Relevant  string
}

type retrievalMetrics struct {
	RecallAt5    float64
	PrecisionAt5 float64
	MRR          float64
	NDCGAt5      float64
}

func TestRetrievalQualityEvaluation(t *testing.T) {
	documents := []retrievalEvalDocument{
		{Key: "stable-ir", Title: "LLVM 稳定中间表示", Aliases: []string{"LLVM IR"}, Tags: []string{"编译器架构"}, Body: "LLVM 的稳定 IR 连接 Clang 前端和目标后端，通过统一中间表示实现组件解耦。"},
		{Key: "pass-manager", Title: "LLVM 新 Pass Manager", Tags: []string{"优化器"}, Body: "新 Pass Manager 缓存 analysis 分析结果；IR 发生变化时必须执行 analysis invalidation，使失效范围保持正确。"},
		{Key: "opaque-pointers", Title: "LLVM Opaque Pointers 迁移", Aliases: []string{"opaque pointer"}, Body: "Opaque Pointers 移除指针元素类型。迁移 typed pointer 代码时应从 load、store 或 GEP 指令获取实际类型。"},
		{Key: "orc-jit", Title: "LLVM ORC JIT", Tags: []string{"JIT"}, Body: "ORC JIT 使用 ExecutionSession、JITDylib 和 MaterializationUnit 组织按需编译、符号解析与并发物化。"},
		{Key: "thinlto", Title: "ThinLTO 增量链接", Body: "ThinLTO 通过模块摘要索引执行跨模块导入，并利用缓存支持增量链接和并行后端优化。"},
		{Key: "tablegen", Title: "TableGen 记录生成", Body: "TableGen 使用 record、class 和 multiclass 描述指令、寄存器及匹配规则，再由后端生成器输出 C++ 表。"},
		{Key: "mlir", Title: "MLIR Dialect 转换", Aliases: []string{"方言转换"}, Body: "MLIR Dialect Conversion 由 ConversionTarget、RewritePatternSet 和 TypeConverter 共同定义合法化与类型转换。"},
		{Key: "sanitizer", Title: "AddressSanitizer 内存检测", Aliases: []string{"ASan"}, Body: "AddressSanitizer 通过 shadow memory 和编译期插桩检测越界访问、use after free 等内存错误。"},
		{Key: "dwarf", Title: "LLVM DWARF 调试信息", Body: "LLVM 调试元数据在代码生成阶段映射为 DWARF DIE、location list 和 line table，供调试器恢复源码位置。"},
		{Key: "clang-modules", Title: "Clang Modules 模块缓存", Body: "Clang Modules 将头文件映射为模块并生成 PCM；module cache key 必须包含编译选项和依赖状态。"},
		{Key: "lld", Title: "LLD 符号解析", Body: "LLD 链接器按强弱定义、可见性和归档抽取规则解析 symbol，并在重定位阶段写入最终地址。"},
		{Key: "libcxx-abi", Title: "libc++ ABI 稳定性", Body: "libc++ 通过 ABI version、inline namespace 和兼容宏管理标准库二进制兼容性。"},
		{Key: "meeting", Title: "编译器团队会议记录", Body: "会议讨论 LLVM 发布排期、会议室安排和下周值班，不包含可复用的技术结论。"},
	}
	queries := []retrievalEvalQuery{
		{Natural: "LLVM 前端和后端靠什么实现解耦？", Rewritten: "LLVM 稳定 IR 前端 后端 解耦", Relevant: "stable-ir"},
		{Natural: "新 Pass Manager 修改 IR 后怎样处理分析缓存？", Rewritten: "Pass Manager IR analysis invalidation 分析缓存失效", Relevant: "pass-manager"},
		{Natural: "typed pointer 迁移到不透明指针时从哪里取类型？", Rewritten: "Opaque Pointers typed pointer load store GEP 类型", Relevant: "opaque-pointers"},
		{Natural: "ORC 的按需编译和符号物化由哪些对象组织？", Rewritten: "ORC JIT ExecutionSession JITDylib MaterializationUnit", Relevant: "orc-jit"},
		{Natural: "哪种 LTO 模式支持摘要索引和增量缓存？", Rewritten: "ThinLTO 模块摘要索引 增量缓存", Relevant: "thinlto"},
		{Natural: "LLVM 后端的指令描述怎样生成 C++ 表？", Rewritten: "TableGen record multiclass 指令 C++ 生成", Relevant: "tablegen"},
		{Natural: "MLIR 方言合法化和类型转换使用什么组件？", Rewritten: "MLIR Dialect Conversion ConversionTarget TypeConverter", Relevant: "mlir"},
		{Natural: "ASan 如何检测 use after free？", Rewritten: "AddressSanitizer ASan shadow memory use after free", Relevant: "sanitizer"},
		{Natural: "LLVM 调试元数据最后如何变成源码位置？", Rewritten: "LLVM DWARF DIE location list line table", Relevant: "dwarf"},
		{Natural: "Clang 的 PCM 为什么需要模块缓存键？", Rewritten: "Clang Modules PCM module cache key", Relevant: "clang-modules"},
		{Natural: "LLD 根据什么规则选择强弱符号？", Rewritten: "LLD 链接器 强弱 symbol 符号解析", Relevant: "lld"},
		{Natural: "libc++ 怎样维护标准库二进制兼容？", Rewritten: "libc++ ABI version inline namespace 二进制兼容", Relevant: "libcxx-abi"},
	}

	cfg, ids := createRetrievalEvaluationWiki(t, documents)
	natural := evaluateRetrieval(t, cfg, ids, queries, false)
	rewritten := evaluateRetrieval(t, cfg, ids, queries, true)
	t.Logf("natural recall@5=%.3f precision@5=%.3f MRR=%.3f nDCG@5=%.3f", natural.RecallAt5, natural.PrecisionAt5, natural.MRR, natural.NDCGAt5)
	t.Logf("rewritten recall@5=%.3f precision@5=%.3f MRR=%.3f nDCG@5=%.3f", rewritten.RecallAt5, rewritten.PrecisionAt5, rewritten.MRR, rewritten.NDCGAt5)
	if natural.RecallAt5 < 0.80 || natural.PrecisionAt5 < 0.16 || natural.MRR < 0.70 || natural.NDCGAt5 < 0.75 {
		t.Fatalf("natural-language retrieval quality regressed: %#v", natural)
	}
	if rewritten.RecallAt5 < 0.95 || rewritten.PrecisionAt5 < 0.19 || rewritten.MRR < 0.90 || rewritten.NDCGAt5 < 0.90 {
		t.Fatalf("rewritten-query retrieval quality regressed: %#v", rewritten)
	}
}

func BenchmarkSearchScale(b *testing.B) {
	for _, count := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("documents-%d", count), func(b *testing.B) {
			cfg := createBenchmarkWiki(b, count)
			query := fmt.Sprintf("UniqueNeedle%05d", count/2)
			b.ReportMetric(float64(count), "documents")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := Search(cfg, query, 8)
				if err != nil || len(result.Candidates) == 0 {
					b.Fatalf("search failed at scale %d: %#v %v", count, result, err)
				}
			}
		})
	}
}

func createBenchmarkWiki(tb testing.TB, count int) *config.Instance {
	tb.Helper()
	root := filepath.Join(tb.TempDir(), "retrieval-benchmark")
	initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "retrieval-benchmark", Template: "personal"})
	if err != nil {
		tb.Fatal(err)
	}
	cfg := initialized.Config
	base := time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		writeEvaluationKnowledge(tb, cfg, base.Add(time.Duration(i+1)*time.Millisecond), fmt.Sprintf("Synthetic knowledge %05d", i), fmt.Sprintf("UniqueNeedle%05d deterministic benchmark content for LLVM indexing and retrieval.", i), nil, nil)
	}
	if _, err := Rebuild(cfg); err != nil {
		tb.Fatal(err)
	}
	return cfg
}

func createRetrievalEvaluationWiki(tb testing.TB, documents []retrievalEvalDocument) (*config.Instance, map[string]string) {
	tb.Helper()
	root := filepath.Join(tb.TempDir(), "retrieval-evaluation")
	initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "retrieval-evaluation", Template: "personal"})
	if err != nil {
		tb.Fatal(err)
	}
	cfg := initialized.Config
	base := time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC)
	ids := make(map[string]string, len(documents))
	for i, item := range documents {
		ids[item.Key] = writeEvaluationKnowledge(tb, cfg, base.Add(time.Duration(i+1)*time.Millisecond), item.Title, item.Body, item.Tags, item.Aliases)
	}
	if _, err := Rebuild(cfg); err != nil {
		tb.Fatal(err)
	}
	return cfg, ids
}

func writeEvaluationKnowledge(tb testing.TB, cfg *config.Instance, at time.Time, title, text string, tags, aliases []string) string {
	tb.Helper()
	id, err := document.NewID("know", at)
	if err != nil {
		tb.Fatal(err)
	}
	body := []byte(fmt.Sprintf("# %s\n\n%s\n", title, text))
	governanceVersion, err := governance.Version(cfg)
	if err != nil {
		tb.Fatal(err)
	}
	meta := document.Metadata{
		SchemaVersion: document.CurrentSchema, ID: id, Type: "concept", Title: title, Status: "published",
		PublishedAt: at.Format(time.RFC3339), UpdatedAt: at.Format(time.RFC3339), ContentHash: document.HashBytes(body),
		Tags: tags, Aliases: aliases, GovernanceVersion: governanceVersion,
		Lineage: []document.LineageRef{{InboxID: "inbox_01arz3ndektsv4rrffq69g5fav", PayloadHash: document.HashBytes([]byte("evaluation-source")), Source: "retrieval-evaluation", CapturedAt: at.Format(time.RFC3339)}},
		Extra:   map[string]any{"category": "learning", "description": "Retrieval evaluation fixture", "lifecycle": "current"},
	}
	path := filepath.Join(cfg.Root, filepath.FromSlash(document.KnowledgePath(cfg.Paths.Knowledge, meta)))
	if err := document.Write(path, meta, body); err != nil {
		tb.Fatal(err)
	}
	return id
}

func evaluateRetrieval(t *testing.T, cfg *config.Instance, ids map[string]string, queries []retrievalEvalQuery, rewritten bool) retrievalMetrics {
	t.Helper()
	var recall, precision, reciprocalRank, ndcg float64
	for _, item := range queries {
		query := item.Natural
		if rewritten {
			query = item.Rewritten
		}
		result, err := Search(cfg, query, 10)
		if err != nil {
			t.Fatalf("query %q failed: %v", query, err)
		}
		seen := map[string]bool{}
		var ranked []string
		for _, candidate := range result.Candidates {
			if !seen[candidate.KnowledgeID] {
				seen[candidate.KnowledgeID] = true
				ranked = append(ranked, candidate.KnowledgeID)
			}
		}
		relevantID := ids[item.Relevant]
		rank := -1
		for i, id := range ranked {
			if id == relevantID {
				rank = i
				break
			}
		}
		if rank >= 0 && rank < 5 {
			recall++
			precision += 1.0 / 5.0
			reciprocalRank += 1.0 / float64(rank+1)
			ndcg += 1.0 / math.Log2(float64(rank+2))
		}
	}
	count := float64(len(queries))
	return retrievalMetrics{RecallAt5: recall / count, PrecisionAt5: precision / count, MRR: reciprocalRank / count, NDCGAt5: ndcg / count}
}
