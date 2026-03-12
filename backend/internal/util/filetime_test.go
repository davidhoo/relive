package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetFileTimes_ModTime(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(p, []byte("data"), 0644))

	info, err := os.Stat(p)
	require.NoError(t, err)

	ft := GetFileTimes(info)
	assert.False(t, ft.ModTime.IsZero())
	assert.Equal(t, info.ModTime(), ft.ModTime)
}

func TestGetFileTimes_CreateTime(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test2.txt")
	require.NoError(t, os.WriteFile(p, []byte("data"), 0644))

	info, err := os.Stat(p)
	require.NoError(t, err)

	ft := GetFileTimes(info)
	// On macOS CreateTime should be non-nil; on Linux it's nil
	if ft.CreateTime != nil {
		assert.False(t, ft.CreateTime.IsZero())
	}
}
