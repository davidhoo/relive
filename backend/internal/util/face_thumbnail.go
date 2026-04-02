package util

import (
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"
)

func GenerateFaceThumbnail(filePath string, outputRoot string, bboxX, bboxY, bboxWidth, bboxHeight float64) (string, error) {
	img, err := OpenImage(filePath)
	if err != nil {
		return "", fmt.Errorf("open image for face thumbnail: %w", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= 0 || height <= 0 {
		return "", fmt.Errorf("invalid image bounds")
	}

	cropRect := buildFaceCropRect(width, height, bboxX, bboxY, bboxWidth, bboxHeight)
	faceImage := imaging.Crop(img, cropRect)
	faceImage = imaging.Fill(faceImage, 256, 256, imaging.Center, imaging.Lanczos)

	relPath := filepath.Join("faces", GenerateDerivedImagePath(fmt.Sprintf(
		"face:%s:%0.6f:%0.6f:%0.6f:%0.6f",
		filePath, bboxX, bboxY, bboxWidth, bboxHeight,
	)))
	fullPath := filepath.Join(outputRoot, relPath)

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("create face thumbnail dir: %w", err)
	}
	if err := imaging.Save(faceImage, fullPath, imaging.JPEGQuality(90)); err != nil {
		return "", fmt.Errorf("save face thumbnail: %w", err)
	}

	return relPath, nil
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
