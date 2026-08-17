package service

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writePlainJPEG 写入一张不带 EXIF（orientation=1）的纯 JPEG 测试图。
// 这样 service 层 PrepareV2FaceCrops 不会受 EXIF 校正影响，便于断言裁剪尺寸。
func writePlainJPEG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 100, A: 255})
		}
	}
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, jpeg.Encode(f, img, &jpeg.Options{Quality: 95}))
}

func TestPrepareV2FaceCrops_NoEXIF_NoRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.jpg")
	// 200×100 横图。人脸框归一化 (0.25,0.25,0.25,0.5) → 像素宽 0.25*200=50，高 0.5*100=50。
	writePlainJPEG(t, path, 200, 100)

	crops, err := PrepareV2FaceCrops(path, 0, 0.25, 0.25, 0.25, 0.5)
	require.NoError(t, err)

	assert.Equal(t, 200, crops.OriginalWidth)
	assert.Equal(t, 100, crops.OriginalHeight)
	assert.Equal(t, 50, crops.FaceBoxWidthPx)
	assert.Equal(t, 50, crops.FaceBoxHeightPx)

	// 上下文裁剪：人脸框 50×50，四周各扩 100% → ±50px。
	// faceRect = (50,25)-(100,75)；扩展后 (0,-25)-(150,125) 裁切到边界 → (0,0)-(150,100) = 150×100。
	assert.Equal(t, 150, crops.ContextCropWidthPx)
	assert.Equal(t, 100, crops.ContextCropHeightPx)
	// 人脸框在上下文裁剪中的偏移：faceRect.Min - ctxRect.Min = (50,25) - (0,0)。
	assert.Equal(t, 50, crops.FaceBoxOffsetX)
	assert.Equal(t, 25, crops.FaceBoxOffsetY)
	assert.NotEmpty(t, crops.ContextCropBase64)

	// 编码结果可解码回图像，且尺寸与记录一致。
	dec, err := base64.StdEncoding.DecodeString(crops.ContextCropBase64)
	require.NoError(t, err)
	m, err := jpeg.Decode(bytes.NewReader(dec))
	require.NoError(t, err)
	assert.Equal(t, crops.ContextCropWidthPx, m.Bounds().Dx())
}

func TestPrepareV2FaceCrops_ManualRotation90SwapsAxes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.jpg")
	// 200×100 横图，manual_rotation=90 → 校正后 100×200 竖图。
	// 人脸框 (0.25,0.25,0.25,0.5)：宽 0.25*100=25，高 0.5*200=100。
	writePlainJPEG(t, path, 200, 100)

	crops, err := PrepareV2FaceCrops(path, 90, 0.25, 0.25, 0.25, 0.5)
	require.NoError(t, err)

	// 旋转后原图维度交换。
	assert.Equal(t, 100, crops.OriginalWidth)
	assert.Equal(t, 200, crops.OriginalHeight)
	assert.Equal(t, 25, crops.FaceBoxWidthPx)
	assert.Equal(t, 100, crops.FaceBoxHeightPx)
}

func TestPrepareV2FaceCrops_EdgeBoxContextClamped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.jpg")
	// 100×100 图，人脸框贴近左上角 (0.0,0.0,0.25,0.25) → 像素 (0,0,25,25)。
	writePlainJPEG(t, path, 100, 100)

	crops, err := PrepareV2FaceCrops(path, 0, 0.0, 0.0, 0.25, 0.25)
	require.NoError(t, err)

	// 扩展 (−25,−25)-(50,50) 裁切到 (0,0)-(50,50) = 50×50（左上不越界）。
	assert.Equal(t, 50, crops.ContextCropWidthPx)
	assert.Equal(t, 50, crops.ContextCropHeightPx)
	// 人脸框贴近左上角，边界裁切后 offset=(0,0)。
	assert.Equal(t, 0, crops.FaceBoxOffsetX)
	assert.Equal(t, 0, crops.FaceBoxOffsetY)
	// 契约断言：边缘裁剪被截断后，目标脸框 offset 必须是真实的 (0,0)，而不是裁剪中心 (25,25)。
	// 下游 worker（people_service / face_quality_rescore）必须把这个 offset 原样转发到 v2 请求，
	// ML 端据此做目标框 IoU 匹配；任何把目标位置重置为裁剪中心的逻辑都会让边缘脸假阴性回归。
	assert.NotEqual(t, crops.ContextCropWidthPx/2, crops.FaceBoxOffsetX, "offset 不得被重置为裁剪中心")
	assert.NotEqual(t, crops.ContextCropHeightPx/2, crops.FaceBoxOffsetY, "offset 不得被重置为裁剪中心")
}

func TestPrepareV2FaceCrops_InvalidBBoxRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.jpg")
	writePlainJPEG(t, path, 100, 100)

	// 零框非法。
	_, err := PrepareV2FaceCrops(path, 0, 0.5, 0.5, 0, 0)
	assert.Error(t, err)

	// 越界非法。
	_, err = PrepareV2FaceCrops(path, 0, 0.9, 0.9, 0.5, 0.5)
	assert.Error(t, err)
}
