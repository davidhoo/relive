package util

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/disintegration/imaging"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

type RenderProfile struct {
	Name           string
	DisplayName    string
	CanvasTemplate string
	Width          int
	Height         int
	PaletteName    string
	DitherMode     string
	// GammaCorrection 是量化前对图像做 Gamma 预处理的指数。
	// 0 或 1.0 表示不处理；< 1.0（如 0.9）略微提亮，补偿墨水屏偏暗的显示特性。
	GammaCorrection  float64
	Palette          []color.NRGBA
	DefaultForDevice bool
}

const DefaultCanvasTemplate = "canvas_portrait_480x800_v1"

const (
	displayCanvasWidth    = 480
	displayCanvasHeight   = 800
	displayPhotoHeight    = 640
	displayInfoHeight     = 160
	infoBinaryThreshold   = 235
	infoHorizontalPadding = 24
	titleFontSize         = 22
	subtitleFontSize      = 18
	titleSingleBaselineY  = 704
	titleFirstBaselineY   = 690
	titleLineGap          = 30
	subtitleBaselineY     = 764
	defaultTextColor      = 0x22
	defaultSubtitleColor  = 0x66
)

var (
	fontLoadOnce  sync.Once
	loadedFont    *sfnt.Font
	loadedFontErr error
)

var (
	blueNoiseOnce        sync.Once
	blueNoiseTexture     []uint8
	blueNoiseTextureSize = 32 // 32×32 蓝噪声纹理，在 480×800 画面上无可见平铺感
)

var (
	// paletteGDEM075F52 调色板顺序必须与硬件 nibble 值一一对应：
	// EINK_BLACK=0x0, EINK_WHITE=0x1, EINK_YELLOW=0x2, EINK_RED=0x3
	paletteGDEM075F52 = []color.NRGBA{
		{R: 0, G: 0, B: 0, A: 255},       // index 0 → nibble 0x0 = Black
		{R: 255, G: 255, B: 255, A: 255}, // index 1 → nibble 0x1 = White
		{R: 233, G: 188, B: 41, A: 255},  // index 2 → nibble 0x2 = Yellow
		{R: 196, G: 44, B: 29, A: 255},   // index 3 → nibble 0x3 = Red
	}
	// paletteSpectra6 调色板顺序必须与硬件 nibble 值一一对应：
	// EINK_BLACK=0x0, EINK_WHITE=0x1, EINK_YELLOW=0x2, EINK_RED=0x3,
	// (0x4 保留/无效), EINK_BLUE=0x5, EINK_GREEN=0x6
	// 由于 0x4 无效，填入接近黑色的占位色，量化时不会选中
	paletteSpectra6 = []color.NRGBA{
		{R: 0, G: 0, B: 0, A: 255},       // index 0 → nibble 0x0 = Black
		{R: 255, G: 255, B: 255, A: 255}, // index 1 → nibble 0x1 = White
		{R: 164, G: 154, B: 49, A: 255},  // index 2 → nibble 0x2 = Yellow
		{R: 126, G: 39, B: 39, A: 255},   // index 3 → nibble 0x3 = Red
		{R: 1, G: 1, B: 1, A: 255},       // index 4 → nibble 0x4 = 无效（占位，量化时极少被选中）
		{R: 31, G: 71, B: 139, A: 255},   // index 5 → nibble 0x5 = Blue
		{R: 54, G: 78, B: 68, A: 255},    // index 6 → nibble 0x6 = Green
	}
)

func BuiltinRenderProfiles() []RenderProfile {
	return []RenderProfile{
		{
			Name:             "gdem075f52_480x800_4color",
			DisplayName:      "GDEM075F52 480x800 四色",
			CanvasTemplate:   DefaultCanvasTemplate,
			Width:            480,
			Height:           800,
			PaletteName:      "bwry4",
			DitherMode:       "atkinson",
			Palette:          paletteGDEM075F52,
			DefaultForDevice: true,
		},
		{
			Name:            "spectra6_480x800",
			DisplayName:     "Spectra 6 480x800",
			CanvasTemplate:  DefaultCanvasTemplate,
			Width:           480,
			Height:          800,
			PaletteName:     "spectra6",
			DitherMode:      "floyd_steinberg",
			GammaCorrection: 0.9,
			Palette:         paletteSpectra6,
		},
		{
			Name:            "spectra6_1600x1200_portrait",
			DisplayName:     "Spectra 6 1200x1600 竖版（预留）",
			CanvasTemplate:  "canvas_portrait_1200x1600_v1",
			Width:           1200,
			Height:          1600,
			PaletteName:     "spectra6",
			DitherMode:      "floyd_steinberg",
			GammaCorrection: 0.9,
			Palette:         paletteSpectra6,
		},
		{
			Name:            "spectra6_1600x1200_landscape",
			DisplayName:     "Spectra 6 1600x1200 横版（预留）",
			CanvasTemplate:  "canvas_landscape_1600x1200_v1",
			Width:           1600,
			Height:          1200,
			PaletteName:     "spectra6",
			DitherMode:      "floyd_steinberg",
			GammaCorrection: 0.9,
			Palette:         paletteSpectra6,
		},
	}
}

func GetRenderProfile(name string) (RenderProfile, bool) {
	for _, profile := range BuiltinRenderProfiles() {
		if profile.Name == name {
			return profile, true
		}
	}
	return RenderProfile{}, false
}

func DefaultRenderProfile() string {
	for _, profile := range BuiltinRenderProfiles() {
		if profile.DefaultForDevice {
			return profile.Name
		}
	}
	return "gdem075f52_480x800_4color"
}

func ActiveEmbeddedRenderProfiles() []RenderProfile {
	profiles := BuiltinRenderProfiles()
	active := make([]RenderProfile, 0, 2)
	for _, profile := range profiles {
		if profile.Width == 480 && profile.Height == 800 {
			active = append(active, profile)
		}
	}
	return active
}

func DisplayBatchRoot(thumbnailRoot string) string {
	if thumbnailRoot == "" {
		return filepath.Clean("./data/display-batches")
	}
	base := filepath.Dir(filepath.Clean(thumbnailRoot))
	return filepath.Join(base, "display-batches")
}

// BuildDisplayCanvas 从原始照片构建竖屏 canvas（480×800），在内存中返回，不经过 JPEG 中转
func BuildDisplayCanvas(filePath string, width, height int, title, subtitle string) (image.Image, error) {
	return BuildDisplayCanvasWithRotation(filePath, width, height, title, subtitle, 0)
}

// BuildDisplayCanvasWithRotation 构建展示画布，先自动校正方向再叠加手动旋转
// manualRotation: 用户手动旋转角度（0/90/180/270）
func BuildDisplayCanvasWithRotation(filePath string, width, height int, title, subtitle string, manualRotation int) (image.Image, error) {
	img, err := OpenImage(filePath)
	if err != nil {
		return nil, err
	}
	// 自动校正方向（非 HEIC 从 EXIF 读取，HEIC 由解码器自动处理）
	img = normalizeImageForDisplay(filePath, img)
	// 叠加手动旋转（所有格式统一）
	img = ApplyManualRotation(img, manualRotation)
	return buildDisplayCanvas(img, width, height, title, subtitle), nil
}

// SaveDisplayPreview 将 canvas 保存为 JPEG，仅用于网页预览
func SaveDisplayPreview(canvas image.Image, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return imaging.Save(canvas, outPath, imaging.JPEGQuality(88))
}

func GenerateDisplayPreview(filePath, outPath string, width, height int, title, subtitle string) error {
	canvas, err := BuildDisplayCanvas(filePath, width, height, title, subtitle)
	if err != nil {
		return err
	}
	return SaveDisplayPreview(canvas, outPath)
}

func buildDisplayCanvas(img image.Image, width, height int, title, subtitle string) image.Image {
	canvas := imaging.New(width, height, color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	// 上方：照片区域 (480×640)，抖动处理
	photo := GenerateFramePreview(img, width, displayPhotoHeight)
	draw.Draw(canvas, image.Rect(0, 0, width, displayPhotoHeight), photo, image.Point{}, draw.Src)

	// 下方：信息区域 (480×160)，纯白底黑字，不抖动
	title = strings.TrimSpace(title)
	subtitle = strings.TrimSpace(subtitle)
	renderCenteredTitle(canvas, title, color.NRGBA{R: defaultTextColor, G: defaultTextColor, B: defaultTextColor, A: 255})
	renderCenteredText(canvas, subtitle, subtitleFontSize, subtitleBaselineY, color.NRGBA{R: defaultSubtitleColor, G: defaultSubtitleColor, B: defaultSubtitleColor, A: 255})

	return canvas
}

func renderCenteredTitle(img *image.NRGBA, text string, textColor color.NRGBA) {
	if strings.TrimSpace(text) == "" {
		return
	}

	maxWidth := img.Bounds().Dx() - infoHorizontalPadding*2
	face := loadFontFace(titleFontSize)
	defer closeFontFace(face)

	lines := wrapTextToLines(face, text, maxWidth, 2)
	if len(lines) == 0 {
		return
	}
	baselineY := titleSingleBaselineY
	if len(lines) > 1 {
		baselineY = titleFirstBaselineY
	}
	for idx, line := range lines {
		renderTextLine(img, face, line, baselineY+idx*titleLineGap, textColor)
	}
}

func renderCenteredText(img *image.NRGBA, text string, size float64, baselineY int, textColor color.NRGBA) {
	if strings.TrimSpace(text) == "" {
		return
	}

	maxWidth := img.Bounds().Dx() - infoHorizontalPadding*2
	face := loadFontFace(size)
	defer closeFontFace(face)

	truncated := truncateTextToWidth(face, text, maxWidth)
	if truncated == "" {
		return
	}
	renderTextLine(img, face, truncated, baselineY, textColor)
}

func renderTextLine(img *image.NRGBA, face font.Face, text string, baselineY int, textColor color.NRGBA) {
	if strings.TrimSpace(text) == "" {
		return
	}
	width := font.MeasureString(face, text).Round()
	x := (img.Bounds().Dx() - width) / 2
	if x < infoHorizontalPadding {
		x = infoHorizontalPadding
	}
	drawer := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(textColor),
		Face: face,
		Dot:  fixed.P(x, baselineY),
	}
	drawer.DrawString(text)
}

func wrapTextToLines(face font.Face, text string, maxWidth, maxLines int) []string {
	text = strings.TrimSpace(text)
	if text == "" || maxLines <= 0 {
		return nil
	}
	if font.MeasureString(face, text).Round() <= maxWidth {
		return []string{text}
	}
	runes := []rune(text)
	lines := make([]string, 0, maxLines)
	remaining := runes
	for lineIndex := 0; lineIndex < maxLines && len(remaining) > 0; lineIndex++ {
		if lineIndex == maxLines-1 {
			lines = append(lines, truncateTextToWidth(face, string(remaining), maxWidth))
			break
		}
		split := bestLineBreak(face, remaining, maxWidth)
		if split <= 0 || split >= len(remaining) {
			lines = append(lines, truncateTextToWidth(face, string(remaining), maxWidth))
			break
		}
		lines = append(lines, strings.TrimSpace(string(remaining[:split])))
		remaining = trimLeadingSpaces(remaining[split:])
	}
	return lines
}

func bestLineBreak(face font.Face, runes []rune, maxWidth int) int {
	best := 0
	for idx := 1; idx <= len(runes); idx++ {
		candidate := strings.TrimSpace(string(runes[:idx]))
		if candidate == "" {
			continue
		}
		if font.MeasureString(face, candidate).Round() > maxWidth {
			break
		}
		best = idx
	}
	return best
}

func trimLeadingSpaces(runes []rune) []rune {
	for len(runes) > 0 && (runes[0] == ' ' || runes[0] == '\t' || runes[0] == '\n') {
		runes = runes[1:]
	}
	return runes
}

func truncateTextToWidth(face font.Face, text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if font.MeasureString(face, text).Round() <= maxWidth {
		return text
	}
	runes := []rune(text)
	ellipsis := "…"
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		candidate := string(runes) + ellipsis
		if font.MeasureString(face, candidate).Round() <= maxWidth {
			return candidate
		}
	}
	return ellipsis
}

func loadFontFace(size float64) font.Face {
	fontLoadOnce.Do(func() {
		loadedFont, loadedFontErr = loadPreferredFont()
	})
	if loadedFontErr != nil || loadedFont == nil {
		return basicfont.Face7x13
	}
	face, err := opentype.NewFace(loadedFont, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return basicfont.Face7x13
	}
	return face
}

func closeFontFace(face font.Face) {
	if closer, ok := face.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

func loadPreferredFont() (*sfnt.Font, error) {
	candidates := append(projectFontCandidates(), []string{
		"/System/Library/Fonts/Hiragino Sans GB.ttc",
		"/System/Library/Fonts/STHeiti Medium.ttc",
		"/System/Library/AssetsV2/com_apple_MobileAsset_Font8/53fe5be564086fefc7523ccd0a31200acf92e0e5.asset/AssetData/STHEITI.ttf",
		"/app/fonts/GlowSansSC-Normal-Light.otf",
		"/app/assets/fonts/GlowSansSC-Normal-Light.otf",
		"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/TTF/DejaVuSans.ttf",
	}...)
	for _, candidate := range candidates {
		fontFile, err := loadFontFile(candidate)
		if err == nil && fontFile != nil {
			return fontFile, nil
		}
	}
	return nil, fmt.Errorf("no usable font found")
}

func projectFontCandidates() []string {
	candidates := []string{
		"./backend/assets/fonts/GlowSansSC-Normal-Light.otf",
		"./assets/fonts/GlowSansSC-Normal-Light.otf",
	}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "assets/fonts/GlowSansSC-Normal-Light.otf"),
			filepath.Join(exeDir, "../assets/fonts/GlowSansSC-Normal-Light.otf"),
		)
	}

	if workDir, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(workDir, "backend/assets/fonts/GlowSansSC-Normal-Light.otf"),
			filepath.Join(workDir, "assets/fonts/GlowSansSC-Normal-Light.otf"),
		)
	}

	return candidates
}

func loadFontFile(path string) (*sfnt.Font, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parsed, err := sfnt.Parse(data)
	if err == nil {
		return parsed, nil
	}
	collection, collectionErr := sfnt.ParseCollection(data)
	if collectionErr != nil {
		return nil, err
	}
	return collection.Font(0)
}

func BuildRenderArtifacts(canvas image.Image, profile RenderProfile, ditherPreviewPath, binPath, headerPath string) (string, int64, error) {
	img := canvas
	if img.Bounds().Dx() != profile.Width || img.Bounds().Dy() != profile.Height {
		img = imaging.Fill(img, profile.Width, profile.Height, imaging.Center, imaging.Lanczos)
	}

	indexed := quantizeToPalette(img, profile)
	if err := os.MkdirAll(filepath.Dir(ditherPreviewPath), 0o755); err != nil {
		return "", 0, err
	}
	if err := saveDitherPreview(indexed, profile, ditherPreviewPath); err != nil {
		return "", 0, err
	}
	payload := encodeIndexedBinary(indexed, profile)
	checksum := sha256.Sum256(payload)
	checksumHex := hex.EncodeToString(checksum[:])

	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.WriteFile(binPath, payload, 0o644); err != nil {
		return "", 0, err
	}

	if err := os.MkdirAll(filepath.Dir(headerPath), 0o755); err != nil {
		return "", 0, err
	}
	header := buildHeaderFile(filepath.Base(headerPath), payload)
	if err := os.WriteFile(headerPath, []byte(header), 0o644); err != nil {
		return "", 0, err
	}

	return checksumHex, int64(len(payload)), nil
}

func buildHeaderFile(fileName string, payload []byte) string {
	varName := sanitizeHeaderVarName(strings.TrimSuffix(fileName, filepath.Ext(fileName)))
	var builder strings.Builder
	builder.WriteString("#pragma once\n\n")
	builder.WriteString(fmt.Sprintf("static const unsigned int %s_len = %d;\n", varName, len(payload)))
	builder.WriteString(fmt.Sprintf("static const unsigned char %s[] = {\n", varName))
	for idx, value := range payload {
		if idx%12 == 0 {
			builder.WriteString("    ")
		}
		builder.WriteString(fmt.Sprintf("0x%02x", value))
		if idx != len(payload)-1 {
			builder.WriteString(", ")
		}
		if idx%12 == 11 || idx == len(payload)-1 {
			builder.WriteString("\n")
		}
	}
	builder.WriteString("};\n")
	return builder.String()
}

func sanitizeHeaderVarName(name string) string {
	name = strings.ToLower(name)
	var builder strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			continue
		}
		builder.WriteRune('_')
	}
	return strings.Trim(builder.String(), "_")
}

func saveDitherPreview(indexed []uint8, profile RenderProfile, outPath string) error {
	img := image.NewNRGBA(image.Rect(0, 0, profile.Width, profile.Height))
	for idx, paletteIndex := range indexed {
		if idx >= profile.Width*profile.Height {
			break
		}
		x := idx % profile.Width
		y := idx / profile.Width
		colorIndex := int(paletteIndex)
		if colorIndex < 0 || colorIndex >= len(profile.Palette) {
			colorIndex = 0
		}
		img.SetNRGBA(x, y, profile.Palette[colorIndex])
	}
	return imaging.Save(img, outPath, imaging.JPEGQuality(92))
}

// rotateIndexed90CCW 将像素索引数组逆时针旋转 90°
// 源尺寸 srcWidth×srcHeight（竖屏），目标尺寸 srcHeight×srcWidth（横屏）
// 旋转逻辑与 ESP32 display_driver.cpp displayRotated() 保持一致：
//
//	dst_x = srcHeight - 1 - src_y
//	dst_y = src_x
func rotateIndexed90CCW(indexed []uint8, srcWidth, srcHeight int) []uint8 {
	dstWidth := srcHeight
	dstHeight := srcWidth
	rotated := make([]uint8, dstWidth*dstHeight)
	for srcY := 0; srcY < srcHeight; srcY++ {
		for srcX := 0; srcX < srcWidth; srcX++ {
			dstX := srcHeight - 1 - srcY
			dstY := srcX
			rotated[dstY*dstWidth+dstX] = indexed[srcY*srcWidth+srcX]
		}
	}
	return rotated
}

func encodeIndexedBinary(indexed []uint8, profile RenderProfile) []byte {
	// 旋转 90°（逆时针）后打包为 4bit 格式：每2个像素1字节
	// 输入：profile.Width × profile.Height（竖屏，如 480×800）
	// 输出：profile.Height × profile.Width（横屏，如 800×480），供 ESP32 直接 display()
	rotated := rotateIndexed90CCW(indexed, profile.Width, profile.Height)

	totalPixels := len(rotated)
	output := make([]byte, (totalPixels+1)/2)

	for i := 0; i < totalPixels; i += 2 {
		pixel1 := rotated[i] & 0x0F
		pixel2 := uint8(0)
		if i+1 < totalPixels {
			pixel2 = rotated[i+1] & 0x0F
		}
		output[i/2] = (pixel1 << 4) | pixel2
	}

	return output
}

func quantizeToPalette(img image.Image, profile RenderProfile) []uint8 {
	// 流水线第一步：Gamma 校正（在 RGB 空间预处理，补偿墨水屏显示特性）
	if gamma := profile.GammaCorrection; gamma > 0 && math.Abs(gamma-1.0) > 1e-3 {
		img = applyGammaCorrection(img, gamma)
	}

	// 流水线第二步：预处理增强（仅对照片区域，在抖动前提升细节保留）
	// Sharpen: 轻度 Unsharp Mask，补偿量化导致的细节丢失
	// AdjustSigmoid: S 曲线对比度增强，拉开中间调层次
	img = imaging.Sharpen(img, 0.5)
	img = imaging.AdjustSigmoid(img, 0.5, 5.0)

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width == 0 || height == 0 {
		return nil
	}

	photoHeight := displayPhotoHeightForProfile(height)
	if photoHeight <= 0 {
		return quantizeDirect(img, profile.Palette)
	}
	if photoHeight >= height {
		switch profile.DitherMode {
		case "floyd_steinberg":
			return quantizeFloydSteinberg(img, profile.Palette)
		case "atkinson":
			return quantizeAtkinson(img, profile.Palette)
		case "blue_noise":
			return quantizeBlueNoise(img, profile.Palette)
		case "ordered":
			fallthrough
		default:
			return quantizeOrdered(img, profile.Palette)
		}
	}

	// 竖屏布局：上方照片区域（抖动），下方信息区域（纯黑白）
	photoRect := image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Min.X+width, bounds.Min.Y+photoHeight)
	infoRect := image.Rect(bounds.Min.X, bounds.Min.Y+photoHeight, bounds.Min.X+width, bounds.Min.Y+height)

	var photoIndexed []uint8
	switch profile.DitherMode {
	case "floyd_steinberg":
		photoIndexed = quantizeFloydSteinbergRegion(img, profile.Palette, photoRect)
	case "atkinson":
		photoIndexed = quantizeAtkinsonRegion(img, profile.Palette, photoRect)
	case "blue_noise":
		photoIndexed = quantizeBlueNoiseRegion(img, profile.Palette, photoRect)
	case "ordered":
		fallthrough
	default:
		photoIndexed = quantizeOrderedRegion(img, profile.Palette, photoRect)
	}
	infoIndexed := quantizeInfoRegionBlackWhite(img, profile.Palette, infoRect)
	return append(photoIndexed, infoIndexed...)
}

func displayPhotoHeightForProfile(totalHeight int) int {
	if totalHeight <= 0 {
		return 0
	}
	photoHeight := int(math.Round(float64(totalHeight) * float64(displayPhotoHeight) / float64(displayCanvasHeight)))
	if photoHeight < 0 {
		return 0
	}
	if photoHeight > totalHeight {
		return totalHeight
	}
	return photoHeight
}

var bayer4 = [4][4]float64{
	{0, 8, 2, 10},
	{12, 4, 14, 6},
	{3, 11, 1, 9},
	{15, 7, 13, 5},
}

func quantizeAtkinson(img image.Image, palette []color.NRGBA) []uint8 {
	return quantizeAtkinsonRegion(img, palette, img.Bounds())
}

// quantizeAtkinsonRegion 实现 Atkinson 误差扩散（Bill Atkinson, Apple 1987）。
// 只传播 6/8 = 3/4 的误差（vs Floyd-Steinberg 的 16/16），有意丢弃 1/4，
// 使高对比度区域保持锐利，中间调产生干净的点阵纹理，适合少色调色板。
// 扩散模板（每格 1/8 误差）：
//
//	    X  1/8  1/8
//	1/8  1/8  1/8
//	    1/8
func quantizeAtkinsonRegion(img image.Image, palette []color.NRGBA, rect image.Rectangle) []uint8 {
	width := rect.Dx()
	height := rect.Dy()
	indexed := make([]uint8, width*height)
	r := make([][]float64, height)
	g := make([][]float64, height)
	b := make([][]float64, height)
	for y := 0; y < height; y++ {
		r[y] = make([]float64, width)
		g[y] = make([]float64, width)
		b[y] = make([]float64, width)
		for x := 0; x < width; x++ {
			// 对有色调像素做色度增益，帮助 Red/Yellow 与 Black/White 竞争
			raw := color.NRGBAModel.Convert(img.At(rect.Min.X+x, rect.Min.Y+y)).(color.NRGBA)
			boosted := boostChroma(raw, orderedDitherChromaBoost)
			r[y][x] = float64(boosted.R)
			g[y][x] = float64(boosted.G)
			b[y][x] = float64(boosted.B)
		}
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			current := color.NRGBA{R: clampByte(r[y][x]), G: clampByte(g[y][x]), B: clampByte(b[y][x]), A: 255}
			idx := nearestPaletteIndex(current, palette)
			indexed[y*width+x] = uint8(idx)
			selected := palette[idx]
			errR := r[y][x] - float64(selected.R)
			errG := g[y][x] - float64(selected.G)
			errB := b[y][x] - float64(selected.B)
			// 6 邻居各传播 1/8，总计传播 6/8（有意丢弃 2/8）
			spreadError(r, g, b, x+1, y, width, height, errR, errG, errB, 1.0/8.0)
			spreadError(r, g, b, x+2, y, width, height, errR, errG, errB, 1.0/8.0)
			spreadError(r, g, b, x-1, y+1, width, height, errR, errG, errB, 1.0/8.0)
			spreadError(r, g, b, x, y+1, width, height, errR, errG, errB, 1.0/8.0)
			spreadError(r, g, b, x+1, y+1, width, height, errR, errG, errB, 1.0/8.0)
			spreadError(r, g, b, x, y+2, width, height, errR, errG, errB, 1.0/8.0)
		}
	}
	return indexed
}

func quantizeOrdered(img image.Image, palette []color.NRGBA) []uint8 {
	return quantizeOrderedRegion(img, palette, img.Bounds())
}

// orderedDitherChromaBoost 是有序抖动的 Lab 色度增益系数。
// 对无色像素（a≈0, b≈0）无影响；对有色调像素（暖黄/暖红）会向调色板彩色条目推移，
// 避免大量像素因亮度接近而全部量化到黑/白。
const orderedDitherChromaBoost = 1.8

func quantizeOrderedRegion(img image.Image, palette []color.NRGBA, rect image.Rectangle) []uint8 {
	width := rect.Dx()
	height := rect.Dy()
	indexed := make([]uint8, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			current := color.NRGBAModel.Convert(img.At(rect.Min.X+x, rect.Min.Y+y)).(color.NRGBA)
			// 在 Lab 空间增强色度，使有色调像素更易量化到彩色调色板条目
			current = boostChroma(current, orderedDitherChromaBoost)
			shift := (bayer4[y%4][x%4] - 7.5) * 6
			current.R = clampByte(float64(current.R) + shift)
			current.G = clampByte(float64(current.G) + shift)
			current.B = clampByte(float64(current.B) + shift)
			indexed[y*width+x] = uint8(nearestPaletteIndex(current, palette))
		}
	}
	return indexed
}

func quantizeFloydSteinberg(img image.Image, palette []color.NRGBA) []uint8 {
	return quantizeFloydSteinbergRegion(img, palette, img.Bounds())
}

// quantizeFloydSteinbergRegion 使用蛇形扫描（serpentine scanning）的 Floyd-Steinberg 误差扩散。
// 奇数行从右到左扫描，消除单向扫描产生的方向性蛇形伪影。
func quantizeFloydSteinbergRegion(img image.Image, palette []color.NRGBA, rect image.Rectangle) []uint8 {
	width := rect.Dx()
	height := rect.Dy()
	indexed := make([]uint8, width*height)
	r := make([][]float64, height)
	g := make([][]float64, height)
	b := make([][]float64, height)
	for y := 0; y < height; y++ {
		r[y] = make([]float64, width)
		g[y] = make([]float64, width)
		b[y] = make([]float64, width)
		for x := 0; x < width; x++ {
			c := color.NRGBAModel.Convert(img.At(rect.Min.X+x, rect.Min.Y+y)).(color.NRGBA)
			c = boostChroma(c, blueNoiseChromaBoost)
			r[y][x] = float64(c.R)
			g[y][x] = float64(c.G)
			b[y][x] = float64(c.B)
		}
	}
	for y := 0; y < height; y++ {
		leftToRight := y%2 == 0
		for xRel := 0; xRel < width; xRel++ {
			var x int
			if leftToRight {
				x = xRel
			} else {
				x = width - 1 - xRel
			}
			current := color.NRGBA{R: clampByte(r[y][x]), G: clampByte(g[y][x]), B: clampByte(b[y][x]), A: 255}
			idx := nearestPaletteIndex(current, palette)
			indexed[y*width+x] = uint8(idx)
			selected := palette[idx]
			errR := r[y][x] - float64(selected.R)
			errG := g[y][x] - float64(selected.G)
			errB := b[y][x] - float64(selected.B)
			if leftToRight {
				// 偶数行：从左到右，误差向右和右下扩散
				spreadError(r, g, b, x+1, y, width, height, errR, errG, errB, 7.0/16.0)
				spreadError(r, g, b, x-1, y+1, width, height, errR, errG, errB, 3.0/16.0)
				spreadError(r, g, b, x, y+1, width, height, errR, errG, errB, 5.0/16.0)
				spreadError(r, g, b, x+1, y+1, width, height, errR, errG, errB, 1.0/16.0)
			} else {
				// 奇数行：从右到左，误差向左和左下扩散
				spreadError(r, g, b, x-1, y, width, height, errR, errG, errB, 7.0/16.0)
				spreadError(r, g, b, x+1, y+1, width, height, errR, errG, errB, 3.0/16.0)
				spreadError(r, g, b, x, y+1, width, height, errR, errG, errB, 5.0/16.0)
				spreadError(r, g, b, x-1, y+1, width, height, errR, errG, errB, 1.0/16.0)
			}
		}
	}
	return indexed
}

func quantizeDirect(img image.Image, palette []color.NRGBA) []uint8 {
	return quantizeDirectRegion(img, palette, img.Bounds())
}

func quantizeDirectRegion(img image.Image, palette []color.NRGBA, rect image.Rectangle) []uint8 {
	width := rect.Dx()
	height := rect.Dy()
	indexed := make([]uint8, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			current := color.NRGBAModel.Convert(img.At(rect.Min.X+x, rect.Min.Y+y)).(color.NRGBA)
			indexed[y*width+x] = uint8(nearestPaletteIndex(current, palette))
		}
	}
	return indexed
}

func quantizeInfoRegionBlackWhite(img image.Image, palette []color.NRGBA, rect image.Rectangle) []uint8 {
	width := rect.Dx()
	height := rect.Dy()
	indexed := make([]uint8, width*height)
	whiteIndex := nearestPaletteIndex(color.NRGBA{R: 255, G: 255, B: 255, A: 255}, palette)
	blackIndex := nearestPaletteIndex(color.NRGBA{R: 0, G: 0, B: 0, A: 255}, palette)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			current := color.NRGBAModel.Convert(img.At(rect.Min.X+x, rect.Min.Y+y)).(color.NRGBA)
			if infoLuminance(current) >= infoBinaryThreshold {
				indexed[y*width+x] = uint8(whiteIndex)
				continue
			}
			indexed[y*width+x] = uint8(blackIndex)
		}
	}

	return indexed
}

func infoLuminance(c color.NRGBA) float64 {
	return 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
}

func spreadError(r, g, b [][]float64, x, y, width, height int, errR, errG, errB, factor float64) {
	if x < 0 || x >= width || y < 0 || y >= height {
		return
	}
	r[y][x] = clampFloat(r[y][x] + errR*factor)
	g[y][x] = clampFloat(g[y][x] + errG*factor)
	b[y][x] = clampFloat(b[y][x] + errB*factor)
}

// nearestPaletteIndex 使用 CIE Lab 欧氏距离找到调色板中感知最近的颜色。
// 对有限的 6 色调色板，Lab 欧氏距离比 CIEDE2000 更适合：
// Lab 欧氏距离对色度差异有较强惩罚，使中性/近中性像素稳定映射到黑白轴，
// 不会因 CIEDE2000 的 SC/SH 归一化而将皮肤色误判为黄色、冷调灰误判为蓝色。
func nearestPaletteIndex(current color.NRGBA, palette []color.NRGBA) int {
	L1, a1, b1 := rgbToLab(current)
	bestIndex := 0
	bestDist := math.MaxFloat64
	for idx, candidate := range palette {
		// 跳过 Spectra 6 调色板中硬件保留的无效索引 4（占位色）
		if idx == 4 && len(palette) > 4 {
			continue
		}
		L2, a2, b2 := rgbToLab(candidate)
		dL, da, db := L1-L2, a1-a2, b1-b2
		d := dL*dL + da*da + db*db
		if d < bestDist {
			bestDist = d
			bestIndex = idx
		}
	}
	return bestIndex
}

// rgbToLab 将 sRGB 颜色转换为 CIE Lab（D65 白点）。
func rgbToLab(c color.NRGBA) (L, a, b float64) {
	r := srgbLinearize(float64(c.R) / 255.0)
	g := srgbLinearize(float64(c.G) / 255.0)
	bl := srgbLinearize(float64(c.B) / 255.0)
	// Linear sRGB → XYZ (D65)
	x := 0.4124*r + 0.3576*g + 0.1805*bl
	y := 0.2126*r + 0.7152*g + 0.0722*bl
	z := 0.0193*r + 0.1192*g + 0.9505*bl
	// XYZ → Lab (D65 参考白点)
	x /= 0.95047
	y /= 1.00000
	z /= 1.08883
	fx := labF(x)
	fy := labF(y)
	fz := labF(z)
	L = 116*fy - 16
	a = 500 * (fx - fy)
	b = 200 * (fy - fz)
	return
}

func srgbLinearize(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func labF(t float64) float64 {
	if t > 0.008856 {
		return math.Cbrt(t)
	}
	return 7.787*t + 16.0/116.0
}

func clampByte(value float64) uint8 {
	return uint8(clampFloat(value))
}

func clampFloat(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return value
}

// boostChroma 在 Lab 空间对色度（a, b 分量）应用增益，增强有色调像素与彩色调色板条目的亲和力。
// 对无色像素（a≈0, b≈0）几乎无影响。
func boostChroma(c color.NRGBA, factor float64) color.NRGBA {
	L, a, b := rgbToLab(c)
	return labToRGB(L, a*factor, b*factor)
}

// labToRGB 将 CIE Lab（D65 白点）转换回 sRGB 颜色，是 rgbToLab 的逆变换。
func labToRGB(L, a, b float64) color.NRGBA {
	fy := (L + 16) / 116
	fx := a/500 + fy
	fz := fy - b/200
	labFinv := func(t float64) float64 {
		t3 := t * t * t
		if t3 > 0.008856 {
			return t3
		}
		return (t - 16.0/116.0) / 7.787
	}
	x := labFinv(fx) * 0.95047
	y := labFinv(fy) * 1.00000
	z := labFinv(fz) * 1.08883
	// XYZ → linear sRGB (D65)
	rl := 3.2406*x - 1.5372*y - 0.4986*z
	gl := -0.9689*x + 1.8758*y + 0.0415*z
	bl := 0.0557*x - 0.2040*y + 1.0570*z
	return color.NRGBA{
		R: clampByte(srgbDelinearize(rl) * 255),
		G: clampByte(srgbDelinearize(gl) * 255),
		B: clampByte(srgbDelinearize(bl) * 255),
		A: 255,
	}
}

// srgbDelinearize 将线性光值转换回 sRGB gamma 编码值，是 srgbLinearize 的逆变换。
func srgbDelinearize(v float64) float64 {
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1.0/2.4) - 0.055
}

// ── CIEDE2000 ────────────────────────────────────────────────────────────────

// ciede2000 计算两个 Lab 颜色之间的 CIEDE2000 感知色差（Sharma et al., 2005）。
// 相比 Lab 欧氏距离，CIEDE2000 对亮度、彩度、色相加入非线性加权，
// 并引入旋转项 RT 修正蓝色区域的感知不对称性，感知线性度更高。
func ciede2000(L1, a1, b1, L2, a2, b2 float64) float64 {
	const (
		deg2rad  = math.Pi / 180.0
		pow25to7 = 6103515625.0 // 25^7
	)

	// Step 1：计算 C*ab 均值，推导 G 系数，修正 a'
	cab1 := math.Sqrt(a1*a1 + b1*b1)
	cab2 := math.Sqrt(a2*a2 + b2*b2)
	cabMean := (cab1 + cab2) / 2.0
	cabMean7 := math.Pow(cabMean, 7)
	G := 0.5 * (1 - math.Sqrt(cabMean7/(cabMean7+pow25to7)))

	a1p := a1 * (1 + G)
	a2p := a2 * (1 + G)

	// C' 和 h'
	C1p := math.Sqrt(a1p*a1p + b1*b1)
	C2p := math.Sqrt(a2p*a2p + b2*b2)

	h1p := math.Atan2(b1, a1p) * (180.0 / math.Pi)
	if h1p < 0 {
		h1p += 360
	}
	h2p := math.Atan2(b2, a2p) * (180.0 / math.Pi)
	if h2p < 0 {
		h2p += 360
	}

	// Step 2：ΔL', ΔC', Δh', ΔH'
	dLp := L2 - L1
	dCp := C2p - C1p

	var dhp float64
	if C1p == 0 || C2p == 0 {
		dhp = 0
	} else {
		diff := h2p - h1p
		switch {
		case math.Abs(diff) <= 180:
			dhp = diff
		case diff > 180:
			dhp = diff - 360
		default:
			dhp = diff + 360
		}
	}
	dHp := 2 * math.Sqrt(C1p*C2p) * math.Sin(dhp/2*deg2rad)

	// Step 3：均值 L̄', C̄', h̄'
	Lpm := (L1 + L2) / 2.0
	Cpm := (C1p + C2p) / 2.0

	var hpm float64
	if C1p == 0 || C2p == 0 {
		hpm = h1p + h2p
	} else if math.Abs(h1p-h2p) <= 180 {
		hpm = (h1p + h2p) / 2.0
	} else if h1p+h2p < 360 {
		hpm = (h1p + h2p + 360) / 2.0
	} else {
		hpm = (h1p + h2p - 360) / 2.0
	}

	// 加权函数
	T := 1 -
		0.17*math.Cos((hpm-30)*deg2rad) +
		0.24*math.Cos(2*hpm*deg2rad) +
		0.32*math.Cos((3*hpm+6)*deg2rad) -
		0.20*math.Cos((4*hpm-63)*deg2rad)

	dL50 := (Lpm - 50) * (Lpm - 50)
	SL := 1 + 0.015*dL50/math.Sqrt(20+dL50)
	SC := 1 + 0.045*Cpm
	SH := 1 + 0.015*Cpm*T

	// 旋转项 RT（修正蓝色区域感知不对称）
	Cpm7 := math.Pow(Cpm, 7)
	RC := 2 * math.Sqrt(Cpm7/(Cpm7+pow25to7))
	dTheta := 30 * math.Exp(-math.Pow((hpm-275)/25, 2))
	RT := -math.Sin(2*dTheta*deg2rad) * RC

	val := math.Pow(dLp/SL, 2) +
		math.Pow(dCp/SC, 2) +
		math.Pow(dHp/SH, 2) +
		RT*(dCp/SC)*(dHp/SH)
	if val < 0 {
		val = 0 // 浮点误差保护
	}
	return math.Sqrt(val)
}

// ── Gamma 校正 ───────────────────────────────────────────────────────────────

// applyGammaCorrection 在 RGB 空间对图像做 Gamma 预处理。
// gamma < 1（如 0.9）：提亮，补偿墨水屏偏暗的显示特性。
// gamma > 1：压暗。
func applyGammaCorrection(img image.Image, gamma float64) *image.NRGBA {
	bounds := img.Bounds()
	out := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			out.SetNRGBA(x, y, color.NRGBA{
				R: gammaCorrectChannel(c.R, gamma),
				G: gammaCorrectChannel(c.G, gamma),
				B: gammaCorrectChannel(c.B, gamma),
				A: c.A,
			})
		}
	}
	return out
}

func gammaCorrectChannel(v uint8, gamma float64) uint8 {
	if v == 0 {
		return 0
	}
	if v == 255 {
		return 255
	}
	return clampByte(math.Pow(float64(v)/255.0, gamma) * 255.0)
}

// ── Blue Noise 抖动 ──────────────────────────────────────────────────────────

// getBlueNoise 返回懒加载的 32×32 蓝噪声阈值纹理（每个值 0–255）。
// 使用 "最大空隙优先" 算法（largest-void progressive fill）生成，
// 保证阈值图具备高频分布特性（蓝噪声），消除低频周期性伪影。
func getBlueNoise() []uint8 {
	blueNoiseOnce.Do(func() {
		blueNoiseTexture = generateBlueNoise(blueNoiseTextureSize)
	})
	return blueNoiseTexture
}

// generateBlueNoise 使用最大空隙逐步填充算法生成 size×size 蓝噪声阈值图。
// 算法思路：每次找能量最低（最孤立）的空位，放入下一个点，赋予递增的阈值。
// 预先计算环形（toroidal）高斯核，用增量更新能量，总复杂度 O(n²)。
func generateBlueNoise(size int) []uint8 {
	n := size * size
	sigma := 1.5

	// 预计算环形高斯核（支持无缝平铺）
	kernel := make([]float64, n*n)
	for i := 0; i < n; i++ {
		yi, xi := i/size, i%size
		for j := 0; j < n; j++ {
			yj, xj := j/size, j%size
			dx, dy := xi-xj, yi-yj
			if dx > size/2 {
				dx -= size
			} else if dx < -size/2 {
				dx += size
			}
			if dy > size/2 {
				dy -= size
			} else if dy < -size/2 {
				dy += size
			}
			kernel[i*n+j] = math.Exp(-float64(dx*dx+dy*dy) / (2 * sigma * sigma))
		}
	}

	energy := make([]float64, n)
	placed := make([]bool, n)
	result := make([]uint8, n)

	for rank := 0; rank < n; rank++ {
		// 找能量最低的空位（最大空隙）
		minE, best := math.MaxFloat64, 0
		for i := 0; i < n; i++ {
			if !placed[i] && energy[i] < minE {
				minE, best = energy[i], i
			}
		}
		placed[best] = true
		result[best] = uint8(rank * 255 / (n - 1))
		// 增量更新能量：将新放入点的高斯影响叠加到所有位置
		for i := 0; i < n; i++ {
			energy[i] += kernel[i*n+best]
		}
	}
	return result
}

// blueNoiseDitherStrength 控制蓝噪声抖动的扰动幅度（RGB 空间，0–255 刻度）。
// 值越大，过渡区抖动越明显；值越小，量化越接近直接取最近色。
// 对于 Spectra 6 调色板（颜色相距较远），28 能在过渡带产生自然的混色点阵。
const blueNoiseDitherStrength = 18.0

// blueNoiseChromaBoost 蓝噪声抖动的色度增强系数（Lab 空间）。
// 比 Atkinson 的 1.8 更温和，因为 6 色调色板有更多彩色条目可选。
const blueNoiseChromaBoost = 1.4

// quantizeBlueNoise 对整幅图像做蓝噪声有序抖动量化。
func quantizeBlueNoise(img image.Image, palette []color.NRGBA) []uint8 {
	return quantizeBlueNoiseRegion(img, palette, img.Bounds())
}

// quantizeBlueNoiseRegion 使用蓝噪声阈值图对指定区域做有序抖动量化。
// 流水线：读取像素 → 叠加蓝噪声扰动（RGB 空间）→ CIEDE2000 找最近调色板色 → 输出索引。
// 蓝噪声保证扰动具有高频分布：过渡区产生自然的点阵混色，无可见低频周期纹理。
func quantizeBlueNoiseRegion(img image.Image, palette []color.NRGBA, rect image.Rectangle) []uint8 {
	width := rect.Dx()
	height := rect.Dy()
	indexed := make([]uint8, width*height)
	bn := getBlueNoise()
	sz := blueNoiseTextureSize

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := color.NRGBAModel.Convert(img.At(rect.Min.X+x, rect.Min.Y+y)).(color.NRGBA)
			// 色度增强：让彩色像素更容易匹配到调色板中的彩色条目
			c = boostChroma(c, blueNoiseChromaBoost)
			// 蓝噪声值 [0,255] 映射到 [-strength/2, +strength/2] 的扰动
			bnVal := (float64(bn[(y%sz)*sz+(x%sz)])/255.0 - 0.5) * blueNoiseDitherStrength
			perturbed := color.NRGBA{
				R: clampByte(float64(c.R) + bnVal),
				G: clampByte(float64(c.G) + bnVal),
				B: clampByte(float64(c.B) + bnVal),
				A: 255,
			}
			indexed[y*width+x] = uint8(nearestPaletteIndex(perturbed, palette))
		}
	}
	return indexed
}
