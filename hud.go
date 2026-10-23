//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/imsxx/diablo-lazy-macro/internal/store"
	"github.com/imsxx/diablo-lazy-macro/internal/win32"
	"golang.org/x/sys/windows"
)

var (
	gdi32DLL                   = windows.NewLazySystemDLL("gdi32.dll")
	procBeginPaint             = user32DLL.NewProc("BeginPaint")
	procEndPaint               = user32DLL.NewProc("EndPaint")
	procCreateCompatibleDC     = gdi32DLL.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32DLL.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32DLL.NewProc("SelectObject")
	procDeleteDC               = gdi32DLL.NewProc("DeleteDC")
	procDeleteObject           = gdi32DLL.NewProc("DeleteObject")
	procSetBkMode              = gdi32DLL.NewProc("SetBkMode")
	procSetTextColor           = gdi32DLL.NewProc("SetTextColor")
	procCreateFontW            = gdi32DLL.NewProc("CreateFontW")
	procCreateSolidBrush       = gdi32DLL.NewProc("CreateSolidBrush")
	procGetStockObject         = gdi32DLL.NewProc("GetStockObject")
	procFillRect               = user32DLL.NewProc("FillRect")
	procFrameRect              = user32DLL.NewProc("FrameRect")
	procDrawTextW              = user32DLL.NewProc("DrawTextW")
	procBitBlt                 = gdi32DLL.NewProc("BitBlt")
	procEllipse                = gdi32DLL.NewProc("Ellipse")
	procRegisterClassExW       = user32DLL.NewProc("RegisterClassExW")
	procCreateWindowExW        = user32DLL.NewProc("CreateWindowExW")
	procDestroyWindow          = user32DLL.NewProc("DestroyWindow")
	procTrackMouseEvent        = user32DLL.NewProc("TrackMouseEvent")
	procLoadCursorW            = user32DLL.NewProc("LoadCursorW")

	procCreatePopupMenu  = user32DLL.NewProc("CreatePopupMenu")
	procAppendMenuW      = user32DLL.NewProc("AppendMenuW")
	procTrackPopupMenuEx = user32DLL.NewProc("TrackPopupMenuEx")
	procDestroyMenu      = user32DLL.NewProc("DestroyMenu")
	procGetCursorPos     = user32DLL.NewProc("GetCursorPos")
	procGetWindowRect    = user32DLL.NewProc("GetWindowRect")
	procInvalidateRect   = user32DLL.NewProc("InvalidateRect")
	procUpdateWindow     = user32DLL.NewProc("UpdateWindow")
	procDefWindowProcW   = user32DLL.NewProc("DefWindowProcW")
	procGetDpiForWindow  = user32DLL.NewProc("GetDpiForWindow")
)

const (
	hudClassName = "DLM_FloatingHUD"

	// 横条要放得下「技能宏运行中」这类完整文案：两字标签看不出含义，不能为了窄而牺牲可读性。
	hudBaseBarW   = 232
	hudBaseBarH   = 26
	hudBaseBadgeW = 54
	hudBaseBadgeH = 26

	WS_POPUP          = 0x80000000
	WS_EX_TOPMOST     = 0x00000008
	WS_EX_TOOLWINDOW  = 0x00000080
	SWP_NOACTIVATE    = 0x0010
	SW_SHOWNOACTIVATE = 4

	WM_PAINT           = 0x000F
	WM_DESTROY         = 0x0002
	WM_LBUTTONDOWN     = 0x0201
	WM_LBUTTONUP       = 0x0202
	WM_RBUTTONUP       = 0x0205
	WM_LBUTTONDBLCLK   = 0x0203
	WM_NCLBUTTONDBLCLK = 0x00A3
	WM_MOUSEMOVE       = 0x0200
	WM_MOUSELEAVE      = 0x02A3
	WM_ERASEBKGND      = 0x0014
	WM_EXITSIZEMOVE    = 0x0232
	WM_DPICHANGED      = 0x02E0

	TME_LEAVE = 0x00000002

	DT_SINGLELINE   = 0x00000020
	DT_VCENTER      = 0x00000004
	DT_CENTER       = 0x00000001
	DT_END_ELLIPSIS = 0x00008000
	SRCCOPY         = 0x00CC0020

	MF_STRING       = 0x00000000
	MF_SEPARATOR    = 0x00000800
	MF_CHECKED      = 0x00000008
	MF_UNCHECKED    = 0x00000000
	TPM_RETURNCMD   = 0x0100
	TPM_RIGHTBUTTON = 0x0002

	cmdModeBar    = 1001
	cmdModeBadge  = 1002
	cmdLockPos    = 1003
	cmdGameOnly   = 1004
	cmdOpenMain   = 1005
	cmdHideHUD    = 1006
	cmdAttachGame = 1007
)

type rect struct {
	Left, Top, Right, Bottom int32
}

type paintStruct struct {
	Hdc         uintptr
	FErase      int32
	RcPaint     rect
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type trackMouseEventStruct struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   uintptr
	DwHoverTime uint32
}

type point struct {
	X, Y int32
}

type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type floatingHUD struct {
	app  *app
	hwnd uintptr
	mu   sync.Mutex

	// 运行时状态快照
	gameTag     string // "D3", "D4"
	profileName string // "配置 1"
	profileIdx  int    // 0..3
	status      int    // engineStatus 的取值：0 就绪 / 1 运行中 / 2 已暂停 / 3 休眠
	foreground  bool
	visible     bool
	hovered     bool

	// 设置快照：UI 线程在 applySettings 时写入，tick 线程只读（不直接读 settings，避免竞争）
	cfgEnabled  bool
	cfgGameOnly bool
	cfgLocked   bool
	cfgMode     string
	cfgAttach   string // "free" 任意位置 / "game" 贴在游戏窗口客户区
	cfgOffsetX  int
	cfgOffsetY  int

	// 贴游戏窗口时用的句柄与节流时间戳（只在 tick 线程写）
	gameHWND   uintptr
	targetProc string
	lastFollow time.Time
	lastFind   time.Time

	// GDI 预分配画刷与字体
	hFont       uintptr
	bgBrush     uintptr
	borderBrush uintptr
	hoverBrush  uintptr
	greenBrush  uintptr
	orangeBrush uintptr
	redBrush    uintptr
	grayBrush   uintptr
}

var (
	hudInstance     *floatingHUD
	hudInstanceLock sync.Mutex
)

func newFloatingHUD(a *app) *floatingHUD {
	h := &floatingHUD{
		app:         a,
		gameTag:     strings.ToUpper(a.settings.CurrentGame),
		profileName: "配置 1",
		status:      0,
		targetProc:  a.targetProcessOf(a.settings.CurrentGame),
	}

	hudInstanceLock.Lock()
	hudInstance = h
	hudInstanceLock.Unlock()

	h.createWindow()
	h.initGDI()
	h.syncConfig()
	h.applySettings()
	if h.hwnd == 0 {
		a.log.Printf("[HUD] 悬浮指示器窗口创建失败，本次运行不可用")
	} else {
		cfg := a.settings.FloatingHUD
		a.log.Printf("[HUD] 悬浮指示器已创建：位置 (%d,%d)，开关=%v，仅前台显示=%v",
			cfg.X, cfg.Y, cfg.Enabled, cfg.GameOnly)
	}
	return h
}

func (h *floatingHUD) dpi() int {
	if procGetDpiForWindow.Find() == nil && h.hwnd != 0 {
		r, _, _ := procGetDpiForWindow.Call(h.hwnd)
		if r > 0 {
			return int(r)
		}
	}
	return 96
}

func (h *floatingHUD) scale(val int) int {
	dpi := h.dpi()
	return (val*dpi + 48) / 96
}

func (h *floatingHUD) initGDI() {
	if h.hFont != 0 {
		procDeleteObject.Call(h.hFont)
		h.hFont = 0
	}

	// 微软雅黑 UI (高度随 DPI 自适应缩放，粗度 600)
	fontName, _ := windows.UTF16PtrFromString("Microsoft YaHei UI")
	fHeight := int32(-h.scale(12))
	f, _, _ := procCreateFontW.Call(
		uintptr(fHeight), 0, 0, 0,
		uintptr(600), 0, 0, 0,
		1, // DEFAULT_CHARSET
		0, 0,
		5, // CLEARTYPE_QUALITY
		0,
		uintptr(unsafe.Pointer(fontName)),
	)
	h.hFont = f

	// 预置电竞暗黑风格色彩画刷 (RGB: 0x00bbggrr)
	if h.bgBrush == 0 {
		bBg, _, _ := procCreateSolidBrush.Call(0x001B1716)     // #16171b
		bBorder, _, _ := procCreateSolidBrush.Call(0x00382E2B) // #2b2e38
		bHover, _, _ := procCreateSolidBrush.Call(0x00509BC6)  // #c69b50
		bGreen, _, _ := procCreateSolidBrush.Call(0x0081B910)  // #10b981
		bOrange, _, _ := procCreateSolidBrush.Call(0x000B9EF5) // #f59e0b
		bRed, _, _ := procCreateSolidBrush.Call(0x004444EF)    // #ef4444
		bGray, _, _ := procCreateSolidBrush.Call(0x0080726B)   // #6b7280

		h.bgBrush = bBg
		h.borderBrush = bBorder
		h.hoverBrush = bHover
		h.greenBrush = bGreen
		h.orangeBrush = bOrange
		h.redBrush = bRed
		h.grayBrush = bGray
	}
}

func hudWndProc(hwnd uintptr, msg uint32, wp, lp uintptr) (ret uintptr) {
	// 这是 Windows 回调，panic 会直接终止进程且没有任何可见输出：兜住并写日志。
	defer func() {
		if r := recover(); r != nil {
			ret = 0
			hudInstanceLock.Lock()
			inst := hudInstance
			hudInstanceLock.Unlock()
			if inst != nil && inst.app != nil && inst.app.log != nil {
				inst.app.log.Printf("[崩溃] 悬浮窗消息处理异常（msg=%d）：%v", msg, r)
			}
		}
	}()

	hudInstanceLock.Lock()
	h := hudInstance
	hudInstanceLock.Unlock()

	if h == nil {
		r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
		return r
	}

	switch msg {
	case WM_PAINT:
		h.paint(hwnd)
		return 0

	case WM_ERASEBKGND:
		return 1 // 双缓冲自绘背景，防止闪烁

	case WM_LBUTTONDOWN:
		if _, _, locked, _ := h.config(); !locked {
			procReleaseCapture.Call()
			procSendMessageW.Call(hwnd, uintptr(WM_NCLBUTTONDOWN), uintptr(HTCAPTION), 0)
		}
		return 0

	case WM_LBUTTONDBLCLK, WM_NCLBUTTONDBLCLK:
		h.app.sync(func() {
			h.app.showMainWindow()
		})
		return 0

	case WM_EXITSIZEMOVE:
		var rc rect
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		s := &h.app.settings.FloatingHUD
		if s.Attach == "game" {
			// 贴游戏时保存的是相对游戏客户区的偏移，游戏窗口重新开在别处也不会跑偏。
			if gx, gy, ok := h.clientOrigin(); ok {
				s.OffsetX = int(rc.Left) - gx
				s.OffsetY = int(rc.Top) - gy
			}
		} else {
			s.X = int(rc.Left)
			s.Y = int(rc.Top)
		}
		_ = store.SaveSettings(h.app.dir, *h.app.settings)
		return 0

	case WM_DPICHANGED:
		h.initGDI()
		h.applySettings()
		return 0

	case WM_MOUSEMOVE:
		if !h.isHovered() {
			h.markHovered(true)
			var tme trackMouseEventStruct
			tme.CbSize = uint32(unsafe.Sizeof(tme))
			tme.DwFlags = TME_LEAVE
			tme.HwndTrack = hwnd
			procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
			procInvalidateRect.Call(hwnd, 0, 0)
		}
		return 0

	case WM_MOUSELEAVE:
		h.markHovered(false)
		procInvalidateRect.Call(hwnd, 0, 0)
		return 0

	case WM_RBUTTONUP:
		h.showContextMenu(hwnd)
		return 0

	case WM_DESTROY:
		return 0
	}

	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

func (h *floatingHUD) createWindow() {
	var hinst windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &hinst)

	className, _ := windows.UTF16PtrFromString(hudClassName)
	cursor, _, _ := procLoadCursorW.Call(0, uintptr(32512)) // IDC_ARROW
	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		Style:         0x0008, // CS_DBLCLKS
		LpfnWndProc:   windows.NewCallback(hudWndProc),
		HInstance:     uintptr(hinst),
		HCursor:       cursor,
		LpszClassName: className,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	w, height := h.dimensions()
	x := h.app.settings.FloatingHUD.X
	y := h.app.settings.FloatingHUD.Y
	if x <= 0 && y <= 0 {
		x, y = 80, 80
	}

	title, _ := windows.UTF16PtrFromString("Diablo Lazy Macro HUD")
	hwnd, _, _ := procCreateWindowExW.Call(
		uintptr(WS_EX_TOPMOST|WS_EX_TOOLWINDOW),
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		uintptr(WS_POPUP),
		uintptr(x),
		uintptr(y),
		uintptr(w),
		uintptr(height),
		0, 0, uintptr(hinst), 0,
	)
	h.hwnd = hwnd

	darkMode := int32(1)
	procDwmSetWindowAttribute.Call(hwnd, uintptr(DWMWA_USE_IMMERSIVE_DARK_MODE), uintptr(unsafe.Pointer(&darkMode)), 4)
}

// syncConfig 把设置拷进带锁的快照（UI 线程调用）；tick 线程只读快照。
func (h *floatingHUD) syncConfig() {
	s := h.app.settings.FloatingHUD
	h.mu.Lock()
	h.cfgEnabled = s.Enabled
	h.cfgGameOnly = s.GameOnly
	h.cfgLocked = s.Locked
	h.cfgMode = s.Mode
	h.cfgAttach = s.Attach
	h.cfgOffsetX = s.OffsetX
	h.cfgOffsetY = s.OffsetY
	h.mu.Unlock()
}

// config 返回设置快照（enabled, gameOnly, locked, mode）。
func (h *floatingHUD) config() (bool, bool, bool, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfgEnabled, h.cfgGameOnly, h.cfgLocked, h.cfgMode
}

func (h *floatingHUD) dimensions() (int, int) {
	if _, _, _, mode := h.config(); mode == "badge" {
		return h.scale(hudBaseBadgeW), h.scale(hudBaseBadgeH)
	}
	return h.scale(hudBaseBarW), h.scale(hudBaseBarH)
}

// isVisible / isForeground / markHovered 是带锁的小访问器：
// 悬浮窗的窗口过程在 UI 线程、状态同步在 tick 线程，字段必须统一在 h.mu 下读写。
func (h *floatingHUD) isVisible() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.visible
}

func (h *floatingHUD) isForeground() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.foreground
}

func (h *floatingHUD) isHovered() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hovered
}

func (h *floatingHUD) markHovered(v bool) {
	h.mu.Lock()
	h.hovered = v
	h.mu.Unlock()
}

func (h *floatingHUD) applySettings() {
	if h.hwnd == 0 {
		return
	}
	h.syncConfig()
	enabled, _, _, _ := h.config()
	if !enabled {
		procShowWindow.Call(h.hwnd, uintptr(SW_HIDE))
		h.mu.Lock()
		h.visible = false
		h.mu.Unlock()
		return
	}

	s := h.app.settings.FloatingHUD
	w, height := h.dimensions()
	x, y := s.X, s.Y
	if s.Attach == "game" {
		gx, gy, ok := h.clientOrigin()
		if !ok {
			// 游戏还没开：先不显示，等 followGame 找到窗口后再摆出来。
			procShowWindow.Call(h.hwnd, uintptr(SW_HIDE))
			h.mu.Lock()
			h.visible = false
			h.mu.Unlock()
			return
		}
		x, y = gx+s.OffsetX, gy+s.OffsetY
	}
	// 窗口是以 WS_POPUP（不含 WS_VISIBLE）创建的，光靠 SetWindowPos 不会显示出来；
	// 必须带 SWP_SHOWWINDOW，否则 visible 被标成 true、轮询再也不调 ShowWindow，玩家永远看不到它。
	procSetWindowPos.Call(
		h.hwnd,
		^uintptr(0), // HWND_TOPMOST ((HWND)-1)
		uintptr(x), uintptr(y),
		uintptr(w), uintptr(height),
		uintptr(SWP_NOACTIVATE|SWP_FRAMECHANGED|SWP_SHOWWINDOW),
	)
	procShowWindow.Call(h.hwnd, uintptr(SW_SHOWNOACTIVATE))
	h.mu.Lock()
	h.visible = true
	h.mu.Unlock()

	procInvalidateRect.Call(h.hwnd, 0, 1)
	procUpdateWindow.Call(h.hwnd)
}

// clientOrigin 返回游戏窗口客户区左上角的屏幕坐标（贴游戏定位用）。
// 句柄失效（游戏重启）时重新按目标进程查找一次。
func (h *floatingHUD) clientOrigin() (int, int, bool) {
	h.mu.Lock()
	hwnd := h.gameHWND
	proc := h.targetProc
	h.mu.Unlock()
	if proc == "" {
		proc = h.app.targetProcessOf(h.app.settings.CurrentGame)
	}
	if hwnd == 0 || !win32.IsWindowAlive(windows.Handle(hwnd)) {
		w, ok := win32.FindWindowByProcess(proc)
		if !ok {
			return 0, 0, false
		}
		hwnd = uintptr(w)
		h.mu.Lock()
		h.gameHWND = hwnd
		h.targetProc = proc
		h.lastFind = time.Now()
		h.mu.Unlock()
	}
	_, _, cx, cy, err := win32.GetWindowClientSizeAndOrigin(windows.Handle(hwnd))
	if err != nil {
		return 0, 0, false
	}
	return int(cx), int(cy), true
}

func (h *floatingHUD) hide() {
	if h.isVisible() {
		procShowWindow.Call(h.hwnd, uintptr(SW_HIDE))
		h.mu.Lock()
		h.visible = false
		h.mu.Unlock()
	}
}

// followGame 让悬浮窗贴在游戏客户区上：游戏窗口移动时跟随，最小化/关闭时收起。
// 每 80ms 最多定位一次（tick 是 10ms 级，没必要每帧都算）；找不到窗口时 700ms 才重试一次，
// 避免在没开游戏的桌面上反复枚举窗口。
func (h *floatingHUD) followGame(now time.Time) {
	h.mu.Lock()
	if h.cfgAttach != "game" {
		h.mu.Unlock()
		return
	}
	if !h.lastFollow.IsZero() && now.Sub(h.lastFollow) < 80*time.Millisecond {
		h.mu.Unlock()
		return
	}
	h.lastFollow = now
	hwnd := h.gameHWND
	proc := h.targetProc
	ox, oy := h.cfgOffsetX, h.cfgOffsetY
	lastFind := h.lastFind
	h.mu.Unlock()

	if hwnd == 0 || !win32.IsWindowAlive(windows.Handle(hwnd)) {
		if !lastFind.IsZero() && now.Sub(lastFind) < 700*time.Millisecond {
			h.hide()
			return
		}
		if proc == "" {
			proc = h.app.targetProcessOf(h.app.settings.CurrentGame)
		}
		w, ok := win32.FindWindowByProcess(proc)
		h.mu.Lock()
		h.lastFind = now
		h.mu.Unlock()
		if !ok {
			h.hide()
			return
		}
		hwnd = uintptr(w)
		h.mu.Lock()
		h.gameHWND = hwnd
		h.targetProc = proc
		h.mu.Unlock()
	}
	if win32.IsWindowMinimized(windows.Handle(hwnd)) {
		h.hide()
		return
	}
	_, _, cx, cy, err := win32.GetWindowClientSizeAndOrigin(windows.Handle(hwnd))
	if err != nil {
		h.hide()
		return
	}
	w, hgt := h.dimensions()
	procSetWindowPos.Call(
		h.hwnd,
		^uintptr(0),
		uintptr(int(cx)+ox), uintptr(int(cy)+oy),
		uintptr(w), uintptr(hgt),
		uintptr(SWP_NOACTIVATE|SWP_SHOWWINDOW),
	)
	if !h.isVisible() {
		procShowWindow.Call(h.hwnd, uintptr(SW_SHOWNOACTIVATE))
		h.mu.Lock()
		h.visible = true
		h.mu.Unlock()
	}
}

func (h *floatingHUD) resetPosition() {
	if h.hwnd == 0 {
		return
	}
	s := &h.app.settings.FloatingHUD
	w, height := h.dimensions()
	x, y := 80, 80
	if s.Attach == "game" {
		// 贴游戏时「重置」= 回到游戏客户区左上角，而不是屏幕左上角。
		s.OffsetX, s.OffsetY = 16, 16
		if gx, gy, ok := h.clientOrigin(); ok {
			x, y = gx+16, gy+16
		}
	} else {
		s.X, s.Y = 80, 80
	}
	procSetWindowPos.Call(
		h.hwnd,
		0,
		uintptr(x), uintptr(y),
		uintptr(w), uintptr(height),
		uintptr(SWP_NOACTIVATE|SWP_NOZORDER|SWP_FRAMECHANGED|SWP_SHOWWINDOW),
	)
	procShowWindow.Call(h.hwnd, uintptr(SW_SHOWNOACTIVATE))
	h.mu.Lock()
	h.visible = true
	h.mu.Unlock()
	procInvalidateRect.Call(h.hwnd, 0, 1)
	_ = store.SaveSettings(h.app.dir, *h.app.settings)
}

// update 供主轮询 Ticker 与状态变更驱动悬浮窗同步
func (h *floatingHUD) update(snap runtimeSnapshot, foreground bool) {
	if h.hwnd == 0 {
		return
	}
	enabled, gameOnly, _, _ := h.config()
	if !enabled || (gameOnly && !foreground) {
		if h.isVisible() {
			procShowWindow.Call(h.hwnd, uintptr(SW_HIDE))
			h.mu.Lock()
			h.visible = false
			h.mu.Unlock()
		}
		return
	}

	h.mu.Lock()
	prevStatus, prevTag, prevName := h.status, h.gameTag, h.profileName
	prevForeground := h.foreground

	h.foreground = foreground
	h.gameTag = strings.ToUpper(snap.GameID)
	if h.gameTag == "" {
		h.gameTag = "D3"
	}
	h.targetProc = snap.TargetProcess

	if snap.Engine != nil {
		h.profileIdx = snap.Engine.ProfileIndex()
		if snap.Raw != nil && h.profileIdx < len(snap.Raw.Profiles) {
			h.profileName = snap.Raw.Profiles[h.profileIdx].Name
		} else {
			h.profileName = fmt.Sprintf("配置 %d", h.profileIdx+1)
		}
		// 状态判定与主窗口底栏共用 status.go 的同一套规则与优先级。
		h.status = int(engineStatusOf(snap.Engine, foreground))
	} else {
		h.status = int(statusReady)
		h.profileName = "未配置"
	}
	needShow := !h.visible
	h.visible = true
	// 只有内容真的变了才重绘：主循环每 5–10ms 调一次，无条件 Invalidate 会让 GDI 一直忙。
	changed := needShow || prevStatus != h.status || prevTag != h.gameTag ||
		prevName != h.profileName || prevForeground != h.foreground
	h.mu.Unlock()

	if needShow {
		procShowWindow.Call(h.hwnd, uintptr(SW_SHOWNOACTIVATE))
	}
	if changed {
		procInvalidateRect.Call(h.hwnd, 0, 0)
	}
	// 贴游戏模式：跟随游戏窗口（内部有 80ms 节流，不会每个 tick 都重定位）。
	h.followGame(time.Now())
}

func (h *floatingHUD) paint(hwnd uintptr) {
	h.mu.Lock()
	status := h.status
	gameTag := h.gameTag
	profName := h.profileName
	profIdx := h.profileIdx
	hovered := h.hovered
	isBadge := h.cfgMode == "badge"
	h.mu.Unlock()

	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	w, height := h.dimensions()

	memDC, _, _ := procCreateCompatibleDC.Call(hdc)
	defer procDeleteDC.Call(memDC)

	memBmp, _, _ := procCreateCompatibleBitmap.Call(hdc, uintptr(w), uintptr(height))
	defer procDeleteObject.Call(memBmp)

	procSelectObject.Call(memDC, memBmp)
	procSelectObject.Call(memDC, h.hFont)
	procSetBkMode.Call(memDC, 1) // TRANSPARENT

	// 1. 填充深色背景 (#16171b)
	rcClient := rect{Left: 0, Top: 0, Right: int32(w), Bottom: int32(height)}
	procFillRect.Call(memDC, uintptr(unsafe.Pointer(&rcClient)), h.bgBrush)

	// 2. 直角战术边框 (悬停时琥珀金辉光 #c69b50，常态暗钢灰 #2b2e38)
	if hovered {
		procFrameRect.Call(memDC, uintptr(unsafe.Pointer(&rcClient)), h.hoverBrush)
	} else {
		procFrameRect.Call(memDC, uintptr(unsafe.Pointer(&rcClient)), h.borderBrush)
	}

	// 3. 状态呼吸指示灯画刷与文字颜色 (0x00bbggrr)
	//    状态取值与文案来自 status.go：与主窗口底栏同源。
	st := engineStatus(status)
	var dotBrush uintptr
	var statusColor uint32
	switch st {
	case statusRunning:
		dotBrush = h.orangeBrush
		statusColor = 0x000B9EF5 // #f59e0b 运行橙
	case statusPaused:
		dotBrush = h.redBrush
		statusColor = 0x004444EF // #ef4444 暂停红
	case statusDormant:
		dotBrush = h.grayBrush
		statusColor = 0x0080726B // #6b7280 休眠灰
	default:
		dotBrush = h.greenBrush
		statusColor = 0x0081B910 // #10b981 就绪绿
	}
	statusText := engineStatusShort(st)

	// 4. 绘制状态指示圆点（使用 NULL_PEN 去除黑边）
	nullPen, _, _ := procGetStockObject.Call(8) // NULL_PEN = 8
	procSelectObject.Call(memDC, nullPen)
	procSelectObject.Call(memDC, dotBrush)

	dotR := h.scale(4)
	dotCx := h.scale(11)
	dotCy := height / 2
	procEllipse.Call(memDC, uintptr(dotCx-dotR), uintptr(dotCy-dotR), uintptr(dotCx+dotR), uintptr(dotCy+dotR))

	if isBadge {
		// 紧凑方标形态：显示 D3-1
		badgeStr, _ := windows.UTF16PtrFromString(fmt.Sprintf("%s-%d", gameTag, profIdx+1))
		procSetTextColor.Call(memDC, uintptr(statusColor))
		rText := rect{Left: int32(dotCx + dotR + h.scale(2)), Top: 0, Right: int32(w) - int32(h.scale(3)), Bottom: int32(height)}
		procDrawTextW.Call(memDC, uintptr(unsafe.Pointer(badgeStr)), ^uintptr(0), uintptr(unsafe.Pointer(&rText)), uintptr(DT_SINGLELINE|DT_VCENTER|DT_CENTER))
	} else {
		// 战术小横条形态：[● 运行中 │ D3 · 配置 1]
		// 状态文字
		procSetTextColor.Call(memDC, uintptr(statusColor))
		sTextW, _ := windows.UTF16PtrFromString(statusText)
		rStatus := rect{Left: int32(dotCx + dotR + h.scale(4)), Top: 0, Right: int32(h.scale(112)), Bottom: int32(height)}
		procDrawTextW.Call(memDC, uintptr(unsafe.Pointer(sTextW)), ^uintptr(0), uintptr(unsafe.Pointer(&rStatus)), uintptr(DT_SINGLELINE|DT_VCENTER))

		// 竖向分隔线
		procSetTextColor.Call(memDC, 0x00544440) // #404454
		sDivW, _ := windows.UTF16PtrFromString("│")
		rDiv := rect{Left: int32(h.scale(62)), Top: 0, Right: int32(h.scale(70)), Bottom: int32(height)}
		procDrawTextW.Call(memDC, uintptr(unsafe.Pointer(sDivW)), ^uintptr(0), uintptr(unsafe.Pointer(&rDiv)), uintptr(DT_SINGLELINE|DT_VCENTER))

		// 游戏代数与方案名：如 "D3 · 配置 1"
		procSetTextColor.Call(memDC, 0x00D4CDC9) // #c9cdd4
		summary := fmt.Sprintf("%s · %s", gameTag, profName)
		sProfW, _ := windows.UTF16PtrFromString(summary)
		rProf := rect{Left: int32(h.scale(71)), Top: 0, Right: int32(w) - int32(h.scale(6)), Bottom: int32(height)}
		procDrawTextW.Call(memDC, uintptr(unsafe.Pointer(sProfW)), ^uintptr(0), uintptr(unsafe.Pointer(&rProf)), uintptr(DT_SINGLELINE|DT_VCENTER|DT_END_ELLIPSIS))
	}

	// 一次性双缓冲 BitBlt 刷入屏幕，彻底零闪烁
	procBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(height), memDC, 0, 0, uintptr(SRCCOPY))
}

func (h *floatingHUD) showContextMenu(hwnd uintptr) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	defer procDestroyMenu.Call(hMenu)

	s := h.app.settings.FloatingHUD

	checkBar := uintptr(MF_UNCHECKED)
	checkBadge := uintptr(MF_UNCHECKED)
	if s.Mode == "badge" {
		checkBadge = uintptr(MF_CHECKED)
	} else {
		checkBar = uintptr(MF_CHECKED)
	}

	checkLock := uintptr(MF_UNCHECKED)
	if s.Locked {
		checkLock = uintptr(MF_CHECKED)
	}

	checkGameOnly := uintptr(MF_UNCHECKED)
	if s.GameOnly {
		checkGameOnly = uintptr(MF_CHECKED)
	}

	checkAttach := uintptr(MF_UNCHECKED)
	if s.Attach == "game" {
		checkAttach = uintptr(MF_CHECKED)
	}

	mBar, _ := windows.UTF16PtrFromString("战术横条形态")
	mBadge, _ := windows.UTF16PtrFromString("紧凑方标形态")
	mAttach, _ := windows.UTF16PtrFromString("贴在游戏窗口上（随游戏移动）")
	mLock, _ := windows.UTF16PtrFromString("锁定位置 (禁止拖拽)")
	mGameOnly, _ := windows.UTF16PtrFromString("仅游戏在前台时显示")
	mOpen, _ := windows.UTF16PtrFromString("打开主设置窗口")
	mHide, _ := windows.UTF16PtrFromString("隐藏悬浮窗")

	procAppendMenuW.Call(hMenu, uintptr(MF_STRING)|checkBar, uintptr(cmdModeBar), uintptr(unsafe.Pointer(mBar)))
	procAppendMenuW.Call(hMenu, uintptr(MF_STRING)|checkBadge, uintptr(cmdModeBadge), uintptr(unsafe.Pointer(mBadge)))
	procAppendMenuW.Call(hMenu, uintptr(MF_SEPARATOR), 0, 0)
	procAppendMenuW.Call(hMenu, uintptr(MF_STRING)|checkAttach, uintptr(cmdAttachGame), uintptr(unsafe.Pointer(mAttach)))
	procAppendMenuW.Call(hMenu, uintptr(MF_STRING)|checkLock, uintptr(cmdLockPos), uintptr(unsafe.Pointer(mLock)))
	procAppendMenuW.Call(hMenu, uintptr(MF_STRING)|checkGameOnly, uintptr(cmdGameOnly), uintptr(unsafe.Pointer(mGameOnly)))
	procAppendMenuW.Call(hMenu, uintptr(MF_SEPARATOR), 0, 0)
	procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(cmdOpenMain), uintptr(unsafe.Pointer(mOpen)))
	procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(cmdHideHUD), uintptr(unsafe.Pointer(mHide)))

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	procSetForeground.Call(hwnd)
	cmd, _, _ := procTrackPopupMenuEx.Call(
		hMenu,
		uintptr(TPM_RETURNCMD|TPM_RIGHTBUTTON),
		uintptr(pt.X), uintptr(pt.Y),
		hwnd, 0,
	)

	switch cmd {
	case cmdModeBar:
		h.app.settings.FloatingHUD.Mode = "bar"
		_ = store.SaveSettings(h.app.dir, *h.app.settings)
		h.applySettings()
		if h.app.win != nil {
			h.app.win.refreshStatus()
		}

	case cmdModeBadge:
		h.app.settings.FloatingHUD.Mode = "badge"
		_ = store.SaveSettings(h.app.dir, *h.app.settings)
		h.applySettings()
		if h.app.win != nil {
			h.app.win.refreshStatus()
		}

	case cmdAttachGame:
		// 两种定位互转：按当前实际位置换算，避免切模式瞬间跳到屏幕角落。
		s := &h.app.settings.FloatingHUD
		var rc rect
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		if s.Attach == "game" {
			s.Attach = "free"
			s.X = int(rc.Left)
			s.Y = int(rc.Top)
		} else {
			if gx, gy, ok := h.clientOrigin(); ok {
				s.Attach = "game"
				s.OffsetX = int(rc.Left) - gx
				s.OffsetY = int(rc.Top) - gy
			} else {
				h.app.log.Printf("[HUD] 没找到游戏窗口，暂时无法贴到游戏上（开游戏后再试）")
				return
			}
		}
		_ = store.SaveSettings(h.app.dir, *h.app.settings)
		h.applySettings()
		if h.app.win != nil {
			h.app.win.refreshStatus()
		}

	case cmdLockPos:
		h.app.settings.FloatingHUD.Locked = !h.app.settings.FloatingHUD.Locked
		_ = store.SaveSettings(h.app.dir, *h.app.settings)
		if h.app.win != nil {
			h.app.win.refreshStatus()
		}

	case cmdGameOnly:
		h.app.settings.FloatingHUD.GameOnly = !h.app.settings.FloatingHUD.GameOnly
		_ = store.SaveSettings(h.app.dir, *h.app.settings)
		if h.app.win != nil {
			h.app.win.refreshStatus()
		}

	case cmdOpenMain:
		h.app.sync(func() {
			h.app.showMainWindow()
		})

	case cmdHideHUD:
		h.app.settings.FloatingHUD.Enabled = false
		_ = store.SaveSettings(h.app.dir, *h.app.settings)
		h.applySettings()
		if h.app.win != nil {
			h.app.win.refreshStatus()
		}
	}
}

func (h *floatingHUD) destroy() {
	if h.hwnd != 0 {
		procDestroyWindow.Call(h.hwnd)
		h.hwnd = 0
	}
	if h.hFont != 0 {
		procDeleteObject.Call(h.hFont)
		h.hFont = 0
	}
	if h.bgBrush != 0 {
		procDeleteObject.Call(h.bgBrush)
	}
	if h.borderBrush != 0 {
		procDeleteObject.Call(h.borderBrush)
	}
	if h.hoverBrush != 0 {
		procDeleteObject.Call(h.hoverBrush)
	}
	if h.greenBrush != 0 {
		procDeleteObject.Call(h.greenBrush)
	}
	if h.orangeBrush != 0 {
		procDeleteObject.Call(h.orangeBrush)
	}
	if h.redBrush != 0 {
		procDeleteObject.Call(h.redBrush)
	}
	if h.grayBrush != 0 {
		procDeleteObject.Call(h.grayBrush)
	}
}
