//go:build windows

package main

import (
	"fmt"
	"strings"

	"github.com/imsxx/diablo-lazy-macro/internal/config"
	"github.com/imsxx/diablo-lazy-macro/internal/keys"
)

// 本文件是界面层「键位输入」的唯一裁决点：把浏览器键名 / 鼠标键翻译成协议键名，
// 并按捕获目标限定可选范围。规则不复制到前端——前端只把捕获到的原始键名发过来，
// 由这里决定是否接受、以什么名字显示，避免界面自造协议白名单以外的 token。

// captureMode 是按键捕获的目标类型；不同目标允许的键范围不同（协议决定）。
type captureMode string

const (
	captureSlot       captureMode = "slot"       // 技能槽位：键盘键 + 鼠标键
	captureToggle     captureMode = "toggle"     // 宏启停键：键盘键 + 鼠标键（协议允许鼠标键）
	captureForceStand captureMode = "forceStand" // 强制站立键：仅键盘键（要按住修饰键，鼠标键无意义）
	captureHelperKey  captureMode = "helperKey"  // 助手快捷键：仅键盘键，可留空表示不绑定
)

// captureOptions 描述某个捕获目标的可选行为，由 Go 裁决、前端照此渲染浮层。
type captureOptions struct {
	Title        string `json:"title"`
	Hint         string `json:"hint"`
	MouseAllowed bool   `json:"mouseAllowed"` // 是否显示鼠标键快捷按钮
	ClearToken   string `json:"clearToken"`   // 「清除」要提交的 token 与按钮文案；Label 为空表示不提供
	ClearLabel   string `json:"clearLabel"`
	DefaultToken string `json:"defaultToken"` // 「恢复默认」要提交的 token 与按钮文案；Label 为空表示不提供
	DefaultLabel string `json:"defaultLabel"`
}

// captureResult 是一次成功捕获的结果：协议键名 + 界面显示名。
type captureResult struct {
	Token string `json:"token"`
	Label string `json:"label"`
}

func parseCaptureMode(s string) (captureMode, error) {
	switch captureMode(strings.TrimSpace(s)) {
	case captureSlot:
		return captureSlot, nil
	case captureToggle:
		return captureToggle, nil
	case captureForceStand:
		return captureForceStand, nil
	case captureHelperKey:
		return captureHelperKey, nil
	}
	return "", fmt.Errorf("未知的捕获目标 %q", s)
}

func captureOptionsFor(mode captureMode) captureOptions {
	switch mode {
	case captureToggle:
		return captureOptions{
			Title:        "按下宏启停键（键盘或鼠标均可）",
			Hint:         "游戏中按一下即可让宏开始/停止；鼠标侧键最省力。",
			MouseAllowed: true,
			DefaultToken: config.DefaultToggleKey,
			DefaultLabel: "恢复默认（" + keyNameOfToken(config.DefaultToggleKey) + "）",
		}
	case captureForceStand:
		return captureOptions{
			Title:        "按下强制站立键（通常为 Shift）",
			Hint:         "连点左键技能前会先按住它，避免角色走动。",
			ClearToken:   "",
			ClearLabel:   "清除（改用游戏默认键）",
			DefaultToken: "LSHIFT",
			DefaultLabel: "恢复默认（左 Shift）",
		}
	case captureHelperKey:
		return captureOptions{
			Title:      "按下助手快捷键（仅键盘键）",
			Hint:       "游戏中按一下触发城镇助手；运行中再按一下可打断。",
			ClearToken: "",
			ClearLabel: "清除（不绑定快捷键）",
		}
	}
	return captureOptions{
		Title:        "请按下任意键盘按键，或快捷选取鼠标按键",
		Hint:         "与当前释放策略无关的列会自动置灰。",
		MouseAllowed: true,
	}
}

// resolveCapturedKey 校验并规范化一次捕获输入：
// raw 可以是浏览器 KeyboardEvent.code（推荐）、KeyboardEvent.key，或鼠标键 token。
// 空字符串表示「清除」，仅对允许清除的目标有效。
func resolveCapturedKey(mode captureMode, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	opts := captureOptionsFor(mode)

	if raw == "" {
		if opts.ClearLabel == "" {
			return "", fmt.Errorf("没有捕获到按键，请重新按下要绑定的键")
		}
		return "", nil
	}

	if mouse, ok := mouseKeyTokens[strings.ToUpper(raw)]; ok {
		if !opts.MouseAllowed {
			return "", fmt.Errorf("%s 不支持鼠标键（%s），请改用键盘键", modeLabel(mode), keyNameOfToken(mouse))
		}
		return mouse, nil
	}

	// 已经是协议键名（例如浮层里的「恢复默认」按钮直接提交 LSHIFT）时原样接受。
	if upper := strings.ToUpper(raw); upper != "" {
		if _, err := keys.Parse(upper); err == nil {
			return upper, nil
		}
	}

	token, err := browserKeyToken(raw)
	if err != nil {
		return "", err
	}
	// 兜底：最终必须落在协议白名单内，避免把非法键名写进配置。
	if _, err := keys.Parse(token); err != nil {
		return "", fmt.Errorf("不支持的按键 %q：%v", raw, err)
	}
	return token, nil
}

func modeLabel(mode captureMode) string {
	switch mode {
	case captureSlot:
		return "技能槽位"
	case captureToggle:
		return "宏启停键"
	case captureForceStand:
		return "强制站立键"
	case captureHelperKey:
		return "助手快捷键"
	}
	return "该键位"
}

// mouseKeyTokens 是鼠标键的协议 token（大小写不敏感）。
var mouseKeyTokens = map[string]string{
	"LMB": "LMB", "RMB": "RMB", "MMB": "MMB", "X1": "X1", "X2": "X2",
	"M4": "X1", "M5": "X2",
}

// browserCodeTokens 把 KeyboardEvent.code 映射为协议键名。
// 协议白名单只覆盖 0-9 / A-Z / F1-F12 / TAB / ESC / SPACE / 修饰键 / 鼠标键，
// 其余常用键统一用协议支持的 0xNN 写法表达（协议无需变更）。
var browserCodeTokens = map[string]string{
	"KeyA": "A", "KeyB": "B", "KeyC": "C", "KeyD": "D", "KeyE": "E", "KeyF": "F",
	"KeyG": "G", "KeyH": "H", "KeyI": "I", "KeyJ": "J", "KeyK": "K", "KeyL": "L",
	"KeyM": "M", "KeyN": "N", "KeyO": "O", "KeyP": "P", "KeyQ": "Q", "KeyR": "R",
	"KeyS": "S", "KeyT": "T", "KeyU": "U", "KeyV": "V", "KeyW": "W", "KeyX": "X",
	"KeyY": "Y", "KeyZ": "Z",

	"Digit0": "0", "Digit1": "1", "Digit2": "2", "Digit3": "3", "Digit4": "4",
	"Digit5": "5", "Digit6": "6", "Digit7": "7", "Digit8": "8", "Digit9": "9",

	"F1": "F1", "F2": "F2", "F3": "F3", "F4": "F4", "F5": "F5", "F6": "F6",
	"F7": "F7", "F8": "F8", "F9": "F9", "F10": "F10", "F11": "F11", "F12": "F12",

	"ShiftLeft": "LSHIFT", "ShiftRight": "RSHIFT",
	"ControlLeft": "LCTRL", "ControlRight": "RCTRL",
	"AltLeft": "LALT", "AltRight": "RALT",

	"Escape": "ESC", "Tab": "TAB", "Space": "SPACE", "CapsLock": "CAPSLOCK",
	"Enter": "0x0D", "NumpadEnter": "0x0D",
	"Backspace": "0x08", "Delete": "0x2E", "Insert": "0x2D",
	"ArrowLeft": "0x25", "ArrowUp": "0x26", "ArrowRight": "0x27", "ArrowDown": "0x28",
	"Home": "0x24", "End": "0x23", "PageUp": "0x21", "PageDown": "0x22",
	"PrintScreen": "0x2C", "ScrollLock": "0x91", "Pause": "0x13",

	"Minus": "0xBD", "Equal": "0xBB", "BracketLeft": "0xDB", "BracketRight": "0xDD",
	"Backslash": "0xDC", "Semicolon": "0xBA", "Quote": "0xDE", "Backquote": "0xC0",
	"Comma": "0xBC", "Period": "0xBE", "Slash": "0xBF",

	"Numpad0": "0x60", "Numpad1": "0x61", "Numpad2": "0x62", "Numpad3": "0x63",
	"Numpad4": "0x64", "Numpad5": "0x65", "Numpad6": "0x66", "Numpad7": "0x67",
	"Numpad8": "0x68", "Numpad9": "0x69",
	"NumpadMultiply": "0x6A", "NumpadAdd": "0x6B", "NumpadSubtract": "0x6D",
	"NumpadDecimal": "0x6E", "NumpadDivide": "0x6F",

	"MetaLeft": "0x5B", "MetaRight": "0x5C", "ContextMenu": "0x5D",
}

// browserKeyNameTokens 是 KeyboardEvent.key 的兜底映射（部分环境 code 为空或不可用）。
var browserKeyNameTokens = map[string]string{
	" ": "SPACE", "ESC": "ESC", "ESCAPE": "ESC", "TAB": "TAB", "CAPSLOCK": "CAPSLOCK",
	"ENTER": "0x0D", "BACKSPACE": "0x08", "DELETE": "0x2E", "INSERT": "0x2D",
	"ARROWLEFT": "0x25", "ARROWUP": "0x26", "ARROWRIGHT": "0x27", "ARROWDOWN": "0x28",
	"LEFT": "0x25", "UP": "0x26", "RIGHT": "0x27", "DOWN": "0x28",
	"HOME": "0x24", "END": "0x23", "PAGEUP": "0x21", "PAGEDOWN": "0x22",
	"SHIFT": "LSHIFT", "CONTROL": "LCTRL", "CTRL": "LCTRL", "ALT": "LALT", "META": "0x5B",
	"PRINTSCREEN": "0x2C", "SCROLLLOCK": "0x91", "PAUSE": "0x13",
	"-": "0xBD", "_": "0xBD", "=": "0xBB", "+": "0xBB",
	"[": "0xDB", "{": "0xDB", "]": "0xDD", "}": "0xDD",
	"\\": "0xDC", "|": "0xDC", ";": "0xBA", ":": "0xBA", "'": "0xDE", "\"": "0xDE",
	"`": "0xC0", "~": "0xC0", ",": "0xBC", "<": "0xBC", ".": "0xBE", ">": "0xBE",
	"/": "0xBF", "?": "0xBF",
}

// browserKeyToken 把浏览器键名转成协议键名；无法识别时报错（不猜测）。
func browserKeyToken(raw string) (string, error) {
	if token, ok := browserCodeTokens[raw]; ok {
		return token, nil
	}
	upper := strings.ToUpper(raw)
	if token, ok := browserKeyNameTokens[upper]; ok {
		return token, nil
	}
	if token, ok := browserCodeTokens[upper]; ok {
		return token, nil
	}
	if len(upper) == 1 {
		c := upper[0]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') {
			return upper, nil
		}
	}
	return "", fmt.Errorf("不支持的按键 %q：请改用 A-Z、0-9、F1-F12、Tab、Shift/Ctrl/Alt 或鼠标键", raw)
}

// keyName 把虚拟键码转成界面上的中文名；未覆盖的键沿用协议键名。
func keyName(vk uint16) string {
	switch vk {
	case keys.VKLeftMouse:
		return "鼠标左键"
	case keys.VKRightMouse:
		return "鼠标右键"
	case keys.VKMiddleMouse:
		return "鼠标中键"
	// 用前进/后退而不是「侧键1/2」：玩家看到键帽就知道该按哪一个，不用去试。
	case keys.VKXButton1:
		return "侧键·后退"
	case keys.VKXButton2:
		return "侧键·前进"

	case 0x08:
		return "退格"
	case 0x0D:
		return "回车"
	case 0x13:
		return "Pause"
	case 0x2C:
		return "PrintScreen"
	case 0x2D:
		return "插入"
	case 0x2E:
		return "删除"
	case 0x21:
		return "PageUp"
	case 0x22:
		return "PageDown"
	case 0x23:
		return "End"
	case 0x24:
		return "Home"
	case 0x25:
		return "←"
	case 0x26:
		return "↑"
	case 0x27:
		return "→"
	case 0x28:
		return "↓"
	case 0x91:
		return "ScrollLock"
	case 0x5B, 0x5C:
		return "Win"
	case 0x5D:
		return "菜单键"

	case 0xBD:
		return "-"
	case 0xBB:
		return "="
	case 0xDB:
		return "["
	case 0xDD:
		return "]"
	case 0xDC:
		return "\\"
	case 0xBA:
		return ";"
	case 0xDE:
		return "'"
	case 0xC0:
		return "`"
	case 0xBC:
		return ","
	case 0xBE:
		return "."
	case 0xBF:
		return "/"
	}
	if vk >= 0x60 && vk <= 0x69 {
		return fmt.Sprintf("小键盘%d", vk-0x60)
	}
	switch vk {
	case 0x6A:
		return "小键盘*"
	case 0x6B:
		return "小键盘+"
	case 0x6D:
		return "小键盘-"
	case 0x6E:
		return "小键盘."
	case 0x6F:
		return "小键盘/"
	}
	return keys.Label(vk)
}

// keyNameOfToken 把协议键名转成中文名；无法解析时原样返回（宽容显示，不阻断界面）。
func keyNameOfToken(token string) string {
	if strings.TrimSpace(token) == "" {
		return "无"
	}
	vk, err := keys.Parse(token)
	if err != nil {
		return token
	}
	return keyName(vk)
}
