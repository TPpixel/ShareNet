package winui

import (
	"fmt"
	"math"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"com.tppixel/sharenetusb/internal/netutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	modDwmapi                = windows.NewLazySystemDLL("dwmapi.dll")
	modComdlg32              = windows.NewLazySystemDLL("comdlg32.dll")
	modUser32                = windows.NewLazySystemDLL("user32.dll")
	modGdi32                 = windows.NewLazySystemDLL("gdi32.dll")
	modKernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGetModuleHandleW     = modKernel32.NewProc("GetModuleHandleW")
	procDwmSetWindowAttr     = modDwmapi.NewProc("DwmSetWindowAttribute")
	procGetOpenFileNameW     = modComdlg32.NewProc("GetOpenFileNameW")
	procSetProcessDPIAware   = modUser32.NewProc("SetProcessDPIAware")
	procGetDpiForWindow      = modUser32.NewProc("GetDpiForWindow")
	procGetDC                = modUser32.NewProc("GetDC")
	procReleaseDC            = modUser32.NewProc("ReleaseDC")
	procGetDeviceCaps        = modGdi32.NewProc("GetDeviceCaps")
	procGetWindowRect        = modUser32.NewProc("GetWindowRect")
	procSetWindowPos         = modUser32.NewProc("SetWindowPos")
	procSystemParametersInfo = modUser32.NewProc("SystemParametersInfoW")
	procLoadImageW           = modUser32.NewProc("LoadImageW")
	procSendMessageW         = modUser32.NewProc("SendMessageW")
)

const (
	dwmwaUseImmersiveDarkMode = 20
	dwmwaCaptionColor         = 35
	ofnExplorer               = 0x00080000
	ofnFileMustExist          = 0x00001000
	ofnPathMustExist          = 0x00000800
	ofnNoChangeDir            = 0x00000008
	logPixelsX                = 88
	spiGetWorkArea            = 0x0030
	swpNoSize                 = 0x0001
	swpNoZOrder               = 0x0004
	swpNoActivate             = 0x0010
	imageIcon                 = 1
	lrDefaultColor            = 0x0000
	lrShared                  = 0x8000
	wmSetIcon                 = 0x0080
	iconSmall                 = 0
	iconBig                   = 1
)

type ThemePalette struct {
	Dark           bool   `json:"dark"`
	Accent         string `json:"accent"`
	OnAccent       string `json:"onAccent"`
	AccentHover    string `json:"accentHover"`
	AccentText     string `json:"accentText"`
	AccentSoft     string `json:"accentSoft"`
	AccentSoftText string `json:"accentSoftText"`
	HasCustomColor bool   `json:"hasCustomColor"`
}

type rgb struct {
	r, g, b uint8
}

type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

var _ [152]byte = [unsafe.Sizeof(openFileNameW{})]byte{}

type winRect struct {
	Left, Top, Right, Bottom int32
}

func EnableDPIAware() {
	_, _, _ = procSetProcessDPIAware.Call()
}

// GetWindowDPI returns the effective DPI for hwnd (96 = 100%, 144 = 150%, etc.).
func GetWindowDPI(hwnd uintptr) uint32 {
	if hwnd != 0 && procGetDpiForWindow.Find() == nil {
		if ret, _, _ := procGetDpiForWindow.Call(hwnd); ret >= 96 {
			return uint32(ret)
		}
	}
	dc, _, _ := procGetDC.Call(0)
	if dc != 0 {
		dpi, _, _ := procGetDeviceCaps.Call(dc, uintptr(logPixelsX))
		_, _, _ = procReleaseDC.Call(0, dc)
		if dpi >= 96 {
			return uint32(dpi)
		}
	}
	return 96
}

// ScaleForDPI converts logical 96-DPI pixels into physical pixels for the given DPI.
func ScaleForDPI(logical int, dpi uint32) int {
	if dpi == 0 {
		dpi = 96
	}
	return int(math.Round(float64(logical) * float64(dpi) / 96.0))
}

// CenterWindowInWorkArea centers hwnd within the primary desktop work area.
func CenterWindowInWorkArea(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	var wr, wa winRect
	r1, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
	r2, _, _ := procSystemParametersInfo.Call(uintptr(spiGetWorkArea), 0, uintptr(unsafe.Pointer(&wa)), 0)
	if r1 == 0 || r2 == 0 {
		return
	}
	winW := wr.Right - wr.Left
	winH := wr.Bottom - wr.Top
	workW := wa.Right - wa.Left
	workH := wa.Bottom - wa.Top
	x := wa.Left + (workW-winW)/2
	y := wa.Top + (workH-winH)/2
	if x < wa.Left {
		x = wa.Left
	}
	if y < wa.Top {
		y = wa.Top
	}
	_, _, _ = procSetWindowPos.Call(
		hwnd, 0, uintptr(x), uintptr(y), 0, 0,
		uintptr(swpNoSize|swpNoZOrder|swpNoActivate),
	)
}

// SetWindowAppIcon binds the embedded PE application icon resource (#1) to hwnd's titlebar and taskbar.
func SetWindowAppIcon(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	hInst, _, _ := procGetModuleHandleW.Call(0)
	if hInst == 0 {
		return
	}
	bigSz := uintptr(ScaleForDPI(32, GetWindowDPI(hwnd)))
	smallSz := uintptr(ScaleForDPI(16, GetWindowDPI(hwnd)))
	hBig, _, _ := procLoadImageW.Call(hInst, uintptr(1), uintptr(imageIcon), bigSz, bigSz, uintptr(lrDefaultColor|lrShared))
	if hBig != 0 {
		_, _, _ = procSendMessageW.Call(hwnd, uintptr(wmSetIcon), uintptr(iconBig), hBig)
	}
	hSmall, _, _ := procLoadImageW.Call(hInst, uintptr(1), uintptr(imageIcon), smallSz, smallSz, uintptr(lrDefaultColor|lrShared))
	if hSmall != 0 {
		_, _, _ = procSendMessageW.Call(hwnd, uintptr(wmSetIcon), uintptr(iconSmall), hSmall)
	}
}

func SetDarkTitleBar(hwnd uintptr, dark bool) {
	if hwnd == 0 {
		return
	}
	var val int32
	if dark {
		val = 1
	}
	_, _, _ = procDwmSetWindowAttr.Call(
		hwnd,
		uintptr(dwmwaUseImmersiveDarkMode),
		uintptr(unsafe.Pointer(&val)),
		unsafe.Sizeof(val),
	)
	// Blend caption with Windows 11 system canvas (#202020 dark / #F3F3F3 light); ignored on Win10.
	var colorRef uint32 = 0x00F3F3F3
	if dark {
		colorRef = 0x00202020
	}
	_, _, _ = procDwmSetWindowAttr.Call(
		hwnd,
		uintptr(dwmwaCaptionColor),
		uintptr(unsafe.Pointer(&colorRef)),
		unsafe.Sizeof(colorRef),
	)
}

func PickFile(title string) (string, bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	buf := make([]uint16, 4096)
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return "", false, err
	}
	filter := utf16DoubleNull(
		"All Files (*.*)", "*.*",
		"Images", "*.jpg;*.jpeg;*.png;*.webp;*.jfif;*.gif;*.bmp",
		"Android APK (*.apk)", "*.apk",
	)
	ofn := openFileNameW{
		lStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		lpstrFilter: &filter[0],
		lpstrFile:   &buf[0],
		nMaxFile:    uint32(len(buf)),
		lpstrTitle:  titlePtr,
		flags:       ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnNoChangeDir,
	}
	ret, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret == 0 {
		return "", false, nil
	}
	return windows.UTF16ToString(buf), true, nil
}

func utf16DoubleNull(pairs ...string) []uint16 {
	var out []uint16
	for _, s := range pairs {
		u, _ := syscall.UTF16FromString(s)
		out = append(out, u...)
	}
	return append(out, 0)
}

func OpenInteractiveAdbTerminal(env []string) error {
	cmd := exec.Command("cmd.exe", "/k", "adb version && adb devices -l")
	cmd.Env = env
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	return cmd.Start()
}

func OpenAppFallback(url string) error {
	for _, p := range []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	} {
		cmd := exec.Command(p, "--app="+url, "--window-size=360,620")
		netutil.HideWindow(cmd)
		if err := cmd.Start(); err == nil {
			return cmd.Wait()
		}
	}
	return netutil.OpenURL(url)
}

func IsWindowsDarkTheme() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return true
	}
	return v == 0
}

// GetWindowsTheme reads Windows 11 Light/Dark preference and Personalization AccentColor/AccentPalette,
// returning a WCAG AA accessible ThemePalette (or Windows default blue if no custom color exists).
func GetWindowsTheme() ThemePalette {
	return GetWindowsThemeWithDark(IsWindowsDarkTheme())
}

// GetWindowsThemeWithDark builds the Windows Personalization ThemePalette for an explicit dark/light mode.
func GetWindowsThemeWithDark(dark bool) ThemePalette {
	palette, base, hasCustom := readWindowsAccentRegistry()
	return BuildThemePalette(dark, palette, base, hasCustom)
}

func readWindowsAccentRegistry() ([]rgb, rgb, bool) {
	var palette []rgb
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\Accent`, registry.QUERY_VALUE); err == nil {
		defer k.Close()
		if raw, _, err := k.GetBinaryValue("AccentPalette"); err == nil && len(raw) >= 28 {
			for i := 0; i+3 < len(raw) && len(palette) < 8; i += 4 {
				palette = append(palette, rgb{r: raw[i], g: raw[i+1], b: raw[i+2]})
			}
		}
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\DWM`, registry.QUERY_VALUE); err == nil {
		defer k.Close()
		if v, _, err := k.GetIntegerValue("AccentColor"); err == nil && v != 0 {
			return palette, decodeABGR(uint32(v)), true
		}
	}
	if len(palette) >= 4 {
		return palette, palette[3], true
	}
	return nil, rgb{}, false
}

func decodeABGR(v uint32) rgb {
	return rgb{
		r: uint8(v & 0xFF),
		g: uint8((v >> 8) & 0xFF),
		b: uint8((v >> 16) & 0xFF),
	}
}

func (c rgb) hex() string {
	return fmt.Sprintf("#%02X%02X%02X", c.r, c.g, c.b)
}

func (c rgb) rgba(alpha float64) string {
	return fmt.Sprintf("rgba(%d, %d, %d, %.2f)", c.r, c.g, c.b, alpha)
}

func srgbChannel(u uint8) float64 {
	v := float64(u) / 255.0
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func relativeLuminance(c rgb) float64 {
	return 0.2126*srgbChannel(c.r) + 0.7152*srgbChannel(c.g) + 0.0722*srgbChannel(c.b)
}

func contrastRatio(a, b rgb) float64 {
	la := relativeLuminance(a)
	lb := relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func mixRGB(a, b rgb, t float64) rgb {
	lerp := func(x, y uint8) uint8 {
		v := math.Round(float64(x)*(1-t) + float64(y)*t)
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	return rgb{r: lerp(a.r, b.r), g: lerp(a.g, b.g), b: lerp(a.b, b.b)}
}

func BuildThemePalette(dark bool, palette []rgb, base rgb, hasCustom bool) ThemePalette {
	if !hasCustom {
		if dark {
			return ThemePalette{
				Dark:           true,
				Accent:         "#60CDFF",
				OnAccent:       "#001E2E",
				AccentHover:    "#54BBEB",
				AccentText:     "#60CDFF",
				AccentSoft:     "rgba(96, 205, 255, 0.14)",
				AccentSoftText: "#8CE0FF",
				HasCustomColor: false,
			}
		}
		return ThemePalette{
			Dark:           false,
			Accent:         "#0067C0",
			OnAccent:       "#FFFFFF",
			AccentHover:    "#005BA8",
			AccentText:     "#005FB8",
			AccentSoft:     "rgba(0, 103, 192, 0.10)",
			AccentSoftText: "#00529B",
			HasCustomColor: false,
		}
	}

	surfaceBg := rgb{243, 243, 243}
	if dark {
		surfaceBg = rgb{43, 43, 43}
	}

	var fill, hover, textAccent rgb
	if dark {
		// Windows 11 dark mode uses Light2 (index 1) or Light1 (index 2) from AccentPalette when present.
		switch {
		case len(palette) >= 3:
			fill = palette[2]
			hover = palette[1]
		case len(palette) >= 2:
			fill = palette[1]
			hover = mixRGB(palette[1], rgb{255, 255, 255}, 0.12)
		default:
			fill = mixRGB(base, rgb{255, 255, 255}, 0.20)
			hover = mixRGB(base, rgb{255, 255, 255}, 0.30)
		}
		textAccent = fill
		for i := 0; i < 10 && contrastRatio(textAccent, surfaceBg) < 4.5; i++ {
			textAccent = mixRGB(textAccent, rgb{255, 255, 255}, 0.15)
		}
	} else {
		// Windows 11 light mode uses Dark1 (index 4) or base when it meets 4.5:1 against white text.
		fill = base
		if len(palette) >= 5 && contrastRatio(fill, rgb{255, 255, 255}) < 4.5 {
			fill = palette[4]
		}
		for i := 0; i < 10 && contrastRatio(fill, rgb{255, 255, 255}) < 4.5 && contrastRatio(fill, rgb{17, 17, 17}) < 4.5; i++ {
			fill = mixRGB(fill, rgb{0, 0, 0}, 0.12)
		}
		hover = mixRGB(fill, rgb{0, 0, 0}, 0.10)
		textAccent = fill
		for i := 0; i < 10 && contrastRatio(textAccent, surfaceBg) < 4.5; i++ {
			textAccent = mixRGB(textAccent, rgb{0, 0, 0}, 0.12)
		}
	}

	darkInk := rgb{17, 17, 17}
	whiteInk := rgb{255, 255, 255}
	onAccent := whiteInk
	if contrastRatio(fill, darkInk) > contrastRatio(fill, whiteInk) {
		onAccent = darkInk
	}
	// Nudge fill lightness only if neither black nor white reaches 4.5:1.
	for i := 0; i < 10 && contrastRatio(fill, onAccent) < 4.5; i++ {
		if onAccent == darkInk {
			fill = mixRGB(fill, whiteInk, 0.12)
		} else {
			fill = mixRGB(fill, rgb{0, 0, 0}, 0.12)
		}
	}

	softAlpha := 0.11
	if dark {
		softAlpha = 0.16
	}
	return ThemePalette{
		Dark:           dark,
		Accent:         fill.hex(),
		OnAccent:       onAccent.hex(),
		AccentHover:    hover.hex(),
		AccentText:     textAccent.hex(),
		AccentSoft:     base.rgba(softAlpha),
		AccentSoftText: textAccent.hex(),
		HasCustomColor: true,
	}
}
