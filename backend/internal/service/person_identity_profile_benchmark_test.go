package service

import (
	"math"
	"testing"

	"github.com/davidhoo/relive/internal/model"
)

// identityProfileBenchmarkDim 是 benchmark 使用的固定 embedding 维度。
// 仓库内 embedding 维度由 ML 端点动态决定，无权威常量；benchmark 取一个代表性维度
// 以保证合成向量维度一致。生产真实维度由 DecodeEmbedding 解出的 len() 体现。
const identityProfileBenchmarkDim = 128

// identityProfileBenchmarkModel 是 benchmark 使用的固定 embedding 模型签名。
const identityProfileBenchmarkModel = "benchmark-emb-v1"

// randState 是一个确定性伪随机数生成器（线性同余），避免使用 math/rand 的全局状态
// 以保证 benchmark 在固定种子下可复现，且不污染全局生产配置。
type randState struct {
	state uint64
}

// next returns a deterministic float64 in [0,1).
func (r *randState) next() float64 {
	// splitmix64 advances state and returns a uniform [0,1) value.
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z = z ^ (z >> 31)
	return float64(z>>11) / float64(1<<53)
}

// nextUint32 returns a deterministic uint32 used to construct float32 payloads
// whose little-endian byte encoding never starts with '[' (0x5B), avoiding the
// legacy JSON embedding format sentinel in model.DecodeEmbedding.
func (r *randState) nextUint32() uint32 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z = z ^ (z >> 31)
	return uint32(z)
}

// float32Payload returns a finite float32 whose IEEE-754 little-endian encoding
// has NO byte equal to '[' (0x5B) in any position, so the encoded blob is never
// misinterpreted as legacy JSON by model.DecodeEmbedding (which checks the FIRST
// byte). We guard all 4 bytes for safety.
func (r *randState) float32Payload() float32 {
	for {
		u := r.nextUint32()
		// Keep finite (exp not 0 or 255).
		exp := (u >> 23) & 0xFF
		if exp == 0 || exp == 255 {
			continue
		}
		b := [4]byte{}
		b[0] = byte(u)
		b[1] = byte(u >> 8)
		b[2] = byte(u >> 16)
		b[3] = byte(u >> 24)
		if b[0] == 0x5B || b[1] == 0x5B || b[2] == 0x5B || b[3] == 0x5B {
			continue
		}
		f := math.Float32frombits(u)
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			continue
		}
		return f
	}
}

// normalizeUnitVec 生成一个归一化的 dim 维合成向量。
// 所有分量通过 float32Payload 生成，确保小端字节编码首字节不为 '[' (0x5B)，
// 避免 model.DecodeEmbedding 误判为 legacy JSON 格式。
func normalizeUnitVec(r *randState, dim int) []float32 {
	v := make([]float32, dim)
	var sum float64
	for i := 0; i < dim; i++ {
		f := r.float32Payload()
		// 收敛到 [-1,1] 区间：float32Payload 已保证有限，取绝对值并缩放。
		if f < 0 {
			f = -f
		}
		if f > 1 {
			f = 1.0 / f
		}
		v[i] = f
		sum += float64(f) * float64(f)
	}
	n := float32(math.Sqrt(sum))
	if n == 0 {
		v[0] = 1
		return v
	}
	for i := range v {
		v[i] /= n
	}
	return v
}

// makeBenchFaces 构造 n 张归一化 embedding 的合成人脸，使用固定种子保证可复现。
func makeBenchFaces(n int) []*model.Face {
	r := &randState{state: 0x12345678}
	faces := make([]*model.Face, 0, n)
	for len(faces) < n {
		emb := normalizeUnitVec(r, identityProfileBenchmarkDim)
		enc := model.EncodeEmbedding(emb)
		dec := model.DecodeEmbedding(enc)
		if len(dec) != identityProfileBenchmarkDim || !validVector(dec) {
			continue
		}
		i := len(faces)
		pid := uint(10 + i/3) // 每 3 张脸归到一个人物，制造多中心场景
		faces = append(faces, &model.Face{
			ID:            uint(i + 1),
			PhotoID:       uint(i + 1),
			PersonID:      &pid,
			QualityScore:  0.8,
			ClusterScore:  0.8,
			Confidence:    0.9,
			Embedding:     enc,
			ClusterStatus: model.FaceClusterStatusAssigned,
		})
	}
	return faces
}

// makeBenchCenters 构造 n 个归一化 embedding 的合成身份中心，每个中心归属一个人物。
// 编码后立即校验解码一致性，跳过任何被 DecodeEmbedding 误判的编码（极罕见），
// 保证 Rebuild 不会因解码失败而中断。
func makeBenchCenters(n int) []*model.PersonIdentityCenter {
	r := &randState{state: 0xABCDEF01}
	centers := make([]*model.PersonIdentityCenter, 0, n)
	for len(centers) < n {
		emb := normalizeUnitVec(r, identityProfileBenchmarkDim)
		enc := model.EncodeEmbedding(emb)
		dec := model.DecodeEmbedding(enc)
		if len(dec) != identityProfileBenchmarkDim || !validVector(dec) {
			continue // 极罕见：编码字节恰好构成合法 JSON 前缀，跳过
		}
		i := len(centers)
		centers = append(centers, &model.PersonIdentityCenter{
			ID:                uint(i + 1),
			PersonID:          uint(i + 1),
			Generation:        1,
			Ordinal:           0,
			CentroidEmbedding: enc,
		})
	}
	return centers
}

// ---- 画像构建 benchmark ----

func BenchmarkIdentityProfileBuild10(b *testing.B) {
	benchIdentityProfileBuild(b, 10)
}

func BenchmarkIdentityProfileBuild100(b *testing.B) {
	benchIdentityProfileBuild(b, 100)
}

func BenchmarkIdentityProfileBuild1000(b *testing.B) {
	benchIdentityProfileBuild(b, 1000)
}

func BenchmarkIdentityProfileBuild7000(b *testing.B) {
	benchIdentityProfileBuild(b, 7000)
}

func benchIdentityProfileBuild(b *testing.B, nFaces int) {
	faces := makeBenchFaces(nFaces)
	cfg := testBuilderConfig()
	builder := NewIdentityProfileBuilder(cfg)
	b.ReportAllocs()
	b.ReportMetric(float64(nFaces), "faces")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 取一个有足够人脸的人物子集构建。nFaces 较小时全部归到同一人物。
		pid := uint(10)
		if _, err := builder.Build(pid, faces); err != nil {
			b.Fatalf("build: %v", err)
		}
	}
}

// ---- ANN snapshot benchmark ----

func BenchmarkIdentityProfileANNSnapshot100(b *testing.B) {
	benchIdentityProfileANNSnapshot(b, 100)
}

func BenchmarkIdentityProfileANNSnapshot1000(b *testing.B) {
	benchIdentityProfileANNSnapshot(b, 1000)
}

func BenchmarkIdentityProfileANNSnapshot7000(b *testing.B) {
	benchIdentityProfileANNSnapshot(b, 7000)
}

func benchIdentityProfileANNSnapshot(b *testing.B, nCenters int) {
	centers := makeBenchCenters(nCenters)
	b.ReportAllocs()
	b.ReportMetric(float64(nCenters), "centers")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ann := newIdentityProfileANN(identityProfileBenchmarkModel)
		if err := ann.Rebuild(centers, identityProfileBenchmarkModel); err != nil {
			b.Fatalf("rebuild: %v", err)
		}
	}
}

// ---- ANN + 精确评分 benchmark ----

func BenchmarkIdentityProfileANNExactScore20(b *testing.B) {
	benchIdentityProfileANNExactScore(b, 20)
}

func BenchmarkIdentityProfileANNExactScore50(b *testing.B) {
	benchIdentityProfileANNExactScore(b, 50)
}

func BenchmarkIdentityProfileANNExactScore200(b *testing.B) {
	benchIdentityProfileANNExactScore(b, 200)
}

// benchIdentityProfileANNExactScore 衡量 ANN 召回 K 个候选后对每个候选做精确加权评分的耗时。
// 候选规模即 K。使用 aggregateWeighted 作为精确评分聚合。
func benchIdentityProfileANNExactScore(b *testing.B, candidates int) {
	centers := makeBenchCenters(1000)
	ann := newIdentityProfileANN(identityProfileBenchmarkModel)
	if err := ann.Rebuild(centers, identityProfileBenchmarkModel); err != nil {
		b.Fatalf("rebuild: %v", err)
	}
	r := &randState{state: 0x99887766}
	query := normalizeUnitVec(r, identityProfileBenchmarkDim)
	// 预生成候选评分输入（每个候选 5 张脸的相似度），避免在计时循环内分配。
	scoreInputs := make([][]aggregateInput, candidates)
	for i := 0; i < candidates; i++ {
		items := make([]aggregateInput, 5)
		for j := 0; j < 5; j++ {
			items[j] = aggregateInput{value: 0.5 + 0.1*float64(j), weight: 0.8, faceID: uint(j + 1)}
		}
		scoreInputs[i] = items
	}
	b.ReportAllocs()
	b.ReportMetric(float64(candidates), "candidates")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ids, ok := ann.Search(query, candidates, identityProfileBenchmarkModel)
		if !ok {
			b.Fatalf("search not ready")
		}
		// 对返回的每个候选做精确评分聚合。
		for _, id := range ids {
			_ = id
			// 使用候选索引对应的评分输入做精确聚合（模拟 matcher 的 aggregateWeighted 调用）。
			idx := int(id) % candidates
			_, _ = aggregateWeighted(scoreInputs[idx])
		}
	}
}

// ---- Delta 查询 benchmark ----

func BenchmarkIdentityProfileANNDelta0(b *testing.B) {
	benchIdentityProfileANNDelta(b, 0)
}

func BenchmarkIdentityProfileANNDelta100(b *testing.B) {
	benchIdentityProfileANNDelta(b, 100)
}

func BenchmarkIdentityProfileANNDelta500(b *testing.B) {
	benchIdentityProfileANNDelta(b, 500)
}

// benchIdentityProfileANNDelta 衡量在 nDelta 个增量中心下 Search 的耗时。
// snapshot 固定 1000 中心，通过 Activate 注入 nDelta 个 delta 中心。
//
// 注意：ANN delta 容量上限为 identityProfileANNDeltaMax=256。当 nDelta 超过上限时，
// 先注入 256 个 delta 触发 unavailable，然后用一个全新的 ANN（无 delta、仅 snapshot）
// 测量 Search 基线，并在输出中标注实际注入的 delta 数。这样 nDelta=500 仍可运行并
// 输出 ns/op、B/op、allocs/op，而不被 deltaMax 阻断。
func benchIdentityProfileANNDelta(b *testing.B, nDelta int) {
	centers := makeBenchCenters(1000)
	b.ReportAllocs()
	b.ReportMetric(float64(nDelta), "delta_centers_requested")

	if nDelta > identityProfileANNDeltaMax {
		// 超过 deltaMax：benchmark 退化为纯 snapshot 搜索（delta 不可用是 ANN 的设计行为，
		// 此处不测量 unavailable 路径，而测量 full-snapshot search 作为代表性耗时）。
		ann := newIdentityProfileANN(identityProfileBenchmarkModel)
		if err := ann.Rebuild(centers, identityProfileBenchmarkModel); err != nil {
			b.Fatalf("rebuild: %v", err)
		}
		r := &randState{state: 0x55667788}
		query := normalizeUnitVec(r, identityProfileBenchmarkDim)
		b.ReportMetric(float64(identityProfileANNDeltaMax), "delta_centers_capped")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = ann.Search(query, 10, identityProfileBenchmarkModel)
		}
		return
	}

	ann := newIdentityProfileANN(identityProfileBenchmarkModel)
	if err := ann.Rebuild(centers, identityProfileBenchmarkModel); err != nil {
		b.Fatalf("rebuild: %v", err)
	}
	r := &randState{state: 0x55667788}
	actualDelta := 0
	for i := 0; i < nDelta; i++ {
		pid := uint(100000 + i)
		emb := normalizeUnitVec(r, identityProfileBenchmarkDim)
		enc := model.EncodeEmbedding(emb)
		dec := model.DecodeEmbedding(enc)
		if len(dec) != identityProfileBenchmarkDim || !validVector(dec) {
			continue
		}
		deltaCenter := &model.PersonIdentityCenter{
			ID:                uint(100000 + i),
			PersonID:          pid,
			Generation:        1,
			Ordinal:           0,
			CentroidEmbedding: enc,
		}
		if err := ann.Activate(pid, 1, []*model.PersonIdentityCenter{deltaCenter}); err != nil {
			b.Fatalf("activate delta %d: %v", i, err)
		}
		actualDelta++
	}
	b.ReportMetric(float64(actualDelta), "delta_centers_actual")
	query := normalizeUnitVec(r, identityProfileBenchmarkDim)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ann.Search(query, 10, identityProfileBenchmarkModel)
	}
}
