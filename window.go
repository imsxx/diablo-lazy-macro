//go:build windows

package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/imsxx/diablo-lazy-macro/internal/config"
	"github.com/imsxx/diablo-lazy-macro/internal/games"
	"github.com/imsxx/diablo-lazy-macro/internal/helper"
	"github.com/imsxx/diablo-lazy-macro/internal/keys"
	"github.com/imsxx/diablo-lazy-macro/internal/store"

	webview2 "github.com/jchv/go-webview2"
	"github.com/lxn/walk"
	"golang.org/x/sys/windows"
)

//go:embed web/index.html web/style.css web/app.js
var webAssets embed.FS

var (
	user32DLL   = windows.NewLazySystemDLL("user32.dll")
	kernel32DLL = windows.NewLazySystemDLL("kernel32.dll")
	dwmDLL      = windows.NewLazySystemDLL("dwmapi.dll")

	procShowWindow            = user32DLL.NewProc("ShowWindow")
	procSetForeground         = user32DLL.NewProc("SetForegroundWindow")
	procSetWindowLongPtr      = user32DLL.NewProc("SetWindowLongPtrW")
	procCallWindowProc        = user32DLL.NewProc("CallWindowProcW")
	procGetWindowLong         = user32DLL.NewProc("GetWindowLongW")
	procSetWindowLong         = user32DLL.NewProc("SetWindowLongW")
	procSetWindowPos          = user32DLL.NewProc("SetWindowPos")
	procReleaseCapture        = user32DLL.NewProc("ReleaseCapture")
	procSendMessageW          = user32DLL.NewProc("SendMessageW")
	procSetFocus              = user32DLL.NewProc("SetFocus")
	procGetSystemMetrics      = user32DLL.NewProc("GetSystemMetrics")
	procSetWindowsHookExW     = user32DLL.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx   = user32DLL.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx        = user32DLL.NewProc("CallNextHookEx")
	procGetCurrentThreadId    = kernel32DLL.NewProc("GetCurrentThreadId")
	procDwmSetWindowAttribute = dwmDLL.NewProc("DwmSetWindowAttribute")
)

const (
	// maxProfiles 是方案容量上限（协议允许 1–4 个）。默认只给玩家 1 个方案，
	// 其余由界面上的「+」按需新增——一上来摆 4 个空方案只会让玩家多按几次 Tab。
	maxProfiles = 4
	numSlots    = 6

	// 窗口的基准尺寸（CSS 像素 / 96 DPI）。实际窗口按屏幕 DPI 等比放大，
	// 这样 WebView2 的 CSS 视口恒等于基准尺寸，界面布局在 125%/150% 缩放下不会被裁切。
	baseWinWidth  = 1060
	baseWinHeight = 660

	// 界面侧取值上限：必须与 internal/config 的协议约束保持一致（由 slots_test.go 守护）。
	maxDelayMSUI   = 30000
	maxNameRunesUI = 24

	SW_HIDE     = 0
	SW_SHOW     = 5
	SW_MINIMIZE = 6
)

const (
	WS_CAPTION     = 0x00C00000
	WS_THICKFRAME  = 0x00040000
	WS_MINIMIZEBOX = 0x00020000
	WS_MAXIMIZEBOX = 0x00010000

	SWP_NOSIZE       = 0x0001
	SWP_NOMOVE       = 0x0002
	SWP_NOZORDER     = 0x0004
	SWP_FRAMECHANGED = 0x0020
	SWP_SHOWWINDOW   = 0x0040

	WM_CLOSE         = 0x0010
	WM_NCLBUTTONDOWN = 0x00A1
	HTCAPTION        = 2

	WM_MOUSEACTIVATE = 0x0021
	MA_ACTIVATE      = 1
	WM_SETFOCUS      = 0x0007

	WH_CBT                        = 5
	HCBT_CREATEWND                = 3
	SM_CXSCREEN                   = 0
	SM_CYSCREEN                   = 1
	DWMWA_USE_IMMERSIVE_DARK_MODE = 20
)

type cbtCreateWnd struct {
	lpcs            *createStruct
	hwndInsertAfter uintptr
}

type createStruct struct {
	lpCreateParams uintptr
	hInstance      uintptr
	hMenu          uintptr
	hwndParent     uintptr
	cy             int32
	cx             int32
	y              int32
	x              int32
	style          int32
	lpszName       *uint16
	lpszClass      *uint16
	dwExStyle      uint32
}

const (
	strategyDisabled = config.StrategyDisabled
	strategyHold     = config.StrategyHold
	strategySpam     = config.StrategySpam
	strategyBuff     = config.StrategyBuff
)

type skillSlotData struct {
	Name        string `json:"name"`
	DefaultKey  string `json:"defaultKey"`
	Key         string `json:"key"`
	Strategy    int    `json:"strategy"`
	IntervalMS  int    `json:"intervalMS"`
	DelayMS     int    `json:"delayMS"`
	RandomDelay bool   `json:"randomDelay"`
}

func defaultSlots() [numSlots]skillSlotData {
	var out [numSlots]skillSlotData
	defs := games.DefaultAdapter().Slots()
	for i := 0; i < numSlots && i < len(defs); i++ {
		out[i] = skillSlotData{
			Name:       defs[i].Name,
			DefaultKey: defs[i].DefaultKey,
			Key:        defs[i].DefaultKey,
			IntervalMS: 300,
		}
	}
	return out
}

type frontendSlot struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	KeyLabel    string `json:"keyLabel"` // 界面显示名（中文），由 keyitems.go 统一给出
	Strategy    int    `json:"strategy"`
	IntervalMS  int    `json:"intervalMS"`
	DelayMS     int    `json:"delayMS"`
	RandomDelay bool   `json:"randomDelay"`
	Explanation string `json:"explanation"` // 一行说明：让玩家不在界面上猜「保持Buff」是什么意思
}

type frontendProfile struct {
	Name    string         `json:"name"`
	HK      string         `json:"hk"`
	HKLabel string         `json:"hkLabel"`
	Mode    string         `json:"mode"`
	Slots   []frontendSlot `json:"slots"`
}

type helperMeta struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// frontendEngineState 是宏运行态：状态模型与文案都由 status.go 统一产出，
// 主窗口底栏与游戏悬浮窗因此不可能各说一套。
type frontendEngineState struct {
	Configured bool   `json:"configured"` // 是否已加载配置；false 时界面只提示导入分享码
	Status     string `json:"status"`     // ready / running / paused / dormant
	StatusText string `json:"statusText"` // 底栏完整文案（含启停键名）
	HoldMode   bool   `json:"holdMode"`   // 启动方式：仅按下时（按住生效）
	Enabled    bool   `json:"enabled"`
	Paused     bool   `json:"paused"`
	Foreground bool   `json:"foreground"`
}

type frontendState struct {
	Version           string            `json:"version"`
	CurrentGame       string            `json:"currentGame"`
	CurrentProfileIdx int               `json:"currentProfileIdx"`
	ProfileNames      []string          `json:"profileNames"`
	Profiles          []frontendProfile `json:"profiles"`
	ToggleKey         string            `json:"toggleKey"`
	ToggleKeyLabel    string            `json:"toggleKeyLabel"`
	StartMode         int               `json:"startMode"`
	QuickPause        struct {
		Enabled  bool `json:"enabled"`
		Trigger  int  `json:"trigger"`
		MouseKey int  `json:"mouseKey"`
		Action   int  `json:"action"`
		DelayMS  int  `json:"delayMS"`
	} `json:"quickPause"`
	Helpers struct {
		Hotkey        string          `json:"hotkey"`
		HotkeyLabel   string          `json:"hotkeyLabel"`
		DelayMS       int             `json:"delayMS"`
		ReforgeTarget int             `json:"reforgeTarget"`
		DevMode       bool            `json:"devMode"`
		Enabled       map[string]bool `json:"enabled"`
	} `json:"helpers"`
	AllHelpers         []helperMeta        `json:"allHelpers"`
	SmartPause         bool                `json:"smartPause"`
	SoundCue           bool                `json:"soundCue"`
	ForceStandEnabled  bool                `json:"forceStandEnabled"`
	ForceStandKey      string              `json:"forceStandKey"`
	ForceStandKeyLabel string              `json:"forceStandKeyLabel"`
	TargetProcess      string              `json:"targetProcess"`
	Engine             frontendEngineState `json:"engine"`
	HUD                frontendHUD         `json:"hud"`
}

type frontendHUD struct {
	Enabled  bool   `json:"enabled"`
	Mode     string `json:"mode"`
	Locked   bool   `json:"locked"`
	GameOnly bool   `json:"gameOnly"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
}

// pendingImport 是「已预览、等待玩家确认」的分享码内容（两步导入的中间态）。
type pendingImport struct {
	raw    config.Raw
	parsed *config.Parsed
}

type mainWindow struct {
	app  *app
	wv   webview2.WebView
	hwnd uintptr

	// 数组按容量（maxProfiles）分配，profileCount 才是「玩家当前有几个方案」。
	// 界面只下发与提交前 profileCount 个，其余的数组槽位不可见、也不写进存档。
	profileSlots [maxProfiles][numSlots]skillSlotData
	profileNames [maxProfiles]string
	profileHKs   [maxProfiles]string
	profileModes [maxProfiles]string
	profileCount int
	startMode    int

	profileIdx int
	raw        config.Raw
	loading    bool

	// 窗口在当前 DPI 下的实际物理尺寸（CSS 视口恒为基准尺寸）。
	winW, winH int

	pendingImport   *pendingImport
	hiddenHintShown bool
	quitting        bool
	shown           bool
}

// dpi 返回窗口所在显示器的 DPI（取不到时按 96 处理）。
func (w *mainWindow) dpi() int {
	if procGetDpiForWindow.Find() == nil && w.hwnd != 0 {
		if r, _, _ := procGetDpiForWindow.Call(w.hwnd); r > 0 {
			return int(r)
		}
	}
	return 96
}

// scaledSize 把基准尺寸换算成当前 DPI 下的物理像素。
// WebView2 的 CSS 视口 = 物理尺寸 ÷ 缩放系数，所以必须随 DPI 放大窗口，
// 否则 125%/150% 缩放下视口小于布局所需宽度，右栏会被直接裁掉。
func (w *mainWindow) scaledSize() (int, int) {
	d := w.dpi()
	return (baseWinWidth*d + 48) / 96, (baseWinHeight*d + 48) / 96
}

// applyDPISize 按当前 DPI 重算并应用窗口尺寸（DPI 变化后调用）。
func (w *mainWindow) applyDPISize() {
	if w.hwnd == 0 || w.wv == nil {
		return
	}
	width, height := w.scaledSize()
	if width == w.winW && height == w.winH {
		return
	}
	w.winW, w.winH = width, height
	w.wv.SetSize(width, height, webview2.HintFixed)

	var rc rect
	procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&rc)))
	procSetWindowPos.Call(w.hwnd, 0,
		uintptr(rc.Left), uintptr(rc.Top),
		uintptr(width), uintptr(height),
		uintptr(SWP_NOZORDER|SWP_NOACTIVATE))
}

func (a *app) createMainWindow() error {
	w := &mainWindow{
		app: a,
	}

	htmlBytes, err := webAssets.ReadFile("web/index.html")
	if err != nil {
		return fmt.Errorf("读取 index.html 失败：%v", err)
	}
	cssBytes, err := webAssets.ReadFile("web/style.css")
	if err != nil {
		return fmt.Errorf("读取 style.css 失败：%v", err)
	}
	jsBytes, err := webAssets.ReadFile("web/app.js")
	if err != nil {
		return fmt.Errorf("读取 app.js 失败：%v", err)
	}

	fullHtml := string(htmlBytes)
	fullHtml = strings.Replace(fullHtml, `<link rel="stylesheet" href="style.css">`, "<style>"+string(cssBytes)+"</style>", 1)
	fullHtml = strings.Replace(fullHtml, `<script src="app.js"></script>`, "<script>"+string(jsBytes)+"</script>", 1)

	// 安装临时 CBT 钩子：在 CreateWindowExW 触发时直接剥除原生标题栏并移出屏幕可视范围，
	// 彻底消除启动时 Windows 原生白屏与标题栏的闪烁
	var hook uintptr
	hookCallback := windows.NewCallback(func(nCode int32, wp uintptr, lp unsafe.Pointer) uintptr {
		if nCode == HCBT_CREATEWND && lp != nil {
			cbt := (*cbtCreateWnd)(lp)
			if cbt.lpcs != nil {
				cbt.lpcs.style &^= (WS_CAPTION | WS_THICKFRAME | WS_MAXIMIZEBOX)
				cbt.lpcs.x = -32000
				cbt.lpcs.y = -32000
			}
		}
		r, _, _ := procCallNextHookEx.Call(hook, uintptr(nCode), wp, uintptr(lp))
		return r
	})

	tid, _, _ := procGetCurrentThreadId.Call()
	hook, _, _ = procSetWindowsHookExW.Call(
		uintptr(WH_CBT),
		hookCallback,
		0,
		tid,
	)

	// 创建 WebView2 窗口（先按基准尺寸建在屏幕外，显示前再按 DPI 校正）
	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  fmt.Sprintf("%s v%s", appName, version),
			Width:  baseWinWidth,
			Height: baseWinHeight,
			IconId: 1,
			Center: false,
		},
	})

	if hook != 0 {
		procUnhookWindowsHookEx.Call(hook)
	}
	if wv == nil {
		return fmt.Errorf("初始化 WebView2 运行时失败，请确认系统已安装 Edge WebView2 运行时")
	}

	w.wv = wv
	w.hwnd = uintptr(wv.Window())
	a.win = w

	// 启用 DWM 暗黑模式浸润
	darkMode := int32(1)
	procDwmSetWindowAttribute.Call(w.hwnd, uintptr(DWMWA_USE_IMMERSIVE_DARK_MODE), uintptr(unsafe.Pointer(&darkMode)), 4)

	w.removeSystemTitlebar()
	w.applyDPISize()
	w.subclass()
	w.initBindings()

	wv.SetHtml(fullHtml)

	w.loadFromState()

	// 兜底定时器：前端 ready 信号若因异常没到，也要把窗口显示出来（正常情况下由 api_ready 触发）。
	time.AfterFunc(2500*time.Millisecond, func() {
		w.app.sync(func() {
			w.showCentered()
		})
	})

	a.log.Printf("[窗口] 现代 WebView2 电竞 UI 初始化完成（窗口 %dx%d，DPI %d）", w.winW, w.winH, w.dpi())
	return nil
}

func (w *mainWindow) removeSystemTitlebar() {
	if w.hwnd == 0 {
		return
	}
	gwl := -16
	gwlStyle := uintptr(gwl)
	style, _, _ := procGetWindowLong.Call(w.hwnd, gwlStyle)
	style &^= (WS_CAPTION | WS_THICKFRAME | WS_MAXIMIZEBOX)
	procSetWindowLong.Call(w.hwnd, gwlStyle, style)

	width, height := w.scaledSize()
	procSetWindowPos.Call(
		w.hwnd,
		0,
		0, 0,
		uintptr(width),
		uintptr(height),
		uintptr(SWP_NOMOVE|SWP_NOZORDER|SWP_FRAMECHANGED),
	)
}

func (w *mainWindow) showCentered() {
	if w.shown || w.hwnd == 0 {
		return
	}
	w.shown = true

	width, height := w.scaledSize()
	if width != w.winW || height != w.winH {
		w.winW, w.winH = width, height
		w.wv.SetSize(width, height, webview2.HintFixed)
	}

	screenW, _, _ := procGetSystemMetrics.Call(uintptr(SM_CXSCREEN))
	screenH, _, _ := procGetSystemMetrics.Call(uintptr(SM_CYSCREEN))
	centerX := (int32(screenW) - int32(width)) / 2
	centerY := (int32(screenH) - int32(height)) / 2
	if centerX < 0 {
		centerX = 0
	}
	if centerY < 0 {
		centerY = 0
	}

	procSetWindowPos.Call(
		w.hwnd,
		0,
		uintptr(centerX),
		uintptr(centerY),
		uintptr(width),
		uintptr(height),
		uintptr(SWP_NOZORDER|SWP_FRAMECHANGED|SWP_SHOWWINDOW),
	)
	procSetForeground.Call(w.hwnd)
}

func (w *mainWindow) subclass() {
	if w.hwnd == 0 {
		return
	}
	var oldWndProc uintptr
	cb := windows.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		if msg == WM_CLOSE && !w.quitting {
			// 关闭 = 隐藏到托盘（宏继续运行），与右上角 ✕ 行为一致。
			w.hideToTray()
			return 0
		}
		if msg == WM_MOUSEACTIVATE {
			return uintptr(MA_ACTIVATE)
		}
		if msg == WM_SETFOCUS {
			procSetFocus.Call(w.hwnd)
		}
		if msg == WM_DPICHANGED {
			w.applyDPISize()
		}
		r, _, _ := procCallWindowProc.Call(oldWndProc, hwnd, uintptr(msg), wp, lp)
		return r
	})

	gwlp := -4
	gwlpIndex := uintptr(gwlp)
	if procSetWindowLongPtr.Find() == nil {
		r, _, _ := procSetWindowLongPtr.Call(w.hwnd, gwlpIndex, cb)
		oldWndProc = r
	} else {
		procSetWindowLong := user32DLL.NewProc("SetWindowLongW")
		r, _, _ := procSetWindowLong.Call(w.hwnd, gwlpIndex, cb)
		oldWndProc = r
	}
}

// initBindings 将 Go 方法暴露给前端 JS。
// 约定：所有写操作「先校验、后提交」，失败时回滚内存并把错误返回前端（由前端提示）。
// 禁止出现「界面已经改了、磁盘却没写」的静默失败。
func (w *mainWindow) initBindings() {
	bind := func(name string, f interface{}) {
		if err := w.wv.Bind(name, f); err != nil {
			// 注册失败会让前端拿不到接口（表现为界面点了没反应），必须留痕。
			w.app.log.Printf("[界面] 注册接口 %s 失败：%v", name, err)
		}
	}

	bind("api_dragWindow", func() error {
		procReleaseCapture.Call()
		procSendMessageW.Call(w.hwnd, uintptr(WM_NCLBUTTONDOWN), uintptr(HTCAPTION), 0)
		return nil
	})

	bind("api_getState", func() (*frontendState, error) {
		return w.buildFrontendState(), nil
	})

	// 键位捕获：规则与键名映射都由 Go 裁决（见 keyitems.go），前端只提交原始键名。
	bind("api_captureOptions", func(mode string) (captureOptions, error) {
		m, err := parseCaptureMode(mode)
		if err != nil {
			return captureOptions{}, err
		}
		return captureOptionsFor(m), nil
	})

	bind("api_captureKey", func(mode string, rawKey string) (captureResult, error) {
		m, err := parseCaptureMode(mode)
		if err != nil {
			return captureResult{}, err
		}
		token, err := resolveCapturedKey(m, rawKey)
		if err != nil {
			return captureResult{}, err
		}
		return captureResult{Token: token, Label: keyNameOfToken(token)}, nil
	})

	bind("api_switchGame", func(gameID string) error {
		if gameID == w.app.settings.CurrentGame {
			return nil
		}
		if _, ok := games.For(gameID); !ok {
			return fmt.Errorf("未知的游戏 %q", gameID)
		}
		if err := w.saveToState(); err != nil {
			return err
		}
		w.app.switchGame(gameID)
		return nil
	})

	bind("api_switchProfile", func(idx int) error {
		if idx < 0 || idx >= w.profileCount {
			return fmt.Errorf("方案序号 %d 超出范围", idx)
		}
		w.profileIdx = idx
		w.app.switchProfile(idx)
		w.refreshStatus()
		return nil
	})

	bind("api_updateSlot", func(tabIdx, slotIdx int, key string, strategy, interval, delay int, random bool) error {
		if tabIdx < 0 || tabIdx >= w.profileCount || slotIdx < 0 || slotIdx >= numSlots {
			return fmt.Errorf("槽位序号超出范围")
		}
		if strategy < strategyDisabled || strategy > strategyBuff {
			return fmt.Errorf("释放策略取值 %d 无效", strategy)
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		if key == "" {
			return fmt.Errorf("请先为该槽位选择按键")
		}
		if _, err := keys.Parse(key); err != nil {
			return fmt.Errorf("槽位按键无效：%v", err)
		}
		slot := &w.profileSlots[tabIdx][slotIdx]
		prev := *slot
		slot.Key = key
		slot.Strategy = strategy
		slot.IntervalMS = clampInterval(interval, prev.IntervalMS)
		slot.DelayMS = clampDelay(delay)
		slot.RandomDelay = random && slot.DelayMS != 0
		return w.commitOrRollback(func() { *slot = prev })
	})

	bind("api_updateProfileMeta", func(tabIdx int, name, hk, mode string) error {
		if tabIdx < 0 || tabIdx >= w.profileCount {
			return fmt.Errorf("方案序号 %d 超出范围", tabIdx)
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = fmt.Sprintf("配置 %d", tabIdx+1)
		}
		if utf8.RuneCountInString(name) > maxNameRunesUI {
			return fmt.Errorf("配置名最长 %d 个字符", maxNameRunesUI)
		}
		hk = strings.ToUpper(strings.TrimSpace(hk))
		if hk != "" {
			vk, err := keys.Parse(hk)
			if err != nil {
				return fmt.Errorf("方案切换键无效：%v", err)
			}
			if keys.IsMouse(vk) {
				return fmt.Errorf("方案切换键不支持鼠标键（%s），请改用键盘键", keyName(vk))
			}
		}
		mode = strings.TrimSpace(mode)
		if mode != config.ModeConcurrent && mode != config.ModeSequential {
			return fmt.Errorf("按键模式 %q 无效", mode)
		}

		prevName, prevHK, prevMode := w.profileNames[tabIdx], w.profileHKs[tabIdx], w.profileModes[tabIdx]
		w.profileNames[tabIdx] = name
		w.profileHKs[tabIdx] = hk
		w.profileModes[tabIdx] = mode
		return w.commitOrRollback(func() {
			w.profileNames[tabIdx] = prevName
			w.profileHKs[tabIdx] = prevHK
			w.profileModes[tabIdx] = prevMode
		})
	})

	// api_addProfile 在方案列表末尾追加一个空白方案并切过去，返回新方案序号。
	// 空白方案沿用默认键位但全部「禁用」，由玩家自己选策略；协议要求每方案至少 1 条规则，
	// 落盘时由 slotsToProfile 兜底一条占位规则。
	bind("api_addProfile", func() (int, error) {
		if w.profileCount >= maxProfiles {
			return 0, fmt.Errorf("最多 %d 个配置（协议上限），请先删掉不用的再新增", maxProfiles)
		}
		idx := w.profileCount
		prevCount, prevIdx := w.profileCount, w.profileIdx
		prevSlots := w.profileSlots[idx]
		prevName, prevHK, prevMode := w.profileNames[idx], w.profileHKs[idx], w.profileModes[idx]

		w.profileSlots[idx] = defaultSlots()
		w.profileNames[idx] = fmt.Sprintf("配置 %d", idx+1)
		w.profileHKs[idx] = ""
		w.profileModes[idx] = config.ModeConcurrent
		w.profileCount = idx + 1
		w.profileIdx = idx

		err := w.commitOrRollback(func() {
			w.profileCount, w.profileIdx = prevCount, prevIdx
			w.profileSlots[idx] = prevSlots
			w.profileNames[idx], w.profileHKs[idx], w.profileModes[idx] = prevName, prevHK, prevMode
		})
		if err != nil {
			return 0, err
		}
		w.app.log.Printf("[界面] 新增配置「%s」（共 %d 个）", w.profileNames[idx], w.profileCount)
		return idx, nil
	})

	bind("api_updateToggle", func(token string) error {
		token = strings.ToUpper(strings.TrimSpace(token))
		if token == "" {
			return fmt.Errorf("启停键不能为空（默认 %s）", config.DefaultToggleKey)
		}
		if _, err := keys.Parse(token); err != nil {
			return fmt.Errorf("启停键无效：%v", err)
		}
		prev := w.raw.Toggle
		w.raw.Toggle = token
		return w.commitOrRollback(func() { w.raw.Toggle = prev })
	})

	bind("api_updateStartMode", func(mode int) error {
		if mode != 0 && mode != 1 {
			return fmt.Errorf("启动方式取值 %d 无效", mode)
		}
		prev := w.startMode
		w.startMode = mode
		return w.commitOrRollback(func() { w.startMode = prev })
	})

	bind("api_updateQuickPause", func(enabled bool, trigger, mouseKey, action, delayMS int) error {
		qp := normalizeQuickPause(store.QuickPauseSettings{
			Enabled:  enabled,
			Trigger:  trigger,
			MouseKey: mouseKey,
			Action:   action,
			DelayMS:  delayMS,
		}, w.app.settings.QuickPause)
		if err := qp.Validate(); err != nil {
			return err
		}
		prev := w.app.settings.QuickPause
		w.app.settings.QuickPause = qp
		if err := store.SaveSettings(w.app.dir, *w.app.settings); err != nil {
			w.app.settings.QuickPause = prev
			return err
		}
		w.app.updateQuickPause(qp)
		w.refreshStatus()
		return nil
	})

	bind("api_updateHelpers", func(hotkey string, speedMS, reforgeTarget int, devMode bool, enabled []string) error {
		hs := w.app.settings.Helpers
		hotkey = strings.ToUpper(strings.TrimSpace(hotkey))
		hs.Hotkey = hotkey
		hs.DelayMS = clampInt(speedMS, store.HelperMinDelayMS, store.HelperMaxDelayMS, store.DefaultHelperDelayMS)
		hs.ReforgeTarget = clampInt(reforgeTarget, 0, 5, 0)
		hs.DevMode = devMode
		hs.Enabled = map[string]bool{}
		known := knownHelperIDs()
		for _, id := range enabled {
			if known[id] {
				hs.Enabled[id] = true
			}
		}
		if err := hs.Validate(); err != nil {
			return err
		}
		prev := w.app.settings.Helpers
		w.app.settings.Helpers = hs
		if err := store.SaveSettings(w.app.dir, *w.app.settings); err != nil {
			w.app.settings.Helpers = prev
			return err
		}
		// 运行期快照同步：否则改完助手快捷键要等下次应用配置才生效。
		w.app.updateHelpers(hs)
		w.refreshStatus()
		return nil
	})

	bind("api_updatePreference", func(smart, sound, force bool, forceKey string) error {
		forceKey = strings.ToUpper(strings.TrimSpace(forceKey))
		if forceKey != "" {
			if _, err := keys.Parse(forceKey); err != nil {
				return fmt.Errorf("强制站立键无效：%v", err)
			}
		}
		game := w.app.settings.CurrentGame
		prevSmart, prevSound := w.app.settings.SmartPause, w.app.settings.SoundCue
		prevKeys := w.app.settings.GameKeyMap(game)
		restore := func() {
			w.app.settings.SmartPause = prevSmart
			w.app.settings.SoundCue = prevSound
			w.app.settings.SetGameKeyMap(game, prevKeys)
		}

		w.app.settings.SmartPause = smart
		w.app.settings.SoundCue = sound
		gk := w.app.settings.GameKeyMap(game)
		gk.ForceStandEnabled = force
		gk.ForceStandKey = forceKey
		w.app.settings.SetGameKeyMap(game, gk)

		if err := store.SaveSettings(w.app.dir, *w.app.settings); err != nil {
			restore()
			return err
		}
		w.app.updateSmartPause(smart)
		if snap := w.app.st.snapshot(); snap.Engine != nil {
			w.app.setupForceStand(snap.Engine)
		}
		w.refreshStatus()
		return nil
	})

	bind("api_updateHUD", func(enabled bool, mode string, locked bool, gameOnly bool) error {
		mode = strings.TrimSpace(mode)
		if mode != "" && mode != "bar" && mode != "badge" {
			return fmt.Errorf("悬浮窗形态 %q 无效（有效值：bar / badge）", mode)
		}
		hud := w.app.settings.FloatingHUD
		prev := hud
		hud.Enabled = enabled
		hud.Mode = mode
		hud.Locked = locked
		hud.GameOnly = gameOnly
		w.app.settings.FloatingHUD = hud
		if err := store.SaveSettings(w.app.dir, *w.app.settings); err != nil {
			w.app.settings.FloatingHUD = prev
			return err
		}
		if w.app.hud != nil {
			w.app.hud.applySettings()
		}
		w.refreshStatus()
		return nil
	})

	bind("api_resetHUDPos", func() error {
		if w.app.hud == nil {
			return fmt.Errorf("悬浮窗尚未初始化")
		}
		w.app.hud.resetPosition()
		w.refreshStatus()
		return nil
	})

	// 导入分两步：先预览（不产生副作用），玩家在界面确认后再应用。
	bind("api_previewImport", func() (string, error) {
		raw, parsed, preview, err := w.app.previewImportFromClipboard()
		if err != nil {
			w.pendingImport = nil
			return "", err
		}
		w.pendingImport = &pendingImport{raw: raw, parsed: parsed}
		return preview, nil
	})

	bind("api_applyImport", func() error {
		pending := w.pendingImport
		w.pendingImport = nil
		if pending == nil {
			return fmt.Errorf("没有待导入的配置，请重新读取剪贴板")
		}
		if err := w.app.applyImport(pending.raw, pending.parsed); err != nil {
			return err
		}
		w.loadFromState()
		w.refreshStatus()
		return nil
	})

	bind("api_exportClipboard", func() (string, error) {
		code, err := w.buildShareCode()
		if err != nil {
			return "", err
		}
		if err := walk.Clipboard().SetText(code); err != nil {
			return "", fmt.Errorf("写入剪贴板失败：%v", err)
		}
		w.app.log.Printf("[导出] 分享码已复制（%d 字符，%s）", len(code), gameDisplayName(w.app.settings.CurrentGame))
		return code, nil
	})

	bind("api_openLogWindow", func() error {
		w.app.showLogWindow()
		return nil
	})

	bind("api_minimize", func() error {
		if r, _, err := procShowWindow.Call(w.hwnd, uintptr(SW_MINIMIZE)); r == 0 {
			return fmt.Errorf("最小化失败：%v", err)
		}
		return nil
	})

	bind("api_ready", func() error {
		// 前端首帧渲染完成后才会调到这里：留一行日志，便于排查「窗口没出现」类问题。
		w.app.log.Printf("[窗口] 界面首帧就绪，显示主窗口")
		w.showCentered()
		return nil
	})

	bind("api_close", func() error {
		w.hideToTray()
		return nil
	})
}

// hideToTray 把主窗口隐藏到托盘（首次隐藏给一次气泡提示）。
func (w *mainWindow) hideToTray() {
	procShowWindow.Call(w.hwnd, uintptr(SW_HIDE))
	if w.hiddenHintShown {
		return
	}
	w.hiddenHintShown = true
	if w.app.ni != nil {
		_ = w.app.ni.ShowInfo(appName, "已隐藏到托盘，宏会继续运行。\n双击托盘图标可重新打开。")
	}
}

// knownHelperIDs 返回当前注册的助手 ID 集合（界面传来的未知 ID 一律丢弃）。
func knownHelperIDs() map[string]bool {
	ids := map[string]bool{}
	for _, h := range helper.All() {
		ids[h.ID()] = true
	}
	return ids
}

func (w *mainWindow) buildFrontendState() *frontendState {
	snap := w.app.st.snapshot()
	s := &frontendState{
		Version:           version,
		CurrentGame:       w.app.settings.CurrentGame,
		CurrentProfileIdx: w.profileIdx,
		ProfileNames:      make([]string, w.profileCount),
		Profiles:          make([]frontendProfile, w.profileCount),
		ToggleKey:         config.DefaultToggleKey,
		StartMode:         w.startMode,
		SmartPause:        w.app.settings.SmartPause,
		SoundCue:          w.app.settings.SoundCue,
		TargetProcess:     w.app.targetProcessOf(w.app.settings.CurrentGame),
	}

	if w.raw.Toggle != "" {
		s.ToggleKey = w.raw.Toggle
	}
	s.ToggleKeyLabel = keyNameOfToken(s.ToggleKey)

	for t := 0; t < w.profileCount; t++ {
		s.ProfileNames[t] = w.profileNames[t]
		slots := make([]frontendSlot, numSlots)
		for i := 0; i < numSlots; i++ {
			sd := w.profileSlots[t][i]
			slots[i] = frontendSlot{
				Name:        sd.Name,
				Key:         sd.Key,
				KeyLabel:    keyNameOfToken(sd.Key),
				Strategy:    sd.Strategy,
				IntervalMS:  sd.IntervalMS,
				DelayMS:     sd.DelayMS,
				RandomDelay: sd.RandomDelay,
				Explanation: slotExplanation(sd),
			}
		}
		s.Profiles[t] = frontendProfile{
			Name:    w.profileNames[t],
			HK:      w.profileHKs[t],
			HKLabel: keyNameOfToken(w.profileHKs[t]),
			Mode:    w.profileModes[t],
			Slots:   slots,
		}
	}

	// 快速暂停
	qp := w.app.settings.QuickPause
	s.QuickPause.Enabled = qp.Enabled
	s.QuickPause.Trigger = qp.Trigger
	s.QuickPause.MouseKey = qp.MouseKey
	s.QuickPause.Action = qp.Action
	s.QuickPause.DelayMS = qp.DelayMS

	// 城镇助手
	hs := w.app.settings.Helpers
	s.Helpers.Hotkey = hs.Hotkey
	s.Helpers.HotkeyLabel = keyNameOfToken(hs.Hotkey)
	s.Helpers.DelayMS = hs.DelayMS
	s.Helpers.ReforgeTarget = hs.ReforgeTarget
	s.Helpers.DevMode = hs.DevMode
	if hs.Enabled == nil {
		s.Helpers.Enabled = make(map[string]bool)
	} else {
		s.Helpers.Enabled = hs.Enabled
	}

	allH := helper.All()
	s.AllHelpers = make([]helperMeta, len(allH))
	for i, h := range allH {
		s.AllHelpers[i] = helperMeta{ID: h.ID(), Name: h.Name()}
	}

	// 强制站立
	gk := w.app.settings.GameKeyMap(w.app.settings.CurrentGame)
	s.ForceStandEnabled = gk.ForceStandEnabled
	s.ForceStandKey = w.forceStandKeyName()
	s.ForceStandKeyLabel = keyNameOfToken(s.ForceStandKey)

	// 运行状态：状态判定与文案统一由 status.go 提供，主窗口与悬浮窗不可能各说一套。
	foreground := w.app.gameInForeground(snap)
	st := engineStatusOf(snap.Engine, foreground)
	s.Engine = frontendEngineState{
		Configured: snap.Engine != nil,
		Status:     statusClass(st),
		StatusText: engineStatusText(st, w.startMode == 1, s.ToggleKeyLabel),
		HoldMode:   w.startMode == 1,
		Enabled:    snap.Engine != nil && snap.Engine.Enabled(),
		Paused:     snap.Engine != nil && snap.Engine.Paused(),
		Foreground: foreground,
	}

	// 悬浮状态窗
	hudCfg := w.app.settings.FloatingHUD
	s.HUD = frontendHUD{
		Enabled:  hudCfg.Enabled,
		Mode:     hudCfg.Mode,
		Locked:   hudCfg.Locked,
		GameOnly: hudCfg.GameOnly,
		X:        hudCfg.X,
		Y:        hudCfg.Y,
	}

	return s
}

func (w *mainWindow) forceStandKeyName() string {
	gk := w.app.settings.GameKeyMap(w.app.settings.CurrentGame)
	if gk.ForceStandKey != "" {
		return gk.ForceStandKey
	}
	if adapter, ok := games.For(w.app.settings.CurrentGame); ok {
		return adapter.DefaultForceStandKey()
	}
	return "Shift"
}

func (a *app) showMainWindow() {
	if a.win == nil {
		if err := a.createMainWindow(); err != nil {
			a.log.Printf("[窗口] 创建失败：%v", err)
			return
		}
	}
	if a.win.hwnd != 0 {
		a.win.applyDPISize()
		a.win.showCentered()
		procShowWindow.Call(a.win.hwnd, uintptr(SW_SHOW))
		procSetForeground.Call(a.win.hwnd)
		a.win.refreshStatus()
	}
}

func (w *mainWindow) loadFromState() {
	snap := w.app.st.snapshot()
	w.loading = true
	w.pendingImport = nil
	defer func() { w.loading = false }()

	if snap.Raw != nil {
		w.raw = *snap.Raw
	} else {
		w.raw = defaultConfig()
		w.raw.Game = w.app.settings.CurrentGame
	}

	w.startMode = snap.StartMode

	// 方案数量以存档为准（协议保证 1–4）：老存档里的 4 个方案原样保留，新存档只有 1 个。
	w.profileCount = len(w.raw.Profiles)
	if w.profileCount < 1 {
		w.profileCount = 1
	}
	if w.profileCount > maxProfiles {
		w.profileCount = maxProfiles
	}
	for i := 0; i < w.profileCount; i++ {
		var p config.RawProfile
		if i < len(w.raw.Profiles) {
			p = w.raw.Profiles[i]
		}
		w.profileSlots[i] = profileToSlots(p)
		w.profileNames[i] = p.Name
		if w.profileNames[i] == "" {
			w.profileNames[i] = fmt.Sprintf("配置 %d", i+1)
		}
		w.profileHKs[i] = p.HK
		w.profileModes[i] = p.Mode
	}

	w.profileIdx = 0
	if snap.Engine != nil {
		w.profileIdx = snap.Engine.ProfileIndex()
	}
	if w.profileIdx < 0 || w.profileIdx >= w.profileCount {
		w.profileIdx = 0
	}

	w.refreshStatus()
}

func (w *mainWindow) refreshStatus() {
	if w.wv == nil {
		return
	}
	// 方案序号以引擎为准：托盘菜单与游戏内切换键都只改引擎、不经过界面。
	// 这里对齐一次，避免界面停在旧方案、玩家接着编辑却写进另一个方案。
	if snap := w.app.st.snapshot(); snap.Engine != nil {
		if idx := snap.Engine.ProfileIndex(); idx >= 0 && idx < w.profileCount {
			w.profileIdx = idx
		}
	}
	st := w.buildFrontendState()
	b, err := json.Marshal(st)
	if err != nil {
		w.app.log.Printf("[界面] 序列化状态失败：%v", err)
		return
	}
	// 状态以「字符串字面量 + JSON.parse」的方式注入，避免把数据直接拼进 JS 源码。
	quoted, err := json.Marshal(string(b))
	if err != nil {
		return
	}
	w.wv.Dispatch(func() {
		w.wv.Eval("if(window.onStateUpdate){window.onStateUpdate(JSON.parse(" + string(quoted) + "));}")
	})
}

// saveToState 把界面内存里的方案提交为配置：协议校验 → 重建引擎并继承运行态 → 落盘。
// 只提交 profileCount 个方案（玩家实际拥有的），校验不通过时返回错误（配置与磁盘都不改），
// 由调用方回滚内存，见 commitOrRollback。
func (w *mainWindow) saveToState() error {
	if w.profileCount < 1 {
		w.profileCount = 1
	}
	profs := make([]config.RawProfile, w.profileCount)
	for i := 0; i < w.profileCount; i++ {
		name := w.profileNames[i]
		if name == "" {
			name = fmt.Sprintf("配置 %d", i+1)
		}
		profs[i] = slotsToProfile(w.profileSlots[i], name, w.profileHKs[i], w.profileModes[i])
	}

	w.raw.Version = config.Version
	w.raw.Game = w.app.settings.CurrentGame
	w.raw.Profiles = profs
	if w.raw.Toggle == "" {
		w.raw.Toggle = config.DefaultToggleKey
	}

	raw := w.raw
	parsed, err := config.Resolve(raw)
	if err != nil {
		return err
	}
	vks := w.slotVKs()
	return w.app.applyConfig(&raw, parsed, applyOptions{
		ProfileIdx: w.profileIdx,
		StartMode:  w.startMode,
		SlotVKs:    &vks,
		// 界面自己就是槽位表的真值源，提交后不要再从存档反推（会打乱槽位顺序）。
		SkipUIReload: true,
	})
}

// commitOrRollback 提交一次界面编辑；失败时把内存恢复成编辑前的样子并把错误交给前端。
func (w *mainWindow) commitOrRollback(restore func()) error {
	if err := w.saveToState(); err != nil {
		if restore != nil {
			restore()
		}
		return err
	}
	w.refreshStatus()
	return nil
}

// slotVKs 返回界面槽位键的虚拟键码快照（无法解析的键记为 0），供 Buff 守卫在 tick 线程只读使用。
func (w *mainWindow) slotVKs() [maxProfiles][numSlots]uint16 {
	var out [maxProfiles][numSlots]uint16
	for t := 0; t < w.profileCount && t < maxProfiles; t++ {
		out[t] = slotVKsOf(w.profileSlots[t])
	}
	return out
}

func slotVKsOf(slots [numSlots]skillSlotData) [numSlots]uint16 {
	var out [numSlots]uint16
	for i, s := range slots {
		if vk, err := keys.Parse(s.Key); err == nil {
			out[i] = vk
		}
	}
	return out
}

// slotVKsFromRaw 按存档规则推导槽位键快照（导入分享码等不经过界面槽位表的路径使用）。
func slotVKsFromRaw(raw config.Raw) [maxProfiles][numSlots]uint16 {
	var out [maxProfiles][numSlots]uint16
	for t := 0; t < maxProfiles && t < len(raw.Profiles); t++ {
		out[t] = slotVKsOf(profileToSlots(raw.Profiles[t]))
	}
	return out
}

// buildShareCode 生成当前配置的分享码；不写剪贴板、不弹窗，由调用方决定用途。
func (w *mainWindow) buildShareCode() (string, error) {
	if w.profileCount < 1 {
		w.profileCount = 1
	}
	profs := make([]config.RawProfile, w.profileCount)
	for i := 0; i < w.profileCount; i++ {
		name := w.profileNames[i]
		if name == "" {
			name = fmt.Sprintf("配置 %d", i+1)
		}
		profs[i] = slotsToProfile(w.profileSlots[i], name, w.profileHKs[i], w.profileModes[i])
	}
	w.raw.Profiles = profs
	return config.Encode(w.raw)
}

func profileToSlots(prof config.RawProfile) [numSlots]skillSlotData {
	slots := defaultSlots()
	usedSlots := make([]bool, numSlots)

	for _, r := range prof.Rules {
		kind, targetKey, _ := strings.Cut(r.Action, ":")
		if targetKey == "" {
			targetKey = r.Action
		}

		slotIdx := -1
		for i := 0; i < numSlots; i++ {
			if !usedSlots[i] && (strings.EqualFold(slots[i].DefaultKey, targetKey) || strings.EqualFold(slots[i].Key, targetKey)) {
				slotIdx = i
				break
			}
		}
		if slotIdx == -1 {
			for i := 0; i < numSlots; i++ {
				if !usedSlots[i] && slots[i].Strategy == strategyDisabled {
					slotIdx = i
					break
				}
			}
		}
		if slotIdx == -1 {
			continue
		}

		usedSlots[slotIdx] = true
		slots[slotIdx].Key = targetKey
		if r.IntervalMS > 0 {
			slots[slotIdx].IntervalMS = r.IntervalMS
		}
		slots[slotIdx].DelayMS = r.DelayMS
		slots[slotIdx].RandomDelay = r.RandomDelay

		switch {
		case r.Strategy != config.StrategyDisabled:
			slots[slotIdx].Strategy = r.Strategy
		default:
			switch kind {
			case "hold":
				slots[slotIdx].Strategy = strategyHold
			case "tap":
				if r.IntervalMS == 300 {
					slots[slotIdx].Strategy = strategyBuff
				} else {
					slots[slotIdx].Strategy = strategySpam
				}
			default:
				slots[slotIdx].Strategy = strategySpam
			}
		}
	}

	return slots
}

// normalizeQuickPause 补全快速暂停里「留空表示沿用原值」的字段。
// 时长填 0 在引擎里等于「一直暂停到手动恢复」，不能作为界面留空的解释，因此回退到当前值/默认值。
func normalizeQuickPause(qp, prev store.QuickPauseSettings) store.QuickPauseSettings {
	if qp.DelayMS <= 0 {
		qp.DelayMS = prev.DelayMS
		if qp.DelayMS <= 0 {
			qp.DelayMS = store.DefaultQuickPause().DelayMS
		}
	}
	return qp
}

func clampInterval(v, fallback int) int {
	if v < 20 {
		return fallback
	}
	if v > 600000 {
		return 600000
	}
	return v
}

// clampDelay 把延迟夹取到协议允许的区间（±30s）：界面输入的越界值不再让整份配置保存失败。
func clampDelay(v int) int {
	return clampInt(v, -maxDelayMSUI, maxDelayMSUI, 0)
}

// clampInt 夹取到 [lo, hi]；v 为 0（界面未选/未填）时改用 fallback。
func clampInt(v, lo, hi, fallback int) int {
	if v == 0 && fallback != 0 {
		return fallback
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func slotsToProfile(slots [numSlots]skillSlotData, name string, switchKey string, mode string) config.RawProfile {
	rules := make([]config.RawRule, 0, numSlots)

	for _, s := range slots {
		if s.Strategy == strategyDisabled || s.Key == "" {
			continue
		}

		switch s.Strategy {
		case strategyHold:
			rules = append(rules, config.RawRule{
				Trigger:  "t",
				Action:   "hold:" + s.Key,
				Strategy: config.StrategyHold,
			})
		case strategySpam:
			rule := config.RawRule{
				Trigger:    "t",
				Action:     "tap:" + s.Key,
				IntervalMS: clampInterval(s.IntervalMS, 20),
				Strategy:   config.StrategySpam,
			}
			if s.DelayMS != 0 {
				rule.DelayMS = s.DelayMS
				rule.RandomDelay = s.RandomDelay
			}
			rules = append(rules, rule)
		case strategyBuff:
			rules = append(rules, config.RawRule{
				Trigger:    "t",
				Action:     "tap:" + s.Key,
				IntervalMS: clampInterval(s.IntervalMS, 300),
				Strategy:   config.StrategyBuff,
			})
		}
	}

	// 槽位全部禁用时就是空方案（协议 v4 起合法）：玩家还没填技能，不该顺手替他按一个键。
	return config.RawProfile{
		Name:  name,
		HK:    switchKey,
		Mode:  mode,
		Rules: rules,
	}
}

func slotExplanation(s skillSlotData) string {
	keyText := keyNameOfToken(s.Key)
	switch s.Strategy {
	case 0:
		return "手动释放（不自动触发）"
	case 1:
		return fmt.Sprintf("持续保持按下「%s」（旋风斩/扫射/引导类技能专长）", keyText)
	case 2:
		return fmt.Sprintf("每 %dms 持续连点「%s」", s.IntervalMS, keyText)
	case 3:
		return fmt.Sprintf("智能屏幕找色保持「%s」Buff（发光环未结束绝不按键）", keyText)
	default:
		return ""
	}
}
