package service

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingListPrototypeFaceRepo 包装一个真实 FaceRepository，但 ListPrototypeEmbeddings
// 返回注入的错误，用于注入构建失败。
type failingListPrototypeFaceRepo struct {
	repository.FaceRepository
	failErr error
	called  int32
}

func (f *failingListPrototypeFaceRepo) ListPrototypeEmbeddings(personIDs []uint, perPerson int) ([]*model.Face, error) {
	atomic.AddInt32(&f.called, 1)
	return nil, f.failErr
}
// TestMergeSuggestionANN_DirtyDuringBuildRemainsPending 验证 rebuild 期间发生 MarkDirty
// 时，已完成索引会被发布（供查询降级），但 dirty 保持为 true（pending），下一次
// ensureANNIndex 会重建到最新 generation。
func TestMergeSuggestionANN_DirtyDuringBuildRemainsPending(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	// 首次构建，建立干净 baseline。
	_, err := inner.ensureANNIndex()
	require.NoError(t, err)
	require.False(t, inner.annDirty)

	// 注入 hook：在 buildANNIndex 完成 DB 读取、即将建图前推进 generation（模拟并发 MarkDirty）。
	inner.annBuildHook = func() {
		inner.annMu.Lock()
		inner.annGeneration++
		inner.annDirty = true
		inner.annMu.Unlock()
	}

	targetGen := inner.annGeneration
	// 触发一次 rebuild：标记 dirty 后调用 ensureANNIndex。
	require.NoError(t, svc.MarkDirty("concurrent change during build"))

	idx, err := inner.ensureANNIndex()
	require.NoError(t, err)
	require.NotNil(t, idx, "completed index must be published despite concurrent dirty")

	inner.annMu.Lock()
	pending := inner.annDirty
	annBuilding := inner.annBuilding
	inner.annMu.Unlock()
	inner.annBuildHook = nil

	assert.False(t, annBuilding, "build must not be left running")
	assert.True(t, pending, "dirty must remain pending when generation advanced during build")

	// 下一次 ensureANNIndex 必须重建到最新 generation（MarkDirty +1，hook +1）。
	secondIdx, err := inner.ensureANNIndex()
	require.NoError(t, err)
	inner.annMu.Lock()
	assert.False(t, inner.annDirty, "second rebuild with no concurrent dirty must clear dirty")
	assert.Greater(t, inner.annGeneration, targetGen)
	inner.annMu.Unlock()
	assert.NotSame(t, idx, secondIdx)
}

// TestMergeSuggestionANN_ConcurrentEnsureBuildsOnce 验证多个并发 ensureANNIndex 调用
// 不会启动多个构建：第一个调用者构建，其余调用者拿到最终发布的同一索引，
// 不会重复执行 ListAll / ListPrototypeEmbeddings / HNSW 建图。
func TestMergeSuggestionANN_ConcurrentEnsureBuildsOnce(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))
	createSuggestionTestPerson(t, repos, "stranger", ann512(1, 1.0))

	var buildCount int32
	inner.annBuildHook = func() {
		atomic.AddInt32(&buildCount, 1)
		// 拖慢一点，让其它并发调用者有机会在构建期间进入 ensureANNIndex。
		time.Sleep(50 * time.Millisecond)
	}

	const callers = 8
	var wg sync.WaitGroup
	results := make([]*annIndex, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			idx, err := inner.ensureANNIndex()
			results[i] = idx
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()
	inner.annBuildHook = nil

	for i, err := range errs {
		require.NoError(t, err, "caller %d", i)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&buildCount),
		"concurrent ensureANNIndex must trigger exactly one build")
	first := results[0]
	require.NotNil(t, first)
	for i, r := range results {
		require.NotNil(t, r, "caller %d got nil index", i)
		assert.Same(t, first, r, "caller %d should share the single published index", i)
	}
}

// TestMergeSuggestionANN_FailedBuildKeepsOldIndex 验证构建失败时保留旧 annIdx 继续服务，
// 保持 dirty，不发布半成品，annBuilding 复位。
func TestMergeSuggestionANN_FailedBuildKeepsOldIndex(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	// 先成功构建一次，建立可服务的旧索引。
	oldIdx, err := inner.ensureANNIndex()
	require.NoError(t, err)
	require.NotNil(t, oldIdx)

	// 通过包装 FaceRepository 注入 ListPrototypeEmbeddings 失败。
	// 复用现有 bg repos（测试构造时若未设置 bgDB，bgRepos 走共享 repos），只替换 faceRepo。
	baseFaceRepo := inner.faceRepo
	inner.faceRepo = &failingListPrototypeFaceRepo{
		FaceRepository: baseFaceRepo,
		failErr:        errors.New("simulated DB failure"),
	}
	defer func() { inner.faceRepo = baseFaceRepo }()

	require.NoError(t, svc.MarkDirty("trigger rebuild that will fail"))

	returned, err := inner.ensureANNIndex()
	require.Error(t, err)
	assert.Same(t, oldIdx, returned, "must return old index on build failure for degraded service")

	inner.annMu.Lock()
	dirty := inner.annDirty
	building := inner.annBuilding
	curIdx := inner.annIdx
	inner.annMu.Unlock()

	assert.True(t, dirty, "dirty must remain true after failed build")
	assert.False(t, building, "annBuilding must be reset after failed build")
	assert.Same(t, oldIdx, curIdx, "published index must still be the old one (no half-built publish)")
}

// TestMergeSuggestionANN_SameGenerationSuccessClearsDirty 验证构建期间没有新 MarkDirty
// 时（targetGeneration == annGeneration），构建成功后清除 dirty。
func TestMergeSuggestionANN_SameGenerationSuccessClearsDirty(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	_, err := inner.ensureANNIndex()
	require.NoError(t, err)
	require.False(t, inner.annDirty)

	// 注入 hook：构建期间不推进 generation（模拟无并发变化）。
	inner.annBuildHook = func() {
		// no-op: no MarkDirty during build
	}

	require.NoError(t, svc.MarkDirty("clean rebuild"))
	genAtMark := inner.annGeneration

	idx, err := inner.ensureANNIndex()
	require.NoError(t, err)
	require.NotNil(t, idx)
	inner.annBuildHook = nil

	inner.annMu.Lock()
	dirty := inner.annDirty
	curGen := inner.annGeneration
	inner.annMu.Unlock()

	assert.Equal(t, genAtMark, curGen, "no concurrent dirty must not advance generation")
	assert.False(t, dirty, "same-generation success must clear dirty")
}
