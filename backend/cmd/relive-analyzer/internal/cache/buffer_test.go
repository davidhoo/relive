package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	_ = logger.Init(config.LoggingConfig{Level: "error", Console: true})
	os.Exit(m.Run())
}

func newTestBuffer(t *testing.T, submitter func(ctx context.Context, results []model.AnalysisResult) error) *ResultBuffer {
	t.Helper()
	dir := t.TempDir()
	buf := NewResultBuffer(submitter,
		WithBatchSize(3),
		WithFlushInterval(time.Hour), // large interval so auto-flush doesn't interfere
		WithBufferFile(filepath.Join(dir, "buffer.json")),
	)
	return buf
}

func sampleResult(photoID uint) model.AnalysisResult {
	return model.AnalysisResult{
		PhotoID:     photoID,
		Description: "test",
		MemoryScore: 50,
		BeautyScore: 50,
	}
}

func TestResultBuffer_AddAndCount(t *testing.T) {
	buf := newTestBuffer(t, func(ctx context.Context, results []model.AnalysisResult) error {
		return nil
	})

	assert.Equal(t, 0, buf.Count())
	buf.Add(sampleResult(1))
	assert.Equal(t, 1, buf.Count())
	buf.Add(sampleResult(2))
	assert.Equal(t, 2, buf.Count())
}

func TestResultBuffer_FlushOnBatchSize(t *testing.T) {
	var submitted []model.AnalysisResult
	buf := newTestBuffer(t, func(ctx context.Context, results []model.AnalysisResult) error {
		submitted = append(submitted, results...)
		return nil
	})

	buf.Add(sampleResult(1))
	buf.Add(sampleResult(2))
	// Third add should trigger auto-flush (batch size = 3)
	buf.Add(sampleResult(3))

	// Give a moment for the flush to happen
	time.Sleep(50 * time.Millisecond)
	assert.Len(t, submitted, 3)
	assert.Equal(t, 0, buf.Count())
}

func TestResultBuffer_ManualFlush(t *testing.T) {
	var submitted []model.AnalysisResult
	buf := newTestBuffer(t, func(ctx context.Context, results []model.AnalysisResult) error {
		submitted = append(submitted, results...)
		return nil
	})

	buf.Add(sampleResult(1))
	buf.Add(sampleResult(2))
	require.NoError(t, buf.Flush(context.Background()))

	assert.Len(t, submitted, 2)
	assert.Equal(t, 0, buf.Count())
}

func TestResultBuffer_FlushError_RetainsResults(t *testing.T) {
	buf := newTestBuffer(t, func(ctx context.Context, results []model.AnalysisResult) error {
		return errors.New("submit error")
	})

	buf.Add(sampleResult(1))
	err := buf.Flush(context.Background())
	require.Error(t, err)
	// Results should be restored back into the buffer
	assert.Equal(t, 1, buf.Count())
}

func TestResultBuffer_PersistAndRestore(t *testing.T) {
	dir := t.TempDir()
	bufFile := filepath.Join(dir, "persist_test.json")

	buf := NewResultBuffer(
		func(ctx context.Context, results []model.AnalysisResult) error { return nil },
		WithBatchSize(10),
		WithBufferFile(bufFile),
	)

	buf.Add(sampleResult(1))
	buf.Add(sampleResult(2))
	require.NoError(t, buf.Persist())

	// Create a new buffer and restore
	buf2 := NewResultBuffer(
		func(ctx context.Context, results []model.AnalysisResult) error { return nil },
		WithBatchSize(10),
		WithBufferFile(bufFile),
	)
	require.NoError(t, buf2.Restore())
	assert.Equal(t, 2, buf2.Count())
}

func TestResultBuffer_FlushEmpty(t *testing.T) {
	buf := newTestBuffer(t, func(ctx context.Context, results []model.AnalysisResult) error {
		t.Fatal("should not be called for empty buffer")
		return nil
	})

	err := buf.Flush(context.Background())
	require.NoError(t, err)
}

func TestResultBuffer_OnSubmittedCallback(t *testing.T) {
	var callbackResults []model.AnalysisResult
	buf := newTestBuffer(t, func(ctx context.Context, results []model.AnalysisResult) error {
		return nil
	})
	buf.SetOnSubmitted(func(results []model.AnalysisResult) {
		callbackResults = results
	})

	buf.Add(sampleResult(1))
	require.NoError(t, buf.Flush(context.Background()))
	assert.Len(t, callbackResults, 1)
}
