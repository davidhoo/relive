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
	Name             string
	DisplayName      string
	CanvasTemplate   string
	Width            int
	Height           int
	PaletteName      string
	DitherMode       string
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
	paletteGDEM075F52 = []color.NRGBA{
		{R: 0, G: 0, B: 0, A: 255},        // index 0 = Black  (硬件 nibble 0x0 = Black)
		{R: 255, G: 255, B: 255, A: 255},   // index 1 = White  (硬件 nibble 0x1 = White)
		{R: 196, G: 44, B: 29, A: 255},     // index 2 = Red
		{R: 233, G: 188, B: 41, A: 255},    // index 3 = Yellow
	}
	paletteSpectra6 = []color.NRGBA{
		{R: 0, G: 0, B: 0, A: 255},        // index 0 = Black  (硬件 nibble 0x0 = Black)
		{R: 255, G: 255, B: 255, A: 255},   // index 1 = White  (硬件 nibble 0x1 = White)
		{R: 196, G: 44, B: 29, A: 255},     // index 2 = Red
		{R: 233, G: 188, B: 41, A: 255},    // index 3 = Yellow
		{R: 44, G: 92, B: 180, A: 255},     // index 4 = Blue
		{R: 68, G: 146, B: 68, A: 255},     // index 5 = Green
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
			DitherMode:       "ordered",
			Palette:          paletteGDEM075F52,
			DefaultForDevice: true,
		},
		{
			Name:           "spectra6_480x800",
			DisplayName:    "Spectra 6 480x800",
			CanvasTemplate: DefaultCanvasTemplate,
			Width:          480,
			Height:         800,
			PaletteName:    "spectra6",
			DitherMode:     "floyd_steinberg",
			Palette:        paletteSpectra6,
		},
		{
			Name:           "spectra6_1600x1200_portrait",
			DisplayName:    "Spectra 6 1200x1600 竖版（预留）",
			CanvasTemplate: "canvas_portrait_1200x1600_v1",
			Width:          1200,
			Height:         1600,
			PaletteName:    "spectra6",
			DitherMode:     "floyd_steinberg",
			Palette:        paletteSpectra6,
		},
		{
			Name:           "spectra6_1600x1200_landscape",
			DisplayName:    "Spectra 6 1600x1200 横版（预留）",
			CanvasTemplate: "canvas_landscape_1600x1200_v1",
			Width:          1600,
			Height:         1200,
			PaletteName:    "spectra6",
			DitherMode:     "floyd_steinberg",
			Palette:        paletteSpectra6,
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
	img, err := OpenImage(filePath)
	if err != nil {
		return nil, err
	}
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

func quantizeOrdered(img image.Image, palette []color.NRGBA) []uint8 {
	return quantizeOrderedRegion(img, palette, img.Bounds())
}

func quantizeOrderedRegion(img image.Image, palette []color.NRGBA, rect image.Rectangle) []uint8 {
	width := rect.Dx()
	height := rect.Dy()
	indexed := make([]uint8, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			current := color.NRGBAModel.Convert(img.At(rect.Min.X+x, rect.Min.Y+y)).(color.NRGBA)
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
			r[y][x] = float64(c.R)
			g[y][x] = float64(c.G)
			b[y][x] = float64(c.B)
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
			spreadError(r, g, b, x+1, y, width, height, errR, errG, errB, 7.0/16.0)
			spreadError(r, g, b, x-1, y+1, width, height, errR, errG, errB, 3.0/16.0)
			spreadError(r, g, b, x, y+1, width, height, errR, errG, errB, 5.0/16.0)
			spreadError(r, g, b, x+1, y+1, width, height, errR, errG, errB, 1.0/16.0)
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

func nearestPaletteIndex(current color.NRGBA, palette []color.NRGBA) int {
	bestIndex := 0
	bestDistance := math.MaxFloat64
	for idx, candidate := range palette {
		dr := float64(current.R) - float64(candidate.R)
		dg := float64(current.G) - float64(candidate.G)
		db := float64(current.B) - float64(candidate.B)
		distance := dr*dr + dg*dg + db*db
		if distance < bestDistance {
			bestDistance = distance
			bestIndex = idx
		}
	}
	return bestIndex
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
