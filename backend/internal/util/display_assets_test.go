package util

import (
	"image"
	"image/color"
	"testing"
)

func TestQuantizeToPalettePreservesInfoAreaWithoutDithering(t *testing.T) {
	profile := RenderProfile{
		Width:      4,
		Height:     10,
		DitherMode: "ordered",
		Palette: []color.NRGBA{
			{R: 255, G: 255, B: 255, A: 255},
			{R: 0, G: 0, B: 0, A: 255},
			{R: 196, G: 44, B: 29, A: 255},
			{R: 233, G: 188, B: 41, A: 255},
		},
	}

	img := image.NewNRGBA(image.Rect(0, 0, profile.Width, profile.Height))
	for y := 0; y < profile.Height; y++ {
		for x := 0; x < profile.Width; x++ {
			if y < displayPhotoHeightForProfile(profile.Height) {
				img.SetNRGBA(x, y, color.NRGBA{R: 128, G: 128, B: 128, A: 255})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{R: 120, G: 120, B: 120, A: 255})
			}
		}
	}

	indexed := quantizeToPalette(img, profile)
	photoHeight := displayPhotoHeightForProfile(profile.Height)
	whiteIndex := uint8(nearestPaletteIndex(color.NRGBA{R: 255, G: 255, B: 255, A: 255}, profile.Palette))
	blackIndex := uint8(nearestPaletteIndex(color.NRGBA{R: 0, G: 0, B: 0, A: 255}, profile.Palette))
	for y := photoHeight; y < profile.Height; y++ {
		first := indexed[y*profile.Width]
		for x := 1; x < profile.Width; x++ {
			if indexed[y*profile.Width+x] != first {
				t.Fatalf("expected info area row %d to avoid dithering, got mixed indexes %v", y, indexed[y*profile.Width:(y+1)*profile.Width])
			}
		}
		if first != whiteIndex && first != blackIndex {
			t.Fatalf("expected info area row %d to use black/white only, got index %d", y, first)
		}
	}
}

func TestQuantizeInfoRegionBlackWhiteMapsGrayTextToBlack(t *testing.T) {
	profile := RenderProfile{
		Width:      2,
		Height:     10,
		DitherMode: "floyd_steinberg",
		Palette: []color.NRGBA{
			{R: 255, G: 255, B: 255, A: 255},
			{R: 0, G: 0, B: 0, A: 255},
			{R: 196, G: 44, B: 29, A: 255},
			{R: 233, G: 188, B: 41, A: 255},
		},
	}

	img := image.NewNRGBA(image.Rect(0, 0, profile.Width, profile.Height))
	photoHeight := displayPhotoHeightForProfile(profile.Height)
	for y := 0; y < profile.Height; y++ {
		for x := 0; x < profile.Width; x++ {
			if y < photoHeight {
				img.SetNRGBA(x, y, color.NRGBA{R: 128, G: 128, B: 128, A: 255})
				continue
			}
			if x == 0 {
				img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{R: 120, G: 120, B: 120, A: 255})
			}
		}
	}

	indexed := quantizeToPalette(img, profile)
	whiteIndex := uint8(nearestPaletteIndex(color.NRGBA{R: 255, G: 255, B: 255, A: 255}, profile.Palette))
	blackIndex := uint8(nearestPaletteIndex(color.NRGBA{R: 0, G: 0, B: 0, A: 255}, profile.Palette))
	for y := photoHeight; y < profile.Height; y++ {
		row := indexed[y*profile.Width : (y+1)*profile.Width]
		if row[0] != whiteIndex {
			t.Fatalf("expected white background at row %d, got index %d", y, row[0])
		}
		if row[1] != blackIndex {
			t.Fatalf("expected gray text to map to black at row %d, got index %d", y, row[1])
		}
	}
}
