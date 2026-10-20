//go:build windows

package win32

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsWindow                 = user32.NewProc("IsWindow")
	procIsIconic                 = user32.NewProc("IsIconic")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procShowWindow               = user32.NewProc("ShowWindow")
	procBringWindowToTop         = user32.NewProc("BringWindowToTop")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
	procGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")
)

// swRestore 是 ShowWindow 的 SW_RESTORE：恢复被最小化的窗口而不改变其尺寸。
const swRestore = 9

// ForegroundHandle 返回当前前台窗口句柄（0 表示没有）。
func ForegroundHandle() windows.Handle {
	hwnd, _, _ := procGetForegroundWindow.Call()
	return windows.Handle(hwnd)
}

// ActivateWindow 把指定窗口切到前台。
//
// 存在的意义：SendInput 只把输入送进前台窗口，所以标定期间要自动按键就必须先让游戏拿到焦点。
// Windows 有「前台锁定」——后台进程不能随便抢焦点，直接调 SetForegroundWindow 常常失败，
// 因此失败后改用 AttachThreadInput 把本线程的输入队列临时挂到目标线程上再抢一次。
//
// 注意：这是标定专用手段，正式程序不会调用它（正式程序只在游戏已位于前台时才发送）。
func ActivateWindow(hwnd windows.Handle) error {
	if hwnd == 0 {
		return fmt.Errorf("窗口句柄无效")
	}
	if IsForeground(hwnd) {
		return nil
	}
	// 最小化的窗口无法成为前台，先恢复（返回 0 只表示「它之前是隐藏的」，不是错误）。
	_, _, _ = procShowWindow.Call(uintptr(hwnd), swRestore)

	if ret, _, _ := procSetForegroundWindow.Call(uintptr(hwnd)); ret != 0 {
		return nil
	}

	// 前台锁定：把当前线程的输入队列挂到目标窗口所属线程上，再抢一次。
	targetTID, _, _ := procGetWindowThreadProcessId.Call(uintptr(hwnd), 0)
	curTID, _, _ := procGetCurrentThreadId.Call()
	if targetTID != 0 && curTID != 0 && targetTID != curTID {
		if ret, _, _ := procAttachThreadInput.Call(curTID, targetTID, 1); ret != 0 {
			defer procAttachThreadInput.Call(curTID, targetTID, 0)
			_, _, _ = procBringWindowToTop.Call(uintptr(hwnd))
			if ret, _, _ := procSetForegroundWindow.Call(uintptr(hwnd)); ret != 0 {
				return nil
			}
		}
	}
	if IsForeground(hwnd) {
		return nil
	}
	return fmt.Errorf("无法把目标窗口切到前台（被 Windows 前台锁定拒绝）")
}

// WindowRect 是窗口在屏幕上的绝对物理矩形。
type WindowRect struct {
	X, Y, Width, Height int32
}

// FindWindowByProcess 按进程名查找可见的顶层窗口，返回面积最大的那个（游戏主窗口）。
// 与前台无关：游戏被其他窗口挡住、或玩家切到别处干活时，依然能定位到它。
func FindWindowByProcess(process string) (windows.Handle, bool) {
	var best windows.Handle
	bestArea := 0
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if vis, _, _ := procIsWindowVisible.Call(hwnd); vis == 0 {
			return 1 // 继续枚举
		}
		var pid uint32
		_, _, _ = procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == 0 {
			return 1
		}
		name, err := processName(pid)
		if err != nil || !MatchProcess(name, process) {
			return 1
		}
		var rc RECT
		if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc))); ret == 0 {
			return 1
		}
		if area := int(rc.Right-rc.Left) * int(rc.Bottom-rc.Top); area > bestArea {
			bestArea = area
			best = windows.Handle(hwnd)
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(cb, 0)
	return best, best != 0
}

// GetWindowRectOf 返回窗口矩形在屏幕上的绝对物理坐标。
func GetWindowRectOf(hwnd windows.Handle) (WindowRect, bool) {
	var rc RECT
	if ret, _, _ := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc))); ret == 0 {
		return WindowRect{}, false
	}
	return WindowRect{X: rc.Left, Y: rc.Top, Width: rc.Right - rc.Left, Height: rc.Bottom - rc.Top}, true
}

// IsForeground 返回该窗口当前是否位于前台。
func IsForeground(hwnd windows.Handle) bool {
	cur, _, _ := procGetForegroundWindow.Call()
	return cur != 0 && cur == uintptr(hwnd)
}

// ForegroundWindow 描述当前前台窗口及其进程。
type ForegroundWindow struct {
	Handle      windows.Handle
	Title       string
	ClassName   string
	ProcessID   uint32
	ProcessName string
	Elevated    bool
}

// fgProcCache 缓存「前台窗口 → 进程名」。主循环每 5–10ms 就要判断一次前台，
// 而完整解析（标题 / 类名 / 进程名 / 令牌）一次约 40µs，直接每帧调用会白烧 1% 单核。
var fgProcCache struct {
	mu   sync.Mutex
	hwnd uintptr
	name string
}

// ForegroundProcessName 只返回前台窗口的进程名，hwnd 未变时复用缓存，成本约 1µs。
// 需要标题/类名/令牌等完整信息时仍用 GetForegroundWindow。
func ForegroundProcessName() (string, bool) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return "", false
	}
	fgProcCache.mu.Lock()
	defer fgProcCache.mu.Unlock()
	if fgProcCache.hwnd == hwnd && fgProcCache.name != "" {
		return fgProcCache.name, true
	}
	var pid uint32
	_, _, _ = procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return "", false
	}
	name, err := processName(pid)
	if err != nil {
		return "", false
	}
	fgProcCache.hwnd = hwnd
	fgProcCache.name = name
	return name, true
}

// IsWindowAlive 报告句柄是否仍指向一个存在的窗口（游戏重启后旧句柄会失效）。
func IsWindowAlive(hwnd windows.Handle) bool {
	if hwnd == 0 {
		return false
	}
	r, _, _ := procIsWindow.Call(uintptr(hwnd))
	return r != 0
}

// IsWindowMinimized 报告窗口是否最小化：悬浮窗贴在游戏上时，游戏最小化就要跟着收起来。
func IsWindowMinimized(hwnd windows.Handle) bool {
	if hwnd == 0 {
		return false
	}
	r, _, _ := procIsIconic.Call(uintptr(hwnd))
	return r != 0
}

// GetForegroundWindow 返回当前前台窗口信息，用于「只在游戏前台时发送」的保护与真实窗口信息采集。
func GetForegroundWindow() (ForegroundWindow, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ForegroundWindow{}, fmt.Errorf("当前没有前台窗口")
	}
	fg := ForegroundWindow{Handle: windows.Handle(hwnd)}

	title := make([]uint16, 512)
	if n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title))); n > 0 {
		fg.Title = windows.UTF16ToString(title[:n])
	}
	class := make([]uint16, 256)
	if n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class))); n > 0 {
		fg.ClassName = windows.UTF16ToString(class[:n])
	}
	_, _, _ = procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&fg.ProcessID)))
	if fg.ProcessID != 0 {
		if name, err := processName(fg.ProcessID); err == nil {
			fg.ProcessName = name
		}
		if elevated, err := processElevated(fg.ProcessID); err == nil {
			fg.Elevated = elevated
		}
	}
	return fg, nil
}

func processName(pid uint32) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return filepath.Base(windows.UTF16ToString(buf[:size])), nil
}

func processElevated(pid uint32) (bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(handle)
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		return false, err
	}
	defer token.Close()
	var elevation struct{ TokenIsElevated uint32 }
	var returned uint32
	if err := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&elevation)), uint32(unsafe.Sizeof(elevation)), &returned); err != nil {
		return false, err
	}
	return elevation.TokenIsElevated != 0, nil
}

// SelfElevated 返回当前进程是否以管理员权限运行。
func SelfElevated() (bool, error) {
	return processElevated(windows.GetCurrentProcessId())
}

// MatchProcess 按进程名匹配目标（忽略大小写与 .exe 后缀），跨语言客户端稳定。
func MatchProcess(processName, target string) bool {
	name := strings.TrimSuffix(processName, ".exe")
	aim := strings.TrimSuffix(target, ".exe")
	return name != "" && strings.EqualFold(name, aim)
}
