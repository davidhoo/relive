package util

import (
	"image"
	"image/color"
	"testing"
)

func TestQuantizeToPalettePreservesInfoAreaWithoutDithering(t *testing.T) {
	profile := RenderProfile{
		Width:      10,
		Height:     4,
		DitherMode: "ordered",
		Palette: []color.NRGBA{
			{R: 255, G: 255, B: 255, A: 255},
			{R: 0, G: 0, B: 0, A: 255},
			{R: 196, G: 44, B: 29, A: 255},
			{R: 233, G: 188, B: 41, A: 255},
		},
	}

	img := image.NewNRGBA(image.Rect(0, 0, profile.Width, profile.Height))
	photoWidth := displayPhotoWidthForProfile(profile.Width)
	for y := 0; y < profile.Height; y++ {
		for x := 0; x < profile.Width; x++ {
			if x < photoWidth {
				img.SetNRGBA(x, y, color.NRGBA{R: 128, G: 128, B: 128, A: 255})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{R: 120, G: 120, B: 120, A: 255})
			}
		}
	}

	indexed := quantizeToPalette(img, profile)
	whiteIndex := uint8(nearestPaletteIndex(color.NRGBA{R: 255, G: 255, B: 255, A: 255}, profile.Palette))
	blackIndex := uint8(nearestPaletteIndex(color.NRGBA{R: 0, G: 0, B: 0, A: 255}, profile.Palette))

	// 检查信息区域（右侧）是否避免了抖动
	for y := 0; y < profile.Height; y++ {
		first := indexed[y*profile.Width+photoWidth]
		for x := photoWidth + 1; x < profile.Width; x++ {
			if indexed[y*profile.Width+x] != first {
				t.Fatalf("expected info area row %d to avoid dithering, got mixed indexes", y)
			}
		}
		if first != whiteIndex && first != blackIndex {
			t.Fatalf("expected info area row %d to use black/white only, got index %d", y, first)
		}
	}
}

func TestQuantizeInfoRegionBlackWhiteMapsGrayTextToBlack(t *testing.T) {
	profile := RenderProfile{
		Width:      10,
		Height:     2,
		DitherMode: "floyd_steinberg",
		Palette: []color.NRGBA{
			{R: 255, G: 255, B: 255, A: 255},
			{R: 0, G: 0, B: 0, A: 255},
			{R: 196, G: 44, B: 29, A: 255},
			{R: 233, G: 188, B: 41, A: 255},
		},
	}

	img := image.NewNRGBA(image.Rect(0, 0, profile.Width, profile.Height))
	photoWidth := displayPhotoWidthForProfile(profile.Width)
	for y := 0; y < profile.Height; y++ {
		for x := 0; x < profile.Width; x++ {
			if x < photoWidth {
				img.SetNRGBA(x, y, color.NRGBA{R: 128, G: 128, B: 128, A: 255})
				continue
			}
			if y == 0 {
				img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{R: 120, G: 120, B: 120, A: 255})
			}
		}
	}

	indexed := quantizeToPalette(img, profile)
	whiteIndex := uint8(nearestPaletteIndex(color.NRGBA{R: 255, G: 255, B: 255, A: 255}, profile.Palette))
	blackIndex := uint8(nearestPaletteIndex(color.NRGBA{R: 0, G: 0, B: 0, A: 255}, profile.Palette))

	// 检查信息区域（右侧）
	for x := photoWidth; x < profile.Width; x++ {
		if indexed[0*profile.Width+x] != whiteIndex {
			t.Fatalf("expected white background at column %d, got index %d", x, indexed[0*profile.Width+x])
		}
		if indexed[1*profile.Width+x] != blackIndex {
			t.Fatalf("expected gray text to map to black at column %d, got index %d", x, indexed[1*profile.Width+x])
		}
	}
}
