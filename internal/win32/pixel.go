//go:build windows

package win32

import (
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	gdi32 = windows.NewLazySystemDLL("gdi32.dll")

	procGetDC          = user32.NewProc("GetDC")
	procReleaseDC      = user32.NewProc("ReleaseDC")
	procGetPixel       = gdi32.NewProc("GetPixel")
	procGetClientRect  = user32.NewProc("GetClientRect")
	procClientToScreen = user32.NewProc("ClientToScreen")
)

type RECT struct {
	Left, Top, Right, Bottom int32
}

type POINT struct {
	X, Y int32
}

// Screen 实现游戏适配层的像素源接口：把公开的 GDI 取色与窗口客户区几何交给适配器使用。
// 不读游戏内存、不注入进程、不加载驱动。
type Screen struct{}

// Rect 一次取回屏幕上一块区域的像素（RGB 紧凑排列，长度 = 3*w*h）。
// 自标定要在一个带状区域里找颜色，逐行抓屏要几十次、每次 5~6ms；
// 整块抓一次只要一次 BitBlt，再在内存里统计，快一个数量级。
func (Screen) Rect(x, y, w, h int32) ([]byte, bool) {
	img, err := CaptureRect(x, y, w, h)
	if err != nil || img == nil {
		return nil, false
	}
	width, height := img.Rect.Dx(), img.Rect.Dy()
	out := make([]byte, width*height*3)
	for i := 0; i < width*height; i++ {
		out[i*3] = img.Pix[i*4]
		out[i*3+1] = img.Pix[i*4+1]
		out[i*3+2] = img.Pix[i*4+2]
	}
	return out, true
}

// Band 一次取回屏幕上一行连续像素（RGB 顺序），供 Buff 条这类「按长度判定」的场景使用。
// 复用 CaptureRect 的 BitBlt + GetDIBits：单点 GetPixel 在部分环境（远程桌面、DWM 合成）
// 每次要数毫秒，扫一条 7 点就是几十毫秒，会直接把主循环拖到无响应；抓整行只要一次。
func (Screen) Band(x, y, width int32) ([]byte, bool) {
	img, err := CaptureRect(x, y, width, 1)
	if err != nil || img == nil || img.Rect.Dx() <= 0 {
		return nil, false
	}
	n := img.Rect.Dx()
	out := make([]byte, n*3)
	for i := 0; i < n; i++ {
		out[i*3] = img.Pix[i*4]
		out[i*3+1] = img.Pix[i*4+1]
		out[i*3+2] = img.Pix[i*4+2]
	}
	return out, true
}

// ClientRect 返回窗口客户区的宽高及左上角在屏幕上的绝对物理坐标。
func (Screen) ClientRect(hwnd uintptr) (w, h, originX, originY int32, ok bool) {
	w, h, originX, originY, err := GetWindowClientSizeAndOrigin(windows.Handle(hwnd))
	if err != nil {
		return 0, 0, 0, 0, false
	}
	return w, h, originX, originY, true
}

// Pixel 返回屏幕绝对物理坐标 (x, y) 处的 RGB 分量。
func (Screen) Pixel(x, y int32) (r, g, b uint8, ok bool) {
	r, g, b, err := GetPixelColor(x, y)
	if err != nil {
		return 0, 0, 0, false
	}
	return r, g, b, true
}

// 屏幕 DC 缓存：Buff 守卫每次判定要取十几个点，每次都 GetDC/ReleaseDC 在部分环境下
// 单次可高达数毫秒（远程桌面、DWM 合成），足以把主循环拖到卡死。桌面 DC 可以长期持有。
var (
	screenDC   uintptr
	screenDCMu sync.Mutex
)

// GetPixelColor 获取屏幕绝对物理坐标 (x, y) 的 RGB 颜色。
// 基于标准 Win32 GDI API；屏幕 DC 复用，失败时丢弃缓存并重建。
func GetPixelColor(x, y int32) (r, g, b uint8, err error) {
	screenDCMu.Lock()
	defer screenDCMu.Unlock()

	if screenDC == 0 {
		hdc, _, _ := procGetDC.Call(0)
		if hdc == 0 {
			return 0, 0, 0, errors.New("GetDC failed")
		}
		screenDC = hdc
	}
	color, _, _ := procGetPixel.Call(screenDC, uintptr(x), uintptr(y))
	if color == 0xFFFFFFFF { // CLR_INVALID
		// 分辨率/显示模式变化后旧 DC 会失效：丢掉缓存，下次重新获取。
		procReleaseDC.Call(0, screenDC)
		screenDC = 0
		return 0, 0, 0, errors.New("GetPixel failed (CLR_INVALID)")
	}
	return uint8(color & 0xFF), uint8((color >> 8) & 0xFF), uint8((color >> 16) & 0xFF), nil
}

// GetWindowClientSizeAndOrigin 返回窗口客户区的宽高及左上角在屏幕上的绝对物理坐标。
func GetWindowClientSizeAndOrigin(hwnd windows.Handle) (w, h, originX, originY int32, err error) {
	var rc RECT
	ret, _, _ := procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc)))
	if ret == 0 {
		return 0, 0, 0, 0, errors.New("GetClientRect failed")
	}
	var pt POINT
	ret, _, _ = procClientToScreen.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))
	if ret == 0 {
		return 0, 0, 0, 0, errors.New("ClientToScreen failed")
	}
	return rc.Right - rc.Left, rc.Bottom - rc.Top, pt.X, pt.Y, nil
}
