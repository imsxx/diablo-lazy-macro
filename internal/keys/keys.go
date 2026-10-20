// Package keys 定义分享码协议允许的键名白名单，以及键名与 Windows 虚拟键码的互转。
// 协议层实现：不依赖任何平台包，供分享码解析（internal/config）与执行层共用。
package keys

import (
	"fmt"
	"strings"
)

// 鼠标键与左右修饰键沿用 Windows 虚拟键码编号。
const (
	VKLeftMouse   = 0x01
	VKRightMouse  = 0x02
	VKMiddleMouse = 0x04
	VKXButton1    = 0x05 // 鼠标后退侧键
	VKXButton2    = 0x06 // 鼠标前进侧键

	VKTab   = 0x09
	VKEnter = 0x0D

	VKLeftShift  = 0xA0
	VKRightShift = 0xA1
	VKLeftCtrl   = 0xA2
	VKRightCtrl  = 0xA3
	VKLeftAlt    = 0xA4
	VKRightAlt   = 0xA5
)

var named = map[string]uint16{
	"LMB": VKLeftMouse,
	"RMB": VKRightMouse,
	"MMB": VKMiddleMouse,
	"X1":  VKXButton1,
	"X2":  VKXButton2,
	"M4":  VKXButton1,
	"M5":  VKXButton2,

	// 修饰键：无前缀写法等价于左侧键（暗黑3/4 的「强制站立」默认都是左 Shift）。
	"SHIFT":  VKLeftShift,
	"LSHIFT": VKLeftShift,
	"RSHIFT": VKRightShift,
	"CTRL":   VKLeftCtrl,
	"LCTRL":  VKLeftCtrl,
	"RCTRL":  VKRightCtrl,
	"ALT":    VKLeftAlt,
	"LALT":   VKLeftAlt,
	"RALT":   VKRightAlt,

	"TAB":      VKTab,
	"ESC":      0x1B,
	"SPACE":    0x20,
	"CAPSLOCK": 0x14,
	"F1":       0x70,
	"F2":       0x71,
	"F3":       0x72,
	"F4":       0x73,
	"F5":       0x74,
	"F6":       0x75,
	"F7":       0x76,
	"F8":       0x77,
	"F9":       0x78,
	"F10":      0x79,
	"F11":      0x7A,
	"F12":      0x7B,
}

// Parse 把协议键名转成虚拟键码；不合法返回错误。
func Parse(token string) (uint16, error) {
	upper := strings.ToUpper(strings.TrimSpace(token))
	if strings.HasPrefix(upper, "0X") {
		var v uint16
		if _, err := fmt.Sscanf(upper, "0X%x", &v); err != nil || v == 0 || v > 0xFE {
			return 0, fmt.Errorf("无法解析按键 %q", token)
		}
		return v, nil
	}
	if vk, ok := named[upper]; ok {
		return vk, nil
	}
	if len(upper) == 1 {
		c := upper[0]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') {
			return uint16(c), nil
		}
	}
	return 0, fmt.Errorf("不支持的按键 %q（支持 0-9 / A-Z / F1-F12 / TAB / ESC / SPACE / SHIFT / CTRL / ALT 及其左右变体 / LMB / RMB / MMB / X1 / X2 / 0xNN）", token)
}

// Label 把虚拟键码转成可读名称。
func Label(vk uint16) string {
	switch vk {
	case VKLeftMouse:
		return "LMB"
	case VKRightMouse:
		return "RMB"
	case VKMiddleMouse:
		return "MMB"
	case VKXButton1:
		return "X1"
	case VKXButton2:
		return "X2"
	case VKLeftShift:
		return "LSHIFT"
	case VKRightShift:
		return "RSHIFT"
	case VKLeftCtrl:
		return "LCTRL"
	case VKRightCtrl:
		return "RCTRL"
	case VKLeftAlt:
		return "LALT"
	case VKRightAlt:
		return "RALT"
	}
	if vk >= 0x70 && vk <= 0x7B {
		return fmt.Sprintf("F%d", vk-0x6F)
	}
	if (vk >= 0x30 && vk <= 0x39) || (vk >= 0x41 && vk <= 0x5A) {
		return string(rune(vk))
	}
	switch vk {
	case 0x09:
		return "TAB"
	case 0x14:
		return "CAPSLOCK"
	case 0x1B:
		return "ESC"
	case 0x20:
		return "SPACE"
	}
	return fmt.Sprintf("0x%02X", vk)
}

// IsMouse 判断虚拟键码是否为鼠标键。
func IsMouse(vk uint16) bool {
	switch vk {
	case VKLeftMouse, VKRightMouse, VKMiddleMouse, VKXButton1, VKXButton2:
		return true
	}
	return false
}
