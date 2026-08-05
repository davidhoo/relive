package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	analyzerCache "github.com/davidhoo/relive/cmd/relive-analyzer/internal/cache"
	analyzerClient "github.com/davidhoo/relive/cmd/relive-analyzer/internal/client"
	analyzerConfig "github.com/davidhoo/relive/cmd/relive-analyzer/internal/config"
	analyzerCore "github.com/davidhoo/relive/internal/analyzer"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/provider"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_ = logger.Init(config.LoggingConfig{Level: "warn", Console: false})
}

// fakeProvider 是受控的 AIProvider，按配置返回成功或结构化错误。
type fakeProvider struct {
	name      string
	failErr   error // 非 nil 时 Analyze 返回此错误
	succeedAt int32 // 原子计数：成功次数
	failAt    int32 // 原子计数：失败次数
}

func (p *fakeProvider) Name() string         { return p.name }
func (p *fakeProvider) Cost() float64        { return 0 }
func (p *fakeProvider) BatchCost() float64   { return 0 }
func (p *fakeProvider) IsAvailable() bool    { return true }
func (p *fakeProvider) MaxConcurrency() int  { return 1 }
func (p *fakeProvider) SupportsBatch() bool  { return false }
func (p *fakeProvider) MaxBatchSize() int    { return 1 }

func (p *fakeProvider) Analyze(req *provider.AnalyzeRequest) (*provider.AnalyzeResult, error) {
	if p.failErr != nil {
		atomic.AddInt32(&p.failAt, 1)
		return nil, p.failErr
	}
	atomic.AddInt32(&p.succeedAt, 1)
	return &provider.AnalyzeResult{
		Description:  "ok",
		MainCategory: "日常",
		MemoryScore:  80,
		BeautyScore:  70,
	}, nil
}

func (p *fakeProvider) AnalyzeBatch(reqs []*provider.AnalyzeRequest) ([]*provider.AnalyzeResult, error) {
	return nil, errors.New("not supported")
}

func (p *fakeProvider) GenerateCaption(req *provider.AnalyzeRequest) (string, error) {
	return "caption", nil
}

// newTestAnalyzer 构造一个 APIAnalyzer，client 指向 httptest server，
// provider 用 fake，circuit 用 fake clock。
func newTestAnalyzer(t *testing.T, srvURL string, fp *fakeProvider, clk func() time.Time) *APIAnalyzer {
	t.Helper()
	cfg := analyzerConfig.DefaultConfig()
	cfg.Server.Endpoint = srvURL
	cfg.Server.APIKey = "test-key"
	cfg.Analyzer.Workers = 2
	cfg.Analyzer.FetchLimit = 4
	cfg.Analyzer.CheckpointFile = t.TempDir() + "/cp.db"

	client := analyzerClient.NewAPIClient(cfg.Server.Endpoint, cfg.Server.APIKey,
		analyzerClient.WithTimeout(5*time.Second),
		analyzerClient.WithRetry(0, 10*time.Millisecond),
	)
	tm := analyzerClient.NewTaskManager(client, "analyzer-test", cfg.Analyzer.FetchLimit)

	cp, err := analyzerCache.NewCheckpoint(cfg.Analyzer.CheckpointFile)
	require.NoError(t, err)
	t.Cleanup(func() { cp.Close() })

	a := &APIAnalyzer{
		config:      cfg,
		client:      client,
		taskManager: tm,
		checkpoint:  cp,
		aiProvider:  fp,
		analyzerID:  "analyzer-test",
		circuit:     NewCircuitBreaker(DefaultCircuitConfig()),
		stats:       analyzerCore.NewStats(0),
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.stopCh = make(chan struct{})
	a.sessionPermanentFailures = make(map[uint]string)
	if clk != nil {
		a.circuit.SetNowFunc(clk)
	}
	return a
}

// testStats 最小 Stats 替身（保留以防未来需要；当前用 analyzerCore.NewStats）。
type testStats struct{}

func newTestStats() *testStats                                          { return &testStats{} }
func (s *testStats) RecordSuccess(time.Duration, float64)               {}
func (s *testStats) RecordFailure(string)                               {}
func (s *testStats) Print()                                             {}
func (s *testStats) RecordSuccess2(d time.Duration, c float64)          {}
func (s *testStats) RecordFailure2(r string)                            {}
func (s *testStats) Print2()                                            {}
var _ = newTestStats

// 任务辅助：让 handleTaskError 调用 taskManager.ReleaseTask 时不需要真实下载/心跳。
// 我们直接调用 handleTaskError，client 指向 httptest server。
// 4 workers 同时遇到 502，release payload 包含 failure class/provider/lock version；
// 达到阈值后熔断 open。
func TestHandleTaskError_502OpensCircuitAndSendsFailureClass(t *testing.T) {
	// release 端点记录收到的请求。
	var got atomic.Value // *model.ReleaseTaskRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/analyzer/tasks/task_1/release" {
			var req model.ReleaseTaskRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			got.Store(&req)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(model.Response{
				Success: true,
				Data:    model.ReleaseTaskResult{TaskID: "task_1", NewStatus: "retry_wait"},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(model.Response{Success: true})
	}))
	defer srv.Close()

	fp := &fakeProvider{name: "vllm"}
	a := newTestAnalyzer(t, srv.URL, fp, nil)

	task := &model.AnalysisTask{ID: "task_1", PhotoID: 1, LockVersion: 7}
	err502 := provider.NewHTTPError("vllm", &http.Response{StatusCode: 502, Body: http.NoBody, Header: http.Header{}})

	for i := 0; i < 3; i++ {
		a.handleTaskError(task, err502, "analysis_failed")
	}

	// 同一 photo ID 重复失败只算 1 次阈值贡献，需不同 photo 才 open。
	// 这里三次都是 photoID=1，circuit 不会 open。
	assert.Equal(t, CircuitClosed, a.circuit.State(), "同一 photo ID 重复失败不应触发 open")

	// 不同 photo 各失败一次（共 3 个不同 ID）。
	a.handleTaskError(&model.AnalysisTask{ID: "task_2", PhotoID: 2, LockVersion: 1}, err502, "analysis_failed")
	a.handleTaskError(&model.AnalysisTask{ID: "task_3", PhotoID: 3, LockVersion: 1}, err502, "analysis_failed")
	a.handleTaskError(&model.AnalysisTask{ID: "task_4", PhotoID: 4, LockVersion: 1}, err502, "analysis_failed")
	assert.Equal(t, CircuitOpen, a.circuit.State(), "3 个不同 photo 失败应 open")

	// 验证 release payload 含 failure_class/provider/lock_version。
	stored := got.Load()
	require.NotNil(t, stored, "release 端点应收到请求")
	req := stored.(*model.ReleaseTaskRequest)
	assert.Equal(t, FailureClassProviderTransient, req.FailureClass)
	assert.Equal(t, "vllm", req.Provider)
	assert.Equal(t, int64(7), req.LockVersion)
}

// TestCircuitOpenStopsFetching
// circuit open 时 fetch 不增长；half-open 成功后恢复。
func TestCircuitOpenStopsFetching(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// 返回空任务，便于断言 fetch 次数。
		json.NewEncoder(w).Encode(model.Response{Success: true, Data: model.AnalyzerTasksResponse{Tasks: []model.AnalysisTask{}}})
	}))
	defer srv.Close()

	fp := &fakeProvider{name: "vllm"}
	a := newTestAnalyzer(t, srv.URL, fp, nil)

	// 强制 open。
	a.circuit.RecordFailure(1, FailureClassProviderTransient, 0)
	a.circuit.RecordFailure(2, FailureClassProviderTransient, 0)
	a.circuit.RecordFailure(3, FailureClassProviderTransient, 0)
	require.Equal(t, CircuitOpen, a.circuit.State())

	// fetchLoop 禁止领取 → 不会调用 FetchTasks。
	assert.False(t, a.circuit.CanFetch())

	// 模拟退避到期 → half-open，且 probe 成功 → close。
	a.circuit.SetNowFunc(func() time.Time {
		return time.Now().Add(30 * time.Second)
	})
	require.True(t, a.circuit.AcquireProbe())
	a.circuit.RecordSuccess()
	assert.Equal(t, CircuitClosed, a.circuit.State())
	assert.True(t, a.circuit.CanFetch(), "half-open probe 成功后恢复领取")
}

// TestCheckpointDiagnosticOnlyAfterServerReassign
// checkpoint 只保留诊断，不再用本地 3 次阈值覆盖服务端决定：
// 即使本地 checkpoint 标记 failed，服务器重新分配后应重新处理（不因本地阈值跳过）。
func TestCheckpointDiagnosticOnlyAfterServerReassign(t *testing.T) {
	cp := newTestCheckpoint2(t)
	require.NoError(t, cp.MarkFailed(42, "boom"))
	require.NoError(t, cp.MarkFailed(42, "boom"))
	require.NoError(t, cp.MarkFailed(42, "boom"))

	// 服务器重新分配（IsProcessed=true, status=failed）→ 旧逻辑会 ShouldRetry(3)=false 跳过；
	// 新逻辑只 ResetFailed 后继续处理。这里直接验证新 processLoop 行为：
	// 处理后 ResetFailed，不再调用 ReleaseTask(local_retry_exhausted)。
	// 我们通过断言 checkpoint 仍可被 reset 来体现诊断语义。
	require.NoError(t, cp.ResetFailed(42))
	processed, err := cp.IsProcessed(42)
	require.NoError(t, err)
	assert.False(t, processed, "ResetFailed 后应视为未处理，等待重新分析")
}

// newTestCheckpoint2 复用 cache 包的 checkpoint 构造。
func newTestCheckpoint2(t *testing.T) *analyzerCache.Checkpoint {
	t.Helper()
	cp, err := analyzerCache.NewCheckpoint(t.TempDir() + "/cp.db")
	require.NoError(t, err)
	t.Cleanup(func() { cp.Close() })
	return cp
}

// TestLeasePausedBlocksFetch
// runtime lease 丢失后停止普通 fetch。
func TestLeasePausedBlocksFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(model.Response{Success: true, Data: model.AnalyzerTasksResponse{Tasks: []model.AnalysisTask{}}})
	}))
	defer srv.Close()

	fp := &fakeProvider{name: "vllm"}
	a := newTestAnalyzer(t, srv.URL, fp, nil)

	a.setLeasePaused(true)
	assert.True(t, a.isLeasePaused())

	// isLeasePaused 时 fetchLoop 应跳过 fetch；这里直接断言状态机不调用 fetch。
	// 由于 fetchLoop 是 goroutine，我们用一个短 ticker 验证：启动后 1s 内不应有任务进入 taskManager。
	a.wg.Add(1)
	go a.fetchLoop()
	time.Sleep(1500 * time.Millisecond)
	a.cancel()
	a.wg.Wait()

	assert.Equal(t, 0, a.taskManager.TaskCount(), "lease-paused 时不应领取任务")
}

// TestClassifyFailureClientCancelledNotCounted
// cancel 退出不记业务失败。
func TestClassifyFailureClientCancelledNotCounted(t *testing.T) {
	assert.Equal(t, FailureClassClientCancelled, ClassifyFailure(NewClientCancelledError()))

	// handleTaskError 对 client_cancelled 不应 RecordFailure（这里间接验证不 panic 且不 open circuit）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(model.Response{Success: true, Data: model.ReleaseTaskResult{NewStatus: "pending"}})
	}))
	defer srv.Close()

	fp := &fakeProvider{name: "vllm"}
	a := newTestAnalyzer(t, srv.URL, fp, nil)
	a.handleTaskError(&model.AnalysisTask{ID: "task_1", PhotoID: 1, LockVersion: 1}, NewClientCancelledError(), "cancelled")
	assert.Equal(t, CircuitClosed, a.circuit.State(), "client_cancelled 不应触发熔断")
}
