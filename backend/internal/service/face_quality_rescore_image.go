package service

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"math"

	"github.com/disintegration/imaging"
	"github.com/davidhoo/relive/internal/util"
)

// V2FaceCrops 是 v2 独立复核的裁剪产物。所有尺寸均为方向一致原图上的实际像素，
// 不得用缩略图或 ProcessForAI 压缩图尺寸替代。
type V2FaceCrops struct {
	OriginalWidth  int    // 校正后原图宽（px）
	OriginalHeight int    // 校正后原图高（px）
	FaceBoxWidthPx int    // 人脸框实际宽（px）
	FaceBoxHeightPx int   // 人脸框实际高（px）
	// FaceBoxOffsetX/Y 人脸框左上角在上下文裁剪中的像素偏移。
	// ML 端必须用 (offset_x, offset_y, face_box_width_px, face_box_height_px) 精确裁取人脸框区域
	// 计算原图质量指标，不得用裁剪中心近似。
	FaceBoxOffsetX int
	FaceBoxOffsetY int
	// ContextCrop 是以人脸框为中心、四周各扩展 100% 的上下文裁剪（超出边界裁切）的 Base64 JPEG。
	ContextCropBase64 string
	ContextCropWidthPx  int
	ContextCropHeightPx int
}

// contextExpandRatio 上下文扩展比例：四周各扩展人脸框宽/高的 100%。
const contextExpandRatio = 1.0

// PrepareV2FaceCrops 从原图生成 v2 独立复核所需的裁剪。
// filePath 为原图路径，manualRotation 为照片手动旋转角度；
// bboxX/Y/Width/Height 为归一化坐标，基准是「EXIF 校正 + manual_rotation」的方向一致原图。
//
// 严禁调用 ProcessForAI(1024, 85)：v2 必须在未缩放原图上计算尺寸与质量。
func PrepareV2FaceCrops(filePath string, manualRotation int, bboxX, bboxY, bboxWidth, bboxHeight float64) (*V2FaceCrops, error) {
	if !isValidNormalizedBBox(bboxX, bboxY, bboxWidth, bboxHeight) {
		return nil, fmt.Errorf("invalid normalized bbox: x=%g y=%g w=%g h=%g", bboxX, bboxY, bboxWidth, bboxHeight)
	}

	img, w, h, err := util.OrientImageForVerification(filePath, manualRotation)
	if err != nil {
		return nil, fmt.Errorf("orient image for verification: %w", err)
	}

	// 人脸框在原图中的实际像素矩形（归一化坐标 × 原图宽高）。
	faceRect := normalizedToPixelRect(w, h, bboxX, bboxY, bboxWidth, bboxHeight)
	faceW := faceRect.Dx()
	faceH := faceRect.Dy()
	if faceW <= 0 || faceH <= 0 {
		return nil, fmt.Errorf("face box zero size after scaling: w=%d h=%d", faceW, faceH)
	}

	// 上下文裁剪：以人脸框为中心，四周各扩展 100%（按人脸框宽高）。
	ctxRect := expandContextRect(w, h, faceRect, contextExpandRatio)

	cropImg := imaging.Crop(img, ctxRect)

	b64, err := encodeJPEGBase64(cropImg)
	if err != nil {
		return nil, fmt.Errorf("encode context crop: %w", err)
	}

	// 人脸框左上角在上下文裁剪中的偏移（faceRect.Min - ctxRect.Min）。
	// ctxRect 已裁切到原图边界，故偏移可能因边界裁切而小于扩展量（非负）。
	offsetX := faceRect.Min.X - ctxRect.Min.X
	offsetY := faceRect.Min.Y - ctxRect.Min.Y
	if offsetX < 0 {
		offsetX = 0
	}
	if offsetY < 0 {
		offsetY = 0
	}

	return &V2FaceCrops{
		OriginalWidth:       w,
		OriginalHeight:      h,
		FaceBoxWidthPx:      faceW,
		FaceBoxHeightPx:     faceH,
		FaceBoxOffsetX:      offsetX,
		FaceBoxOffsetY:      offsetY,
		ContextCropBase64:   b64,
		ContextCropWidthPx:  ctxRect.Dx(),
		ContextCropHeightPx: ctxRect.Dy(),
	}, nil
}

// normalizedToPixelRect 把归一化 BBox 转为原图像素矩形。
func normalizedToPixelRect(width, height int, bboxX, bboxY, bboxWidth, bboxHeight float64) image.Rectangle {
	minX := int(math.Floor(bboxX * float64(width)))
	minY := int(math.Floor(bboxY * float64(height)))
	maxX := int(math.Ceil((bboxX + bboxWidth) * float64(width)))
	maxY := int(math.Ceil((bboxY + bboxHeight) * float64(height)))
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX > width {
		maxX = width
	}
	if maxY > height {
		maxY = height
	}
	if maxX <= minX {
		maxX = min(width, minX+1)
	}
	if maxY <= minY {
		maxY = min(height, minY+1)
	}
	return image.Rect(minX, minY, maxX, maxY)
}

// expandContextRect 以 faceRect 为中心，四周各扩展 ratio 倍人脸框宽/高，裁切到原图边界。
func expandContextRect(imgW, imgH int, faceRect image.Rectangle, ratio float64) image.Rectangle {
	fw := faceRect.Dx()
	fh := faceRect.Dy()
	padX := int(math.Round(float64(fw) * ratio))
	padY := int(math.Round(float64(fh) * ratio))

	left := faceRect.Min.X - padX
	top := faceRect.Min.Y - padY
	right := faceRect.Max.X + padX
	bottom := faceRect.Max.Y + padY

	if left < 0 {
		left = 0
	}
	if top < 0 {
		top = 0
	}
	if right > imgW {
		right = imgW
	}
	if bottom > imgH {
		bottom = imgH
	}
	if right <= left {
		right = min(imgW, left+1)
	}
	if bottom <= top {
		bottom = min(imgH, top+1)
	}
	return image.Rect(left, top, right, bottom)
}

// cropSubImage 已被 imaging.Crop 直接替代（裁剪矩形已在原图边界内）。

// encodeJPEGBase64 把图像编码为 JPEG 并转 Base64。质量 95 以保留质量细节供验证器判断。
func encodeJPEGBase64(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
