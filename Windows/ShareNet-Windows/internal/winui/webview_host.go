package winui

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

var (
	wvUser32   = windows.NewLazySystemDLL("user32.dll")
	wvGdi32    = windows.NewLazySystemDLL("gdi32.dll")
	wvKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	wvDwmapi   = windows.NewLazySystemDLL("dwmapi.dll")

	wvGetModuleHandleW      = wvKernel32.NewProc("GetModuleHandleW")
	wvRegisterClassExW      = wvUser32.NewProc("RegisterClassExW")
	wvCreateWindowExW       = wvUser32.NewProc("CreateWindowExW")
	wvDestroyWindow         = wvUser32.NewProc("DestroyWindow")
	wvShowWindow            = wvUser32.NewProc("ShowWindow")
	wvUpdateWindow          = wvUser32.NewProc("UpdateWindow")
	wvSetForegroundWindow   = wvUser32.NewProc("SetForegroundWindow")
	wvDefWindowProcW        = wvUser32.NewProc("DefWindowProcW")
	wvGetMessageW           = wvUser32.NewProc("GetMessageW")
	wvTranslateMessage      = wvUser32.NewProc("TranslateMessage")
	wvDispatchMessageW      = wvUser32.NewProc("DispatchMessageW")
	wvPostQuitMessage       = wvUser32.NewProc("PostQuitMessage")
	wvPostMessageW          = wvUser32.NewProc("PostMessageW")
	wvAdjustWindowRectEx    = wvUser32.NewProc("AdjustWindowRectEx")
	wvSetWindowPos          = wvUser32.NewProc("SetWindowPos")
	wvLoadCursorW           = wvUser32.NewProc("LoadCursorW")
	wvSetTimer              = wvUser32.NewProc("SetTimer")
	wvKillTimer             = wvUser32.NewProc("KillTimer")
	wvGetAncestor           = wvUser32.NewProc("GetAncestor")
	wvIsDialogMessageW      = wvUser32.NewProc("IsDialogMessageW")
	wvCreateSolidBrush      = wvGdi32.NewProc("CreateSolidBrush")
	wvDeleteObject          = wvGdi32.NewProc("DeleteObject")
	wvDwmSetWindowAttribute = wvDwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	wvWSOverlappedWindow = 0x00CF0000
	wvWSClipChildren     = 0x02000000
	wvSWShow             = 5
	wvIDCArrow           = 32512
	wvGARoot             = 2
	wvDWMWACloak         = 13

	wvWMCreate        = 0x0001
	wvWMDestroy       = 0x0002
	wvWMMove          = 0x0003
	wvWMSize          = 0x0005
	wvWMActivate      = 0x0006
	wvWMClose         = 0x0010
	wvWMGetMinMaxInfo = 0x0024
	wvWMTimer         = 0x0113
	wvWMMoving        = 0x0216
	wvWMApp           = 0x8000

	wvSWPNoZOrder     = 0x0004
	wvSWPNoActivate   = 0x0010
	wvSWPNoMove       = 0x0002
	wvSWPFrameChanged = 0x0020

	wvTimerPostNavReveal = 1
	wvTimerSafetyReveal  = 2
)

type wvWndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type wvPoint struct {
	x, y int32
}

type wvMinMaxInfo struct {
	ptReserved     wvPoint
	ptMaxSize      wvPoint
	ptMaxPosition  wvPoint
	ptMinTrackSize wvPoint
	ptMaxTrackSize wvPoint
}

type wvMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      wvPoint
}

// WebViewOptions configures the hidden-until-ready Win32 + WebView2 host window.
type WebViewOptions struct {
	Title    string
	DataPath string
	URL      string
	Width    int
	Height   int
	MinWidth int
	MinHeight int
	Dark     bool
}

// WebViewWindow hosts an edge.Chromium instance in a Win32 window that stays
// hidden with a theme-matched background until initial sizing, centering,
// dark titlebar, and first page load complete.
type WebViewWindow struct {
	hwnd      uintptr
	browser   *edge.Chromium
	bgBrush   uintptr
	minW      int32
	minH      int32
	shown     bool
	mu        sync.Mutex
	dispatchq []func()
}

var (
	wvInstancesMu sync.RWMutex
	wvInstances   = map[uintptr]*WebViewWindow{}
	wvWndProcPtr  = syscall.NewCallback(wvWndProc)
)

// NewWebViewWindow creates a hidden, DPI-scaled, centered Win32 window with
// theme-matched background and embeds WebView2. Returns nil if WebView2
// Runtime is unavailable so the caller can fall back to the default browser.
func NewWebViewWindow(opts WebViewOptions) *WebViewWindow {
	runtime.LockOSThread()

	hInst, _, _ := wvGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString("ShareNetWebViewHost")
	titlePtr, _ := windows.UTF16PtrFromString(opts.Title)

	bgColorRef := uintptr(0x00F3F3F3) // COLORREF 0x00BBGGRR: #F3F3F3
	wvBgColor := edge.COREWEBVIEW2_COLOR{A: 255, R: 0xF3, G: 0xF3, B: 0xF3}
	if opts.Dark {
		bgColorRef = 0x00202020 // #202020
		wvBgColor = edge.COREWEBVIEW2_COLOR{A: 255, R: 0x20, G: 0x20, B: 0x20}
	}
	bgBrush, _, _ := wvCreateSolidBrush.Call(bgColorRef)
	cursor, _, _ := wvLoadCursorW.Call(0, wvIDCArrow)

	wc := wvWndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wvWndClassExW{})),
		lpfnWndProc:   wvWndProcPtr,
		hInstance:     hInst,
		hCursor:       cursor,
		hbrBackground: bgBrush,
		lpszClassName: className,
	}
	_, _, _ = wvRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	// Create with WS_CLIPCHILDREN so parent never paints over WebView2,
	// without WS_VISIBLE so sizing, centering, icon, and DWM frame are
	// finalized before DWM reveals the window.
	style := uintptr(wvWSOverlappedWindow | wvWSClipChildren)
	hwnd, _, _ := wvCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(titlePtr)),
		style,
		0, 0, 640, 480,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		if bgBrush != 0 {
			_, _, _ = wvDeleteObject.Call(bgBrush)
		}
		return nil
	}

	dpi := GetWindowDPI(hwnd)
	physW := ScaleForDPI(opts.Width, dpi)
	physH := ScaleForDPI(opts.Height, dpi)
	minClientW := ScaleForDPI(opts.MinWidth, dpi)
	minClientH := ScaleForDPI(opts.MinHeight, dpi)

	minRect := winRect{Left: 0, Top: 0, Right: int32(minClientW), Bottom: int32(minClientH)}
	_, _, _ = wvAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&minRect)), wvWSOverlappedWindow, 0, 0)

	w := &WebViewWindow{
		hwnd:    hwnd,
		bgBrush: bgBrush,
		minW:    minRect.Right - minRect.Left,
		minH:    minRect.Bottom - minRect.Top,
	}

	wvInstancesMu.Lock()
	wvInstances[hwnd] = w
	wvInstancesMu.Unlock()

	SetWindowAppIcon(hwnd)
	SetDarkTitleBar(hwnd, opts.Dark)

	winR := winRect{Left: 0, Top: 0, Right: int32(physW), Bottom: int32(physH)}
	_, _, _ = wvAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&winR)), wvWSOverlappedWindow, 0, 0)
	_, _, _ = wvSetWindowPos.Call(
		hwnd, 0, 0, 0,
		uintptr(winR.Right-winR.Left),
		uintptr(winR.Bottom-winRectHeightOffset(&winR)),
		wvSWPNoZOrder|wvSWPNoActivate|wvSWPNoMove|wvSWPFrameChanged,
	)
	CenterWindowInWorkArea(hwnd)

	// Cloak via DWM before ShowWindow so WebView2 sees a visible parent and
	// renders its first frame normally while staying invisible on screen.
	var cloak uint32 = 1
	_, _, _ = wvDwmSetWindowAttribute.Call(hwnd, wvDWMWACloak, uintptr(unsafe.Pointer(&cloak)), unsafe.Sizeof(cloak))
	_, _, _ = wvShowWindow.Call(hwnd, wvSWShow)

	chromium := edge.NewChromium()
	chromium.DataPath = opts.DataPath
	chromium.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) {
		if !w.shown {
			// Give Chromium ~60ms to composite the first frame before uncloaking.
			_, _, _ = wvSetTimer.Call(hwnd, wvTimerPostNavReveal, 60, 0)
		}
	}

	if !chromium.Embed(hwnd) {
		w.Destroy()
		return nil
	}
	w.browser = chromium

	if settings, err := chromium.GetSettings(); err == nil && settings != nil {
		_ = settings.PutAreDevToolsEnabled(false)
		_ = settings.PutIsStatusBarEnabled(false)
	}
	if ctrl := chromium.GetController(); ctrl != nil {
		if ctrl2 := ctrl.GetICoreWebView2Controller2(); ctrl2 != nil {
			_ = ctrl2.PutDefaultBackgroundColor(wvBgColor)
		}
	}

	_ = chromium.Show()
	chromium.Resize()
	// Safety fallback in case NavigationCompleted is delayed.
	_, _, _ = wvSetTimer.Call(hwnd, wvTimerSafetyReveal, 800, 0)
	if opts.URL != "" {
		chromium.Navigate(opts.URL)
	}

	return w
}

func winRectHeightOffset(r *winRect) int32 {
	return r.Top
}

func (w *WebViewWindow) reveal() {
	if w.shown || w.hwnd == 0 {
		return
	}
	w.shown = true
	_, _, _ = wvKillTimer.Call(w.hwnd, wvTimerPostNavReveal)
	_, _, _ = wvKillTimer.Call(w.hwnd, wvTimerSafetyReveal)
	if w.browser != nil {
		_ = w.browser.Show()
		w.browser.Resize()
	}
	var cloak uint32 = 0
	_, _, _ = wvDwmSetWindowAttribute.Call(w.hwnd, wvDWMWACloak, uintptr(unsafe.Pointer(&cloak)), unsafe.Sizeof(cloak))
	_, _, _ = wvShowWindow.Call(w.hwnd, wvSWShow)
	_, _, _ = wvUpdateWindow.Call(w.hwnd)
	_, _, _ = wvSetForegroundWindow.Call(w.hwnd)
	if w.browser != nil {
		w.browser.Focus()
	}
}

func (w *WebViewWindow) drainDispatch() {
	w.mu.Lock()
	q := append([]func(){}, w.dispatchq...)
	w.dispatchq = w.dispatchq[:0]
	w.mu.Unlock()
	for _, fn := range q {
		fn()
	}
}

// Window returns the underlying HWND as an unsafe.Pointer.
func (w *WebViewWindow) Window() unsafe.Pointer {
	return unsafe.Pointer(w.hwnd)
}

// Dispatch runs fn on the UI thread owning the host window.
func (w *WebViewWindow) Dispatch(fn func()) {
	if w == nil || w.hwnd == 0 || fn == nil {
		return
	}
	w.mu.Lock()
	w.dispatchq = append(w.dispatchq, fn)
	w.mu.Unlock()
	_, _, _ = wvPostMessageW.Call(w.hwnd, wvWMApp, 0, 0)
}

// UpdateTheme updates the titlebar and WebView2 default background color.
func (w *WebViewWindow) UpdateTheme(dark bool) {
	if w == nil || w.hwnd == 0 {
		return
	}
	SetDarkTitleBar(w.hwnd, dark)
	if w.browser != nil {
		if ctrl := w.browser.GetController(); ctrl != nil {
			if ctrl2 := ctrl.GetICoreWebView2Controller2(); ctrl2 != nil {
				c := edge.COREWEBVIEW2_COLOR{A: 255, R: 0xF3, G: 0xF3, B: 0xF3}
				if dark {
					c = edge.COREWEBVIEW2_COLOR{A: 255, R: 0x20, G: 0x20, B: 0x20}
				}
				_ = ctrl2.PutDefaultBackgroundColor(c)
			}
		}
	}
}

// Run processes the Win32 message loop until the window closes.
func (w *WebViewWindow) Run() {
	var msg wvMsg
	for {
		ret, _, _ := wvGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			return
		}
		if msg.Message() == wvWMApp {
			w.drainDispatch()
			continue
		}
		root, _, _ := wvGetAncestor.Call(msg.hwnd, wvGARoot)
		isDlg, _, _ := wvIsDialogMessageW.Call(root, uintptr(unsafe.Pointer(&msg)))
		if isDlg != 0 {
			continue
		}
		_, _, _ = wvTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = wvDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (m *wvMsg) Message() uint32 {
	return m.message
}

// Destroy closes and cleans up the window and GDI brush.
func (w *WebViewWindow) Destroy() {
	if w == nil {
		return
	}
	if w.hwnd != 0 {
		_, _, _ = wvDestroyWindow.Call(w.hwnd)
	}
	if w.bgBrush != 0 {
		_, _, _ = wvDeleteObject.Call(w.bgBrush)
		w.bgBrush = 0
	}
}

func wvWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	wvInstancesMu.RLock()
	w := wvInstances[hwnd]
	wvInstancesMu.RUnlock()

	if w == nil {
		ret, _, _ := wvDefWindowProcW.Call(hwnd, msg, wParam, lParam)
		return ret
	}

	switch msg {
	case wvWMSize:
		if w.browser != nil {
			w.browser.Resize()
		}
		return 0
	case wvWMMove, wvWMMoving:
		if w.browser != nil {
			_ = w.browser.NotifyParentWindowPositionChanged()
		}
	case wvWMActivate:
		if wParam != 0 && w.browser != nil {
			w.browser.Focus()
		}
	case wvWMGetMinMaxInfo:
		if lParam != 0 && (w.minW > 0 || w.minH > 0) {
			mmi := (*wvMinMaxInfo)(unsafe.Pointer(lParam))
			mmi.ptMinTrackSize.x = w.minW
			mmi.ptMinTrackSize.y = w.minH
		}
		return 0
	case wvWMTimer:
		if wParam == wvTimerPostNavReveal || wParam == wvTimerSafetyReveal {
			w.reveal()
			return 0
		}
	case wvWMApp:
		w.drainDispatch()
		return 0
	case wvWMClose:
		_, _, _ = wvDestroyWindow.Call(hwnd)
		return 0
	case wvWMDestroy:
		wvInstancesMu.Lock()
		delete(wvInstances, hwnd)
		wvInstancesMu.Unlock()
		w.hwnd = 0
		_, _, _ = wvPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := wvDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return ret
}
