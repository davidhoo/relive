package util

import (
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"
)

var openFaceThumbnailImage = OpenImage

type FaceThumbnailSpec struct {
	BBoxX      float64
	BBoxY      float64
	BBoxWidth  float64
	BBoxHeight float64
}

// GenerateFaceThumbnail generates a face thumbnail with rotation support.
// manualRotation is the clockwise rotation to apply (0, 90, 180, or 270 degrees).
func GenerateFaceThumbnail(filePath string, outputRoot string, bboxX, bboxY, bboxWidth, bboxHeight float64, manualRotation int) (string, error) {
	paths, err := GenerateFaceThumbnails(filePath, outputRoot, []FaceThumbnailSpec{
		{
			BBoxX:      bboxX,
			BBoxY:      bboxY,
			BBoxWidth:  bboxWidth,
			BBoxHeight: bboxHeight,
		},
	}, manualRotation)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no face thumbnail generated")
	}
	return paths[0], nil
}

// GenerateFaceThumbnails generates multiple face thumbnails with rotation support.
// manualRotation is the clockwise rotation to apply (0, 90, 180, or 270 degrees).
func GenerateFaceThumbnails(filePath string, outputRoot string, specs []FaceThumbnailSpec, manualRotation int) ([]string, error) {
	if len(specs) == 0 {
		return nil, nil
	}

	img, err := openFaceThumbnailImage(filePath)
	if err != nil {
		return nil, fmt.Errorf("open image for face thumbnail: %w", err)
	}

	// Apply manual rotation before generating thumbnails
	img = ApplyManualRotation(img, manualRotation)

	return generateFaceThumbnailsFromImage(img, filePath, outputRoot, specs)
}

func generateFaceThumbnailsFromImage(img image.Image, filePath string, outputRoot string, specs []FaceThumbnailSpec) ([]string, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid image bounds")
	}

	paths := make([]string, 0, len(specs))
	for _, spec := range specs {
		cropRect := buildFaceCropRect(width, height, spec.BBoxX, spec.BBoxY, spec.BBoxWidth, spec.BBoxHeight)
		faceImage := imaging.Crop(img, cropRect)
		faceImage = imaging.Fill(faceImage, 256, 256, imaging.Center, imaging.Lanczos)

		relPath := filepath.Join("faces", GenerateDerivedImagePath(fmt.Sprintf(
			"face:%s:%0.6f:%0.6f:%0.6f:%0.6f",
			filePath, spec.BBoxX, spec.BBoxY, spec.BBoxWidth, spec.BBoxHeight,
		)))
		fullPath := filepath.Join(outputRoot, relPath)

		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return nil, fmt.Errorf("create face thumbnail dir: %w", err)
		}
		if err := imaging.Save(faceImage, fullPath, imaging.JPEGQuality(90)); err != nil {
			return nil, fmt.Errorf("save face thumbnail: %w", err)
		}
		paths = append(paths, relPath)
	}

	return paths, nil
}

func buildFaceCropRect(width, height int, bboxX, bboxY, bboxWidth, bboxHeight float64) image.Rectangle {
	minX := int(math.Floor(bboxX * float64(width)))
	minY := int(math.Floor(bboxY * float64(height)))
	maxX := int(math.Ceil((bboxX + bboxWidth) * float64(width)))
	maxY := int(math.Ceil((bboxY + bboxHeight) * float64(height)))

	paddingX := int(math.Round(float64(maxX-minX) * 0.18))
	paddingY := int(math.Round(float64(maxY-minY) * 0.18))

	left := max(0, minX-paddingX)
	top := max(0, minY-paddingY)
	right := min(width, maxX+paddingX)
	bottom := min(height, maxY+paddingY)

	if right <= left {
		right = min(width, left+1)
	}
	if bottom <= top {
		bottom = min(height, top+1)
	}

	return image.Rect(left, top, right, bottom)
}
