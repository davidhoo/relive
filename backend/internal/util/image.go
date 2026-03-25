package util

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // 支持 PNG 格式
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/jdeng/goheif"
	"github.com/jdeng/goheif/heif"
)

type heifTransformInfo struct {
	rotations    int
	mirror       int
	visualWidth  int
	visualHeight int
}

var extractEXIFFunc = ExtractEXIF

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
	// 打开图片（使用 OpenImage 支持非标准 JPEG 及外部工具 fallback）
	img, err := OpenImage(filePath)
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
	if IsHEIC(filePath) {
		if info, heifErr := readHEIFTransformInfo(filePath); heifErr == nil && info.visualWidth > 0 && info.visualHeight > 0 {
			return info.visualWidth, info.visualHeight, nil
		}
	}

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


// init 注册 HEIC 解码器
func init() {
	// 注册 HEIC/HEIF 格式解码器
	image.RegisterFormat("heic", "ftypheic", goheif.Decode, goheif.DecodeConfig)
	image.RegisterFormat("heic", "ftypmif1", goheif.Decode, goheif.DecodeConfig)
	image.RegisterFormat("heif", "ftypheic", goheif.Decode, goheif.DecodeConfig)
	image.RegisterFormat("heif", "ftypmif1", goheif.Decode, goheif.DecodeConfig)
}

// IsHEIC 检查是否是 HEIC/HEIF 格式
func IsHEIC(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".heic" || ext == ".heif"
}

// OpenImage 打开图片（支持 JPEG、PNG、HEIC 等格式）
func OpenImage(filePath string) (image.Image, error) {
	// 如果是 HEIC 格式，使用专门的解码器
	if IsHEIC(filePath) {
		img, err := openHEIC(filePath)
		if err == nil {
			return img, nil
		}
		// goheif 解码失败（可能是扩展名为 .HEIC 但实际是 JPEG 等格式），
		// fallback 到 imaging 库尝试按实际格式解码
		fallbackImg, fallbackErr := imaging.Open(filePath)
		if fallbackErr == nil {
			return fallbackImg, nil
		}
		// Go 解码器也失败，尝试外部工具
		if extImg, extErr := openImageWithExternalTool(filePath); extErr == nil {
			return extImg, nil
		}
		// 全部失败，返回原始 goheif 错误
		return nil, err
	}

	// 其他格式使用 imaging 库
	img, err := imaging.Open(filePath)
	if err == nil {
		return img, nil
	}

	// Go 标准 JPEG 解码器较严格，对非标准 JPEG 可能失败，
	// fallback 到外部工具（macOS: sips, Linux: vips）
	if extImg, extErr := openImageWithExternalTool(filePath); extErr == nil {
		return extImg, nil
	}

	return nil, err
}

// openImageWithExternalTool 使用外部工具转换图片为 JPEG 后读取
func openImageWithExternalTool(filePath string) (image.Image, error) {
	tmpFile, err := os.CreateTemp("", "relive-convert-*.jpg")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	if err := convertImageWithExternalTool(filePath, tmpPath); err != nil {
		return nil, err
	}

	return imaging.Open(tmpPath)
}

// convertImageWithExternalTool 使用系统可用的外部工具将图片转换为 JPEG。
// 重要：所有外部工具必须禁用自动旋转（--no-rotate 等），
// 因为 normalizeImageForDisplay 会统一根据 EXIF Orientation 做旋转校正。
func convertImageWithExternalTool(srcPath, dstPath string) error {
	if runtime.GOOS == "darwin" {
		// macOS: 使用 sips（sips 不会自动旋转，无需额外参数）
		if _, err := exec.LookPath("sips"); err == nil {
			cmd := exec.Command("sips", "-s", "format", "jpeg", "-s", "formatOptions", "85", srcPath, "--out", dstPath)
			if err := cmd.Run(); err == nil {
				return nil
			}
		}
	}

	// Linux/Docker: 优先使用 vips jpegsave（--no-rotate 禁止自动旋转）
	if _, err := exec.LookPath("vips"); err == nil {
		cmd := exec.Command("vips", "jpegsave", srcPath, dstPath, "--Q", "85", "--no-rotate")
		if err := cmd.Run(); err == nil {
			return nil
		}
	}

	// vips 不可用时尝试 vipsthumbnail（--no-rotate 禁止自动旋转）
	if _, err := exec.LookPath("vipsthumbnail"); err == nil {
		cmd := exec.Command("vipsthumbnail", srcPath, "--size", "99999x99999", "--no-rotate", "-o", dstPath+"[Q=85]")
		if err := cmd.Run(); err == nil {
			return nil
		}
	}

	// fallback: ImageMagick convert（-auto-orient 默认关闭，不需要额外参数）
	if _, err := exec.LookPath("convert"); err == nil {
		cmd := exec.Command("convert", srcPath, "-quality", "85", dstPath)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}

	return fmt.Errorf("no external image conversion tool available (tried sips, vips, vipsthumbnail, convert)")
}

// openHEIC 使用 goheif 解码 HEIC 文件并应用 HEIF 变换
func openHEIC(filePath string) (image.Image, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	img, err := goheif.Decode(file)
	if err != nil {
		return nil, err
	}

	info, err := readHEIFTransformInfoFromFile(file)
	if err != nil {
		return img, nil
	}

	return applyHEIFTransforms(img, info), nil
}

func normalizeImageForDisplay(filePath string, img image.Image) image.Image {
	if IsHEIC(filePath) {
		return img
	}

	exifData, err := extractEXIFFunc(filePath)
	if err != nil || exifData == nil {
		return img
	}

	return NormalizeOrientation(img, exifData.Orientation)
}

func readHEIFTransformInfo(filePath string) (heifTransformInfo, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return heifTransformInfo{}, err
	}
	defer file.Close()

	return readHEIFTransformInfoFromFile(file)
}

func readHEIFTransformInfoFromFile(file *os.File) (heifTransformInfo, error) {
	hf := heif.Open(file)
	item, err := hf.PrimaryItem()
	if err != nil {
		return heifTransformInfo{}, err
	}

	info := heifTransformInfo{
		rotations: item.Rotations(),
		mirror:    item.Mirror(),
	}

	if width, height, ok := item.VisualDimensions(); ok {
		info.visualWidth = width
		info.visualHeight = height
	}

	return info, nil
}

func heifRotationToOrientation(rotations int, mirror int) int {
	if mirror != 0 {
		return 0
	}

	switch ((rotations % 4) + 4) % 4 {
	case 0:
		return 1
	case 1:
		return 8
	case 2:
		return 3
	case 3:
		return 6
	default:
		return 0
	}
}

func applyHEIFTransforms(img image.Image, info heifTransformInfo) image.Image {
	transformed := img
	switch info.mirror {
	case 1:
		transformed = imaging.FlipH(transformed)
	case 2:
		transformed = imaging.FlipV(transformed)
	}

	for i := 0; i < ((info.rotations%4)+4)%4; i++ {
		transformed = imaging.Rotate90(transformed)
	}

	return transformed
}

func GetDisplayImageSize(filePath string) (width, height int, err error) {
	width, height, err = GetImageSize(filePath)
	if err != nil {
		return 0, 0, err
	}

	if IsHEIC(filePath) {
		return width, height, nil
	}

	exifData, exifErr := extractEXIFFunc(filePath)
	if exifErr != nil || exifData == nil {
		return width, height, nil
	}

	width, height = normalizeDimensionsForOrientation(width, height, exifData.Orientation)
	return width, height, nil
}

func ShouldRefreshThumbnailCache(sourcePath, cachePath string) bool {
	return ShouldRefreshThumbnailCacheWithRotation(sourcePath, cachePath, 0)
}

// ShouldRefreshThumbnailCacheWithRotation 检查缩略图是否需要重新生成。
// manualRotation: 用户手动旋转角度（0/90/180/270）
func ShouldRefreshThumbnailCacheWithRotation(sourcePath, cachePath string, manualRotation int) bool {
	// 获取自动校正后的 display 尺寸
	sourceWidth, sourceHeight, err := GetDisplayImageSize(sourcePath)
	if err != nil || sourceWidth <= 0 || sourceHeight <= 0 {
		return false
	}

	// 叠加手动旋转对尺寸的影响
	if manualRotation == 90 || manualRotation == 270 {
		sourceWidth, sourceHeight = sourceHeight, sourceWidth
	}

	cacheWidth, cacheHeight, err := GetImageSize(cachePath)
	if err != nil || cacheWidth <= 0 || cacheHeight <= 0 {
		return true
	}

	sourceLandscape := sourceWidth > sourceHeight
	cacheLandscape := cacheWidth > cacheHeight
	return sourceLandscape != cacheLandscape
}

func normalizeDimensionsForOrientation(width, height, orientation int) (int, int) {
	if orientationSwapsAxes(orientation) {
		return height, width
	}

	return width, height
}

func orientationSwapsAxes(orientation int) bool {
	switch orientation {
	case 5, 6, 7, 8:
		return true
	default:
		return false
	}
}

// NormalizeOrientation 根据 EXIF orientation 将图片旋正。
func NormalizeOrientation(img image.Image, orientation int) image.Image {
	switch orientation {
	case 2:
		return imaging.FlipH(img)
	case 3:
		return imaging.Rotate180(img)
	case 4:
		return imaging.FlipV(img)
	case 5:
		return imaging.Transpose(img)
	case 6:
		return imaging.Rotate270(img)
	case 7:
		return imaging.Transverse(img)
	case 8:
		return imaging.Rotate90(img)
	default:
		return img
	}
}

// ApplyManualRotation 叠加用户手动旋转（顺时针 0/90/180/270 度）。
// 在自动方向校正之后调用，所有格式统一处理。
func ApplyManualRotation(img image.Image, degrees int) image.Image {
	switch degrees {
	case 90:
		return imaging.Rotate270(img) // Rotate270 = 顺时针 90°
	case 180:
		return imaging.Rotate180(img)
	case 270:
		return imaging.Rotate90(img) // Rotate90 = 顺时针 270° = 逆时针 90°
	default:
		return img
	}
}

// GenerateFramePreview 按目标比例生成尽量保住主体的预览图。
func GenerateFramePreview(img image.Image, targetWidth, targetHeight int) image.Image {
	if targetWidth <= 0 || targetHeight <= 0 {
		return imaging.Clone(img)
	}

	bounds := img.Bounds()
	sourceWidth := bounds.Dx()
	sourceHeight := bounds.Dy()
	if sourceWidth == 0 || sourceHeight == 0 {
		return imaging.New(targetWidth, targetHeight, color.NRGBA{R: 245, G: 247, B: 250, A: 255})
	}

	cropRect := calculateSmartCropRect(sourceWidth, sourceHeight, targetWidth, targetHeight, buildSaliencyMap(img))
	cropped := imaging.Crop(img, cropRect)
	return imaging.Resize(cropped, targetWidth, targetHeight, imaging.Lanczos)
}

type saliencyMap struct {
	integral [][]float64
	width    int
	height   int
	scaleX   float64
	scaleY   float64
	total    float64
}

func buildSaliencyMap(img image.Image) saliencyMap {
	bounds := img.Bounds()
	sourceWidth := bounds.Dx()
	sourceHeight := bounds.Dy()
	if sourceWidth == 0 || sourceHeight == 0 {
		return saliencyMap{}
	}

	analysis := imaging.Clone(img)
	const maxAnalysisSide = 160
	if max(sourceWidth, sourceHeight) > maxAnalysisSide {
		analysis = imaging.Fit(img, maxAnalysisSide, maxAnalysisSide, imaging.Linear)
	}

	analysisBounds := analysis.Bounds()
	analysisWidth := analysisBounds.Dx()
	analysisHeight := analysisBounds.Dy()
	integral := make([][]float64, analysisHeight+1)
	for i := range integral {
		integral[i] = make([]float64, analysisWidth+1)
	}

	total := 0.0
	for y := 0; y < analysisHeight; y++ {
		rowSum := 0.0
		for x := 0; x < analysisWidth; x++ {
			current := analysis.NRGBAAt(analysisBounds.Min.X+x, analysisBounds.Min.Y+y)
			right := current
			down := current
			if x+1 < analysisWidth {
				right = analysis.NRGBAAt(analysisBounds.Min.X+x+1, analysisBounds.Min.Y+y)
			}
			if y+1 < analysisHeight {
				down = analysis.NRGBAAt(analysisBounds.Min.X+x, analysisBounds.Min.Y+y+1)
			}

			edgeStrength := math.Abs(luminance(current)-luminance(right)) + math.Abs(luminance(current)-luminance(down))
			saliency := edgeStrength + colorfulness(current)*0.35

			rowSum += saliency
			integral[y+1][x+1] = integral[y][x+1] + rowSum
			total += saliency
		}
	}

	return saliencyMap{
		integral: integral,
		width:    analysisWidth,
		height:   analysisHeight,
		scaleX:   float64(analysisWidth) / float64(sourceWidth),
		scaleY:   float64(analysisHeight) / float64(sourceHeight),
		total:    total,
	}
}

func calculateSmartCropRect(sourceWidth, sourceHeight, targetWidth, targetHeight int, saliency saliencyMap) image.Rectangle {
	targetRatio := float64(targetWidth) / float64(targetHeight)
	sourceRatio := float64(sourceWidth) / float64(sourceHeight)

	if math.Abs(sourceRatio-targetRatio) < 1e-6 {
		return image.Rect(0, 0, sourceWidth, sourceHeight)
	}

	if sourceRatio > targetRatio {
		cropWidth := int(math.Round(float64(sourceHeight) * targetRatio))
		if cropWidth <= 0 || cropWidth >= sourceWidth {
			return image.Rect(0, 0, sourceWidth, sourceHeight)
		}

		maxOffset := sourceWidth - cropWidth
		bestOffset := chooseBestCropOffset(maxOffset, func(offset int) float64 {
			rect := image.Rect(offset, 0, offset+cropWidth, sourceHeight)
			return scoreCropRect(rect, saliency, sourceWidth, sourceHeight, 0.5, 0.5)
		})
		return image.Rect(bestOffset, 0, bestOffset+cropWidth, sourceHeight)
	}

	cropHeight := int(math.Round(float64(sourceWidth) / targetRatio))
	if cropHeight <= 0 || cropHeight >= sourceHeight {
		return image.Rect(0, 0, sourceWidth, sourceHeight)
	}

	maxOffset := sourceHeight - cropHeight
	bestOffset := chooseBestCropOffset(maxOffset, func(offset int) float64 {
		rect := image.Rect(0, offset, sourceWidth, offset+cropHeight)
		return scoreCropRect(rect, saliency, sourceWidth, sourceHeight, 0.5, 0.42)
	})
	return image.Rect(0, bestOffset, sourceWidth, bestOffset+cropHeight)
}

func chooseBestCropOffset(maxOffset int, score func(int) float64) int {
	if maxOffset <= 0 {
		return 0
	}

	bestOffset := 0
	bestScore := math.Inf(-1)
	for _, offset := range buildCropOffsets(maxOffset) {
		currentScore := score(offset)
		if currentScore > bestScore {
			bestScore = currentScore
			bestOffset = offset
		}
	}
	return bestOffset
}

func buildCropOffsets(maxOffset int) []int {
	step := max(1, maxOffset/64)
	offsets := make([]int, 0, maxOffset/step+3)
	seen := make(map[int]struct{})

	appendOffset := func(offset int) {
		offset = clampInt(offset, 0, maxOffset)
		if _, ok := seen[offset]; ok {
			return
		}
		seen[offset] = struct{}{}
		offsets = append(offsets, offset)
	}

	for offset := 0; offset <= maxOffset; offset += step {
		appendOffset(offset)
	}
	appendOffset(maxOffset / 2)
	appendOffset(maxOffset)
	return offsets
}

func scoreCropRect(rect image.Rectangle, saliency saliencyMap, sourceWidth, sourceHeight int, preferredCenterX, preferredCenterY float64) float64 {
	centerX := float64(rect.Min.X+rect.Max.X) / 2 / float64(sourceWidth)
	centerY := float64(rect.Min.Y+rect.Max.Y) / 2 / float64(sourceHeight)
	compositionPenalty := math.Abs(centerX-preferredCenterX)*0.18 + math.Abs(centerY-preferredCenterY)*0.28

	if saliency.total <= 0 {
		return -compositionPenalty
	}

	coverage := saliency.sum(rect) / saliency.total
	return coverage - compositionPenalty
}

func (m saliencyMap) sum(rect image.Rectangle) float64 {
	if m.width == 0 || m.height == 0 {
		return 0
	}

	x0 := clampInt(int(math.Floor(float64(rect.Min.X)*m.scaleX)), 0, m.width)
	y0 := clampInt(int(math.Floor(float64(rect.Min.Y)*m.scaleY)), 0, m.height)
	x1 := clampInt(int(math.Ceil(float64(rect.Max.X)*m.scaleX)), 0, m.width)
	y1 := clampInt(int(math.Ceil(float64(rect.Max.Y)*m.scaleY)), 0, m.height)

	if x1 <= x0 {
		x1 = min(m.width, x0+1)
	}
	if y1 <= y0 {
		y1 = min(m.height, y0+1)
	}

	return m.integral[y1][x1] - m.integral[y0][x1] - m.integral[y1][x0] + m.integral[y0][x0]
}

func luminance(c color.NRGBA) float64 {
	return 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
}

func colorfulness(c color.NRGBA) float64 {
	maxChannel := max(int(c.R), max(int(c.G), int(c.B)))
	minChannel := min(int(c.R), min(int(c.G), int(c.B)))
	return float64(maxChannel - minChannel)
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
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
	return g.GenerateThumbnailWithRotation(filePath, 0)
}

// GenerateThumbnailWithRotation 生成缩略图，先自动校正方向，再叠加手动旋转
// manualRotation: 0/90/180/270，用户手动旋转角度
func (g *ThumbnailGenerator) GenerateThumbnailWithRotation(filePath string, manualRotation int) (string, error) {
	// 打开原图（支持 HEIC 等格式）
	img, err := OpenImage(filePath)
	if err != nil {
		return "", err
	}

	// 自动校正方向（非 HEIC 从 EXIF 读取，HEIC 由解码器自动处理）
	img = normalizeImageForDisplay(filePath, img)

	// 叠加手动旋转（所有格式统一）
	img = ApplyManualRotation(img, manualRotation)

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
		if ShouldRefreshThumbnailCache(filePath, thumbnailPath) {
			return g.GenerateThumbnail(filePath)
		}
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
	return generateHashedJPEGPath(filePath)
}

// GenerateDerivedImagePath 生成图片派生资源路径。
func GenerateDerivedImagePath(cacheKey string) string {
	return generateHashedJPEGPath(cacheKey)
}

func generateHashedJPEGPath(cacheKey string) string {
	// 计算文件路径的哈希
	hash := sha256.Sum256([]byte(cacheKey))
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
