package cache

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestCheckpoint(t *testing.T) *Checkpoint {
	t.Helper()
	dir := t.TempDir()
	cp, err := NewCheckpoint(filepath.Join(dir, "test_checkpoint.db"))
	require.NoError(t, err)
	t.Cleanup(func() { cp.Close() })
	return cp
}

func TestCheckpoint_NewAndClose(t *testing.T) {
	cp := newTestCheckpoint(t)
	assert.NotEmpty(t, cp.GetDBPath())
}

func TestCheckpoint_MarkPending(t *testing.T) {
	cp := newTestCheckpoint(t)

	require.NoError(t, cp.MarkPending(1))

	processed, err := cp.IsProcessed(1)
	require.NoError(t, err)
	assert.False(t, processed, "pending should not count as processed")
}

func TestCheckpoint_MarkAnalyzed(t *testing.T) {
	cp := newTestCheckpoint(t)

	require.NoError(t, cp.MarkPending(1))
	require.NoError(t, cp.MarkAnalyzed(1))

	processed, err := cp.IsProcessed(1)
	require.NoError(t, err)
	assert.True(t, processed)
}

func TestCheckpoint_MarkSubmitted(t *testing.T) {
	cp := newTestCheckpoint(t)

	require.NoError(t, cp.MarkPending(1))
	require.NoError(t, cp.MarkAnalyzed(1))
	require.NoError(t, cp.MarkSubmitted(1))

	processed, err := cp.IsProcessed(1)
	require.NoError(t, err)
	assert.True(t, processed)
}

func TestCheckpoint_MarkFailed(t *testing.T) {
	cp := newTestCheckpoint(t)

	require.NoError(t, cp.MarkPending(1))
	require.NoError(t, cp.MarkFailed(1, "test error"))

	processed, err := cp.IsProcessed(1)
	require.NoError(t, err)
	assert.True(t, processed)

	failed, err := cp.GetFailedPhotos(10)
	require.NoError(t, err)
	require.Len(t, failed, 1)
	assert.Equal(t, uint(1), failed[0].PhotoID)
	assert.Equal(t, "test error", failed[0].ErrorMsg)
}

func TestCheckpoint_GetStats(t *testing.T) {
	cp := newTestCheckpoint(t)

	require.NoError(t, cp.MarkPending(1))
	require.NoError(t, cp.MarkPending(2))
	require.NoError(t, cp.MarkAnalyzed(1))
	require.NoError(t, cp.MarkSubmitted(2))
	require.NoError(t, cp.MarkPending(3))
	require.NoError(t, cp.MarkFailed(3, "err"))

	stats, err := cp.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(3), stats.Total)
	assert.Equal(t, int64(1), stats.Analyzed)
	assert.Equal(t, int64(1), stats.Submitted)
	assert.Equal(t, int64(1), stats.Failed)
	assert.Equal(t, int64(0), stats.Pending)
}

func TestCheckpoint_ShouldRetry(t *testing.T) {
	cp := newTestCheckpoint(t)

	// Unknown photo — should retry
	ok, err := cp.ShouldRetry(99, 3)
	require.NoError(t, err)
	assert.True(t, ok)

	// Failed photo with attempts < max
	require.NoError(t, cp.MarkPending(1))
	require.NoError(t, cp.MarkFailed(1, "err"))
	ok, err = cp.ShouldRetry(1, 3)
	require.NoError(t, err)
	assert.True(t, ok)

	// Submitted photo — should not retry
	require.NoError(t, cp.MarkPending(2))
	require.NoError(t, cp.MarkSubmitted(2))
	ok, err = cp.ShouldRetry(2, 3)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCheckpoint_GetAnalyzed(t *testing.T) {
	cp := newTestCheckpoint(t)

	require.NoError(t, cp.MarkPending(1))
	require.NoError(t, cp.MarkAnalyzed(1))
	require.NoError(t, cp.MarkPending(2))
	require.NoError(t, cp.MarkAnalyzed(2))

	ids, err := cp.GetAnalyzed()
	require.NoError(t, err)
	assert.Len(t, ids, 2)
}

func TestCheckpoint_ResetFailed(t *testing.T) {
	cp := newTestCheckpoint(t)

	require.NoError(t, cp.MarkPending(1))
	require.NoError(t, cp.MarkFailed(1, "err"))
	require.NoError(t, cp.ResetFailed(1))

	// After reset, photo is no longer tracked
	ok, err := cp.ShouldRetry(1, 3)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestCheckpoint_FilterProcessed(t *testing.T) {
	cp := newTestCheckpoint(t)

	require.NoError(t, cp.MarkPending(1))
	require.NoError(t, cp.MarkSubmitted(1))
	require.NoError(t, cp.MarkPending(2))
	// photo 2 is still pending, 3 is unknown

	unprocessed, err := cp.FilterProcessed([]uint{1, 2, 3})
	require.NoError(t, err)
	// 1 is submitted (processed), 2 is pending (not processed), 3 is unknown (not processed)
	assert.Contains(t, unprocessed, uint(2))
	assert.Contains(t, unprocessed, uint(3))
	assert.NotContains(t, unprocessed, uint(1))
}
