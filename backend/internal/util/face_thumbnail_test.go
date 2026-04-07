package util

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateFaceThumbnailsOpensImageOnce(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "source.jpg")
	outputRoot := filepath.Join(tempDir, "thumbs")

	img := image.NewNRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x % 255), G: uint8(y % 255), B: 120, A: 255})
		}
	}

	file, err := os.Create(sourcePath)
	if err != nil {
		t.Fatalf("create source image: %v", err)
	}
	defer file.Close()
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode source image: %v", err)
	}

	originalOpen := openFaceThumbnailImage
	openCalls := 0
	openFaceThumbnailImage = func(path string) (image.Image, error) {
		openCalls++
		return originalOpen(path)
	}
	defer func() { openFaceThumbnailImage = originalOpen }()

	paths, err := GenerateFaceThumbnails(sourcePath, outputRoot, []FaceThumbnailSpec{
		{BBoxX: 0.10, BBoxY: 0.10, BBoxWidth: 0.20, BBoxHeight: 0.20},
		{BBoxX: 0.55, BBoxY: 0.15, BBoxWidth: 0.18, BBoxHeight: 0.18},
	})
	if err != nil {
		t.Fatalf("generate face thumbnails: %v", err)
	}

	if openCalls != 1 {
		t.Fatalf("expected image to be opened once, got %d", openCalls)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 thumbnail paths, got %d", len(paths))
	}
	for _, relPath := range paths {
		if relPath == "" {
			t.Fatal("expected non-empty thumbnail path")
		}
		if _, err := os.Stat(filepath.Join(outputRoot, relPath)); err != nil {
			t.Fatalf("expected thumbnail file %s to exist: %v", relPath, err)
		}
	}
}
