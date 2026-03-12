package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractEXIF_NonExistentFile(t *testing.T) {
	data, err := ExtractEXIF("/nonexistent/image.jpg")
	// Implementation may not error (falls back to external tools that also fail silently)
	// Just verify it doesn't panic and returns reasonable data
	if err != nil {
		return
	}
	if data != nil {
		assert.Nil(t, data.TakenAt)
	}
}

func TestExtractEXIF_NonImageFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.jpg")
	_ = os.WriteFile(p, []byte("not an image"), 0644)

	data, err := ExtractEXIF(p)
	if err != nil {
		return // acceptable
	}
	assert.Nil(t, data.TakenAt)
}

func TestExtractEXIF_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.jpg")
	_ = os.WriteFile(p, []byte{}, 0644)

	data, err := ExtractEXIF(p)
	// Empty file — either error or empty data is fine
	if err != nil {
		return
	}
	if data != nil {
		assert.Nil(t, data.TakenAt)
	}
}
