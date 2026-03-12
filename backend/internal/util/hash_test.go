package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashFile_ValidFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(p, []byte("hello world"), 0644))

	hash, err := HashFile(p)
	require.NoError(t, err)
	// SHA256 of "hello world"
	assert.Equal(t, "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9", hash)
}

func TestHashFile_NonExistentFile(t *testing.T) {
	_, err := HashFile("/nonexistent/path/to/file.txt")
	require.Error(t, err)
}

func TestHashBytes_KnownValue(t *testing.T) {
	hash := HashBytes([]byte("hello world"))
	assert.Equal(t, "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9", hash)
}

func TestHashBytes_Empty(t *testing.T) {
	hash := HashBytes([]byte{})
	// SHA256 of empty string
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hash)
}
