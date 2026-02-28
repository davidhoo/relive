package util

import (
	"bytes"
	"image"
	"image/jpeg"
	_ "image/png" // 支持 PNG 格式
	"os"

	"github.com/disintegration/imaging"
)

// ImageProcessor 图片处理器
type ImageProcessor struct {
	MaxLongSide int // 最大长边（像素）
	JPEGQuality int // JPEG 质量（0-100）
}

// NewImageProcessor 创建图片处理器
func NewImageProcessor(maxLongSide, jpegQuality int) *ImageProcessor {
	return &ImageProcessor{
		MaxLongSide: maxLongSide,
		JPEGQuality: jpegQuality,
	}
}

// ProcessForAI 为 AI 分析预处理图片
func (p *ImageProcessor) ProcessForAI(filePath string) ([]byte, error) {
	// 打开图片
	img, err := imaging.Open(filePath)
	if err != nil {
		return nil, err
	}

	// 获取原始尺寸
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// 检查是否需要缩放
	longSide := max(width, height)
	if longSide > p.MaxLongSide {
		// 缩放图片
		img = p.resizeImage(img, width, height)
	}

	// JPEG 压缩
	compressed, err := p.compressToJPEG(img)
	if err != nil {
		return nil, err
	}

	return compressed, nil
}

// resizeImage 缩放图片（保持宽高比）
func (p *ImageProcessor) resizeImage(img image.Image, width, height int) image.Image {
	// 计算缩放后的尺寸
	var newWidth, newHeight int
	if width > height {
		// 横向图片
		newWidth = p.MaxLongSide
		newHeight = int(float64(height) * float64(p.MaxLongSide) / float64(width))
	} else {
		// 竖向图片
		newHeight = p.MaxLongSide
		newWidth = int(float64(width) * float64(p.MaxLongSide) / float64(height))
	}

	// 使用 Lanczos 算法（高质量）
	return imaging.Resize(img, newWidth, newHeight, imaging.Lanczos)
}

// compressToJPEG JPEG 压缩
func (p *ImageProcessor) compressToJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer

	// JPEG 编码
	err := jpeg.Encode(&buf, img, &jpeg.Options{
		Quality: p.JPEGQuality,
	})
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// GetImageSize 获取图片尺寸（不加载完整图片）
func GetImageSize(filePath string) (width, height int, err error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}

	return config.Width, config.Height, nil
}

// max 返回两个整数中的较大值
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
