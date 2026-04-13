# 人物聚类算法优化方案 v2

**日期**: 2026-04-07
**问题**: NAS 上人物聚类 CPU 占用过高（200%），API 响应变慢
> **Status:** Completed
> **Note:** The pre-decoding optimization path described here has landed on `main`; deferred options in this document remain intentionally unimplemented.

---

## 1. pprof 分析结论

### 1.1 测试数据
- 50 个 pending faces
- 66 个已分配 persons
- 66 组 prototypes（`selectPersonPrototypes` 选出，每组最多 5 个 face）

### 1.2 真实瓶颈（与初诊完全不同）

| 函数 | CPU 占比 | 说明 |
|------|----------|------|
| `decodeEmbedding` (json.Unmarshal) | **91.03%** | 绝对瓶颈 |
| `scoreComponentAgainstPerson` | 78.29% | 主要调用方 |
| `buildFaceGraph` | 13.47% | 次要调用方 |
| `cosineSimilarity` | 0.65% | 实际向量计算很快 |

**关键发现**: `json.Unmarshal` 占 91% CPU，而非 O(n²) 比较本身。

### 1.3 问题代码定位

```go
// 问题 1: buildFaceGraph 中每对 face 都重复解码
score := cosineSimilarity(
    decodeEmbedding(faces[i].Embedding),  // 重复解码
    decodeEmbedding(faces[j].Embedding),  // 重复解码
)

// 问题 2: scoreComponentAgainstPerson 中内层循环重复解码
for _, face := range component {
    embedding := decodeEmbedding(face.Embedding)  // 每次循环解码
    for _, prototype := range prototypes {
        score := cosineSimilarity(embedding, decodeEmbedding(prototype.Embedding))
    }
}

// 问题 3: selectDiversePrototypes 中也重复解码（本次 profile 未覆盖）
for _, f := range selected {
    selectedEmbeddings = append(selectedEmbeddings, decodeEmbedding(f.Embedding))
}
```

---

## 2. 优化方案

### 2.1 方案 A: 预解码 + 预计算（P0，唯一实施项）

**核心思路**: 在循环外一次性解码所有 embedding，用 `[]float32` 传递而非 `[]byte`，同时预计算 norm。

**改动范围**:
- `backend/internal/service/people_service.go`
- 新增内部结构体 `faceWithEmbedding`
- **必须包含**: `buildFaceGraph`、`scoreComponentAgainstPerson`、`selectDiversePrototypes`

**实现要点**:

```go
// 新增：带解码后 embedding 的内部结构
type faceWithEmbedding struct {
    face      *model.Face
    embedding []float32
    norm      float64  // 预计算模长
}

// 关键：保留当前语义，坏 embedding 的 face 仍要进入 graph（作为孤立点）
func (s *peopleService) buildFaceGraph(faces []*model.Face, linkThreshold float64) map[uint][]uint {
    graph := make(map[uint][]uint, len(faces))

    // 先给所有 face 建空邻接表（保留当前语义）
    for _, f := range faces {
        if f != nil && f.ID != 0 {
            graph[f.ID] = []uint{}
        }
    }

    // 预解码所有有效 embedding
    faceEmbeddings := make([]faceWithEmbedding, 0, len(faces))
    for _, f := range faces {
        if f == nil || f.ID == 0 {
            continue
        }
        emb := decodeEmbedding(f.Embedding)
        // 关键：即使 emb == nil，也要保留 graph[f.ID] = []uint{}
        if emb != nil {
            faceEmbeddings = append(faceEmbeddings, faceWithEmbedding{
                face:      f,
                embedding: emb,
                norm:      calculateNorm(emb),
            })
        }
    }

    // 使用预解码的 embedding 进行比较
    for i := 0; i < len(faceEmbeddings); i++ {
        for j := i + 1; j < len(faceEmbeddings); j++ {
            score := cosineSimilarityPrecomputed(
                faceEmbeddings[i].embedding, faceEmbeddings[i].norm,
                faceEmbeddings[j].embedding, faceEmbeddings[j].norm,
            )
            if score >= linkThreshold {
                graph[faceEmbeddings[i].face.ID] = append(...)
                graph[faceEmbeddings[j].face.ID] = append(...)
            }
        }
    }
    return graph
}
```

**预期效果**（预测，需验证）:
- CPU 占用显著下降（json.Unmarshal 占 91% → 预期 < 10%）
- 单次聚类时间减少（当前 661ms，目标 < 200ms）
- **零精度损失，结果与优化前完全一致**

---

### 2.2 方案 B: Prototype 缓存（暂不实施）

**状态**: 从 P1 降级为"profiling 后再决定"

**原因**:
1. 当前 profile 未覆盖 `ListAssignedPersonIDs` / `ListTopByPersonIDs` / `selectPersonPrototypes` 路径
2. 缓存一致性成本高：需覆盖合并、拆分、移动、解散、检测结果写回、人物同步删除等多个失效点
3. 方案 A 实施后需重新 profiling 确认是否还有瓶颈

---

### 2.3 方案 C: 延迟批量聚类（暂不实施）

**状态**: 可选，待方案 A 实测后决定

---

## 3. 实施计划

| 阶段 | 任务 | 工作量 | 验证方式 |
|------|------|--------|----------|
| 1 | 实现方案 A（含三个函数） | 2-3 小时 | **结果等价测试** + pprof 对比 |
| 2 | 跑 benchmark 对比 | 1 小时 | 优化前后性能数据对比 |
| 3 | NAS 实测 | 半天 | 整体 CPU 监控 |

### 3.1 结果等价测试（必须）

优化前后聚类结果必须完全一致：

```go
func TestClusteringEquivalence(t *testing.T) {
    // 使用相同输入
    pendingFaces := loadTestFaces()

    // 旧实现
    graphOld := buildFaceGraphOld(pendingFaces, threshold)
    componentsOld := findConnectedComponents(graphOld)

    // 新实现
    graphNew := buildFaceGraphNew(pendingFaces, threshold)
    componentsNew := findConnectedComponents(graphNew)

    // 验证：连通分量完全一致
    assertEqualComponents(t, componentsOld, componentsNew)

    // 验证：相似度分数完全一致（浮点误差范围内）
    assertEqualScores(t, scoresOld, scoresNew)
}
```

### 3.2 Benchmark 对比

```go
func BenchmarkClustering(b *testing.B) {
    faces := loadTestFaces()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        runIncrementalClustering()
    }
}
```

---

## 4. 风险评估

| 风险 | 等级 | 缓解措施 |
|------|------|----------|
| 坏 embedding face 被遗漏 | **高** | 确保即使 `emb == nil` 也保留 graph 节点 |
| 内存增加 | 低 | 50 faces × 512 × 4B = 100KB，可忽略 |
| 精度损失 | 无 | 结果等价测试保证 |
| 预期收益不达 | 中 | 先跑 benchmark 再部署 |

---

## 5. 附录

### 5.1 相关代码位置
- `backend/internal/service/people_service.go:1117-1236` - `selectDiversePrototypes()`（本次需修改）
- `backend/internal/service/people_service.go:1239-1274` - `buildFaceGraph()`
- `backend/internal/service/people_service.go:1320-1356` - `scoreComponentAgainstPerson()`
- `backend/internal/service/people_service.go:1844-1853` - `decodeEmbedding()`

### 5.2 测试命令
```bash
cd backend

# 结果等价测试
go test -v -run TestClusteringEquivalence ./internal/service/

# 性能对比
go test -bench=BenchmarkClustering -benchmem ./internal/service/

# pprof
go test -v -run TestPeopleClusteringProfile ./internal/service/
go tool pprof -top /tmp/people_clustering.prof
```

### 5.3 Code Review 修正记录
- **高风险修正**: 明确保留坏 embedding face 的 graph 节点语义
- **范围修正**: P0 必须包含 `selectDiversePrototypes`
- **方案 B 降级**: 从"计划实施"改为"profiling 后再决定"
- **测试要求**: 增加结果等价测试，不只看 pprof 占比
