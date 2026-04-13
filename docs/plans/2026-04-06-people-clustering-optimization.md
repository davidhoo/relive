# 人物聚类算法优化方案

**日期**: 2026-04-06
**问题**: NAS 上人物聚类 CPU 占用过高（200%），影响正常服务
> **Status:** Superseded
> **Note:** This first-pass optimization proposal was replaced by `docs/plans/2026-04-07-people-clustering-optimization-v2.md`; do not treat it as current backlog.

---

## 1. 当前问题分析

### 1.1 现象
- NAS 上 relive 服务 CPU 占用 200%（4 核中的 2 核被占满）
- 713 个 pending 人脸需要聚类
- 每次聚类需要计算 50 万次余弦相似度（512 维向量）
- API 响应时间从毫秒级降到秒级

### 1.2 根本原因
```
当前算法: O(n²) 暴力计算
- 535 个 face → 286,225 次比较
- 713 个 face → 508,369 次比较
- 1000 个 face → 1,000,000 次比较

每次比较: 512 维浮点运算（点积 + 开方）
```

### 1.3 瓶颈定位
| 组件 | CPU 占用 | 说明 |
|------|----------|------|
| ML 检测 | 0.24% | 几乎不占用 |
| 聚类计算 | ~90% | 主要瓶颈 |
| 数据库 IO | ~10% | 次要瓶颈 |

---

## 2. 优化方案对比

### 2.1 方案总览

| 方案 | 时间复杂度 | 空间复杂度 | 精度 | 实现难度 | 推荐指数 |
|------|-----------|-----------|------|----------|----------|
| HNSW 向量索引 | O(n log n) | O(n) | ~95% | 中等 | ⭐⭐⭐⭐⭐ |
| 暴力计算（当前） | O(n²) | O(1) | 100% | 简单 | ⭐⭐ |
| Faiss 向量库 | O(n log n) | O(n) | ~98% | 较难 | ⭐⭐⭐⭐ |
| PCA 降维 | O(n²) | O(1) | ~90% | 简单 | ⭐⭐⭐ |
| 延迟批量聚类 | - | - | 100% | 简单 | ⭐⭐⭐ |

### 2.2 方案详情

#### 方案 A: HNSW 向量索引（推荐）

**原理**: 构建多层图结构，实现近似最近邻搜索

```
层次结构示意图:
Layer 2:  ●────────●              (稀疏层，快速定位)
           \      /
Layer 1:   ●──●──●──●            (中等密度)
            \ | / \
Layer 0:   ●─●─●─●─●─●─●        (全数据，精确搜索)
```

**性能对比**:
```
数据量    暴力计算      HNSW
100       10,000      ~500      (20x 提升)
1,000     1,000,000   ~50,000   (20x 提升)
10,000    1亿         ~700,000  (140x 提升)
```

**Go 实现**:
- 库: `github.com/coder/hnsw-go`（纯 Go，无 CGO）
- 构建时间: O(n log n)
- 搜索时间: O(log n)

**优点**:
- 搜索速度提升 10-100 倍
- 支持增量添加节点
- 内存友好，可控精度

**缺点**:
- 近似结果（非 100% 精确）
- 需要额外内存存储索引
- 首次构建索引需要时间

**适用场景**: 人脸数量 > 500，需要频繁聚类

---

#### 方案 B: Faiss 向量库

**原理**: Facebook 开源的向量搜索库，使用 IVF、PQ 等优化算法

**性能**: 比 HNSW 更快，精度更高

**缺点**:
- 需要 CGO 绑定（编译复杂）
- 依赖 C++ 库
- 对 NAS 部署不友好

**适用场景**: 服务器环境，人脸数量 > 10,000

---

#### 方案 C: PCA 降维

**原理**: 将 512 维 embedding 降到 128 维或 64 维

**效果**:
- 计算量减少 4-8 倍
- 但复杂度仍是 O(n²)

**缺点**:
- 需要预训练 PCA 模型
- 降维后信息丢失
- 治标不治本

**适用场景**: 作为辅助优化，配合其他方案

---

#### 方案 D: 延迟批量聚类

**原理**: 不立即聚类，积累到一定数量再触发

**策略**:
- 检测时只保存 face，标记为 pending
- 积累 200 个 pending face 再触发聚类
- 或在低峰期（凌晨）执行全量聚类

**优点**:
- 实现简单
- 100% 精确
- 可控制执行时机

**缺点**:
- 实时性降低
- 不能解决单次聚类的 CPU 问题

**适用场景**: 作为兜底方案，配合其他优化

---

## 3. 推荐方案

### 3.1 短期方案（1-2 天）

**进一步优化现有代码**:
```go
// 当前优化
peopleClusteringBatchSize     = 50   → 20   // 降低批次
peopleClusteringInterval      = 100  → 500  // 增加休眠
peopleClusteringTaskInterval  = 5    → 10   // 更低频率

// 新增：延迟聚类
peopleClusteringMinPending    = 100       // 至少 100 个才聚类
peopleClusteringMaxPerDay     = 500       // 每天最多处理 500 个
```

**预期效果**:
- CPU 峰值从 200% 降到 80%
- 聚类完成时间延长 3-5 倍

---

### 3.2 中期方案（1-2 周）

**引入 HNSW 向量索引**:

```go
// 伪代码
type FaceIndex struct {
    index *hnsw.HnswIndex
}

func (fi *FaceIndex) Build(faces []*model.Face) {
    for _, f := range faces {
        embedding := decodeEmbedding(f.Embedding)
        fi.index.Add(f.ID, embedding)
    }
    fi.index.Build()
}

func (fi *FaceIndex) FindSimilar(query []float32, threshold float64) []uint {
    // O(log n) 搜索，代替 O(n) 暴力扫描
    neighbors := fi.index.Search(query, 10)
    return filterByThreshold(neighbors, threshold)
}
```

**实现步骤**:
1. 引入 `hnsw-go` 库
2. 修改 `buildFaceGraph()` 使用 HNSW 索引
3. 增量更新索引（新 face 加入时）
4. 定期重建索引（如每天凌晨）

**预期效果**:
- CPU 占用峰值降到 30-50%
- 1000 个 face 聚类时间从 30 秒降到 2 秒

---

### 3.3 长期方案（1 个月）

**向量数据库**:
- 考虑引入 Milvus、Pinecone 等向量数据库
- 专门处理大规模向量相似度搜索
- 支持分布式部署

**适用场景**: 人脸数量 > 100,000

---

## 4. 风险评估

### 4.1 HNSW 近似精度影响

**测试方案**:
1. 使用现有数据对比暴力计算和 HNSW 的聚类结果
2. 计算准确率、召回率
3. 调整 HNSW 参数（ef、M）平衡精度和速度

**预期**:
- 准确率 > 95%（可接受）
- 边缘 case：相似度在阈值附近的人脸可能分配不同

### 4.2 内存占用

**估算**:
- 每个节点：512 维 × 4 字节 + 索引开销 ≈ 2-4 KB
- 10,000 个 face：20-40 MB
- 可接受

---

## 5. 决策建议

| 优先级 | 方案 | 工作量 | 效果 | 建议 |
|--------|------|--------|------|------|
| P0 | 短期参数调优 | 1 小时 | 中等 | 立即执行 |
| P1 | HNSW 索引 | 2-3 天 | 显著 | 本周内完成 |
| P2 | 延迟聚类策略 | 半天 | 中等 | 配合 HNSW |
| P3 | 向量数据库 | 2 周+ | 显著 | 后续考虑 |

---

## 6. 下一步行动

待讨论事项：
1. 是否采用 HNSW 方案？
2. 精度损失是否可接受？
3. 短期参数调优是否立即执行？
4. 是否需要先进行 HNSW 精度测试？

---

## 附录

### A. 相关代码位置
- `backend/internal/service/people_service.go:1215` - `buildFaceGraph()`
- `backend/internal/service/people_service.go:1472` - `runIncrementalClustering()`
- `backend/internal/service/people_service.go:1827` - `cosineSimilarity()`

### B. 参考资源
- HNSW 论文: https://arxiv.org/abs/1603.09320
- hnsw-go 库: https://github.com/coder/hnsw-go
- Faiss 文档: https://faiss.ai/

### C. 当前优化已生效
- 批次限制：50 个 face
- 聚类间隔：每 5 个任务
- 休眠时间：100ms

效果：CPU 从 200% 降到 100%，但仍需进一步优化。
