package util

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/jpeg"
	_ "image/png" // 支持 PNG 格式
	"os"
	"path/filepath"

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

// ThumbnailGenerator 缩略图生成器
type ThumbnailGenerator struct {
	MaxWidth    int    // 最大宽度
	MaxHeight   int    // 最大高度
	JPEGQuality int    // JPEG 质量
	OutputDir   string // 输出目录
}

// NewThumbnailGenerator 创建缩略图生成器
func NewThumbnailGenerator(maxWidth, maxHeight, jpegQuality int, outputDir string) *ThumbnailGenerator {
	return &ThumbnailGenerator{
		MaxWidth:    maxWidth,
		MaxHeight:   maxHeight,
		JPEGQuality: jpegQuality,
		OutputDir:   outputDir,
	}
}

// GenerateThumbnail 生成缩略图
// 返回缩略图的相对路径和错误
func (g *ThumbnailGenerator) GenerateThumbnail(filePath string) (string, error) {
	// 打开原图
	img, err := imaging.Open(filePath)
	if err != nil {
		return "", err
	}

	// 获取原始尺寸
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// 计算缩略图尺寸（保持宽高比）
	newWidth, newHeight := g.calculateSize(width, height)

	// 生成缩略图
	thumbnail := imaging.Resize(img, newWidth, newHeight, imaging.Lanczos)

	// 生成缩略图文件名（基于原文件路径的哈希）
	relPath := generateThumbnailPath(filePath)
	thumbnailPath := filepath.Join(g.OutputDir, relPath)

	// 确保目录存在
	dir := filepath.Dir(thumbnailPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	// 保存缩略图
	if err := imaging.Save(thumbnail, thumbnailPath, imaging.JPEGQuality(g.JPEGQuality)); err != nil {
		return "", err
	}

	return relPath, nil
}

// GenerateThumbnailIfNotExists 如果缩略图不存在则生成
func (g *ThumbnailGenerator) GenerateThumbnailIfNotExists(filePath string) (string, error) {
	relPath := generateThumbnailPath(filePath)
	thumbnailPath := filepath.Join(g.OutputDir, relPath)

	// 检查缩略图是否已存在
	if _, err := os.Stat(thumbnailPath); err == nil {
		// 已存在，直接返回路径
		return relPath, nil
	}

	// 不存在，生成缩略图
	return g.GenerateThumbnail(filePath)
}

// calculateSize 计算缩略图尺寸
func (g *ThumbnailGenerator) calculateSize(width, height int) (newWidth, newHeight int) {
	// 如果图片已经小于等于目标尺寸，保持原尺寸
	if width <= g.MaxWidth && height <= g.MaxHeight {
		return width, height
	}

	// 计算缩放比例
	ratioW := float64(g.MaxWidth) / float64(width)
	ratioH := float64(g.MaxHeight) / float64(height)
	ratio := ratioW
	if ratioH < ratioW {
		ratio = ratioH
	}

	newWidth = int(float64(width) * ratio)
	newHeight = int(float64(height) * ratio)

	return newWidth, newHeight
}

// generateThumbnailPath 生成缩略图路径
// 使用文件路径的哈希作为文件名，避免特殊字符问题
func generateThumbnailPath(filePath string) string {
	// 计算文件路径的哈希
	hash := sha256.Sum256([]byte(filePath))
	hashStr := hex.EncodeToString(hash[:])[:16]

	// 使用两级目录结构避免单目录文件过多
	dir1 := hashStr[:2]
	dir2 := hashStr[2:4]
	filename := hashStr + ".jpg"

	return filepath.Join(dir1, dir2, filename)
}

// ThumbnailExists 检查缩略图是否存在
func (g *ThumbnailGenerator) ThumbnailExists(thumbnailPath string) bool {
	if thumbnailPath == "" {
		return false
	}
	fullPath := filepath.Join(g.OutputDir, thumbnailPath)
	_, err := os.Stat(fullPath)
	return err == nil
}
