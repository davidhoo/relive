package util

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateThumbnailNormalizesJPEGOrientation(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "source.jpg")

	source := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			source.Set(x, y, color.NRGBA{R: uint8(20 * x), G: uint8(40 * y), B: 180, A: 255})
		}
	}

	file, err := os.Create(sourcePath)
	if err != nil {
		t.Fatalf("create source jpeg: %v", err)
	}
	defer file.Close()

	if err := jpeg.Encode(file, source, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode source jpeg: %v", err)
	}

	originalExtractEXIF := extractEXIFFunc
	extractEXIFFunc = func(path string) (*EXIFData, error) {
		if path == sourcePath {
			return &EXIFData{Orientation: 6}, nil
		}
		return &EXIFData{}, nil
	}
	defer func() { extractEXIFFunc = originalExtractEXIF }()

	generator := NewThumbnailGenerator(100, 100, 90, tempDir)
	relPath, err := generator.GenerateThumbnail(sourcePath)
	if err != nil {
		t.Fatalf("generate thumbnail: %v", err)
	}

	thumbnail, err := OpenImage(filepath.Join(tempDir, relPath))
	if err != nil {
		t.Fatalf("open thumbnail: %v", err)
	}

	bounds := thumbnail.Bounds()
	if bounds.Dx() != 2 || bounds.Dy() != 4 {
		t.Fatalf("expected portrait thumbnail 2x4, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestShouldRefreshThumbnailCacheWhenJPEGOrientationChangesDisplayAspect(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "source.jpg")
	cachePath := filepath.Join(tempDir, "cache.jpg")

	writeJPEG := func(path string, width, height int) {
		t.Helper()
		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		file, err := os.Create(path)
		if err != nil {
			t.Fatalf("create jpeg %s: %v", path, err)
		}
		defer file.Close()
		if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
			t.Fatalf("encode jpeg %s: %v", path, err)
		}
	}

	writeJPEG(sourcePath, 4, 2)
	writeJPEG(cachePath, 4, 2)

	originalExtractEXIF := extractEXIFFunc
	extractEXIFFunc = func(path string) (*EXIFData, error) {
		if path == sourcePath {
			return &EXIFData{Orientation: 6}, nil
		}
		return &EXIFData{}, nil
	}
	defer func() { extractEXIFFunc = originalExtractEXIF }()

	if !ShouldRefreshThumbnailCache(sourcePath, cachePath) {
		t.Fatal("expected stale landscape cache to refresh for portrait JPEG")
	}
}
