package winui

import (
	"fmt"
	"testing"
)

func parseHexRGB(t *testing.T, hex string) rgb {
	t.Helper()
	var r, g, b uint8
	if _, err := fmt.Sscanf(hex, "#%02X%02X%02X", &r, &g, &b); err != nil {
		t.Fatalf("invalid hex color %q: %v", hex, err)
	}
	return rgb{r: r, g: g, b: b}
}

func TestScaleForDPI(t *testing.T) {
	if got := ScaleForDPI(450, 96); got != 450 {
		t.Errorf("ScaleForDPI(450, 96) = %d, want 450", got)
	}
	if got := ScaleForDPI(450, 144); got != 675 {
		t.Errorf("ScaleForDPI(450, 144) = %d, want 675", got)
	}
	if got := ScaleForDPI(620, 144); got != 930 {
		t.Errorf("ScaleForDPI(620, 144) = %d, want 930", got)
	}
}

func TestDecodeABGR(t *testing.T) {
	got := decodeABGR(0xffd88c8e)
	want := rgb{r: 0x8E, g: 0x8C, b: 0xD8}
	if got != want {
		t.Fatalf("decodeABGR(0xffd88c8e) = %+v, want %+v", got, want)
	}
}

func TestBuildThemePaletteContrast(t *testing.T) {
	userPalette := []rgb{
		{0xE8, 0xE3, 0xF8},
		{0xC5, 0xC1, 0xEC},
		{0x9F, 0x9C, 0xDE},
		{0x8E, 0x8C, 0xD8},
		{0x6D, 0x6B, 0xCD},
		{0x36, 0x34, 0x9B},
		{0x1A, 0x19, 0x56},
		{0x87, 0x64, 0xB8},
	}

	cases := []struct {
		name      string
		dark      bool
		palette   []rgb
		base      rgb
		hasCustom bool
		surface   rgb
	}{
		{
			name:      "default dark",
			dark:      true,
			hasCustom: false,
			surface:   rgb{0x2B, 0x2B, 0x2B},
		},
		{
			name:      "default light",
			dark:      false,
			hasCustom: false,
			surface:   rgb{0xF3, 0xF3, 0xF3},
		},
		{
			name:      "user lavender dark",
			dark:      true,
			palette:   userPalette,
			base:      rgb{0x8E, 0x8C, 0xD8},
			hasCustom: true,
			surface:   rgb{0x2B, 0x2B, 0x2B},
		},
		{
			name:      "user lavender light",
			dark:      false,
			palette:   userPalette,
			base:      rgb{0x8E, 0x8C, 0xD8},
			hasCustom: true,
			surface:   rgb{0xF3, 0xF3, 0xF3},
		},
		{
			name:      "very light accent dark",
			dark:      true,
			base:      rgb{0xF5, 0xF5, 0xDC},
			hasCustom: true,
			surface:   rgb{0x2B, 0x2B, 0x2B},
		},
		{
			name:      "very dark accent light",
			dark:      false,
			base:      rgb{0x10, 0x14, 0x20},
			hasCustom: true,
			surface:   rgb{0xF3, 0xF3, 0xF3},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := BuildThemePalette(tc.dark, tc.palette, tc.base, tc.hasCustom)
			accent := parseHexRGB(t, p.Accent)
			onAccent := parseHexRGB(t, p.OnAccent)
			accentText := parseHexRGB(t, p.AccentText)

			if cr := contrastRatio(accent, onAccent); cr < 4.5 {
				t.Errorf("Accent (%s) vs OnAccent (%s) contrast = %.2f < 4.5", p.Accent, p.OnAccent, cr)
			}
			if cr := contrastRatio(accentText, tc.surface); cr < 4.5 {
				t.Errorf("AccentText (%s) vs surface (%s) contrast = %.2f < 4.5", p.AccentText, tc.surface.hex(), cr)
			}
		})
	}
}
