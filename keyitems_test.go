//go:build windows

package main

import (
	"strings"
	"testing"

	"github.com/imsxx/diablo-lazy-macro/internal/keys"
)

// 界面层的键位输入必须全部落在协议白名单内：浏览器键名 → 协议键名的映射由本包负责，
// 这些用例防止再出现「界面接受了某个键、配置却拒绝保存」的静默失败。

func TestBrowserKeyTokenMapping(t *testing.T) {
	cases := map[string]string{
		"KeyA":        "A",
		"KeyZ":        "Z",
		"Digit1":      "1",
		"Digit0":      "0",
		"F5":          "F5",
		"F12":         "F12",
		"ShiftLeft":   "LSHIFT",
		"ShiftRight":  "RSHIFT",
		"ControlLeft": "LCTRL",
		"AltRight":    "RALT",
		"Space":       "SPACE",
		"Tab":         "TAB",
		"Escape":      "ESC",
		"CapsLock":    "CAPSLOCK",
		"Enter":       "0x0D",
		"Backspace":   "0x08",
		"Delete":      "0x2E",
		"ArrowUp":     "0x26",
		"PageDown":    "0x22",
		"Numpad5":     "0x65",
		"NumpadAdd":   "0x6B",
		"Minus":       "0xBD",
		"Backquote":   "0xC0",
		"Semicolon":   "0xBA",
		"NumpadEnter": "0x0D",
		"ScrollLock":  "0x91",
		"ContextMenu": "0x5D",
		// KeyboardEvent.key 兜底（部分环境没有 code）
		"enter":     "0x0D",
		" ":         "SPACE",
		"ArrowLeft": "0x25",
		"?":         "0xBF",
		"a":         "A",
		"7":         "7",
	}
	for raw, want := range cases {
		got, err := browserKeyToken(raw)
		if err != nil {
			t.Errorf("browserKeyToken(%q) 报错：%v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("browserKeyToken(%q) = %q，期望 %q", raw, got, want)
		}
		// 映射结果必须能被协议解析，否则界面会把非法值写进配置。
		if _, err := keys.Parse(got); err != nil {
			t.Errorf("映射结果 %q（来自 %q）不是合法的协议键名：%v", got, raw, err)
		}
	}
}

func TestBrowserKeyTokenRejectsUnknown(t *testing.T) {
	for _, raw := range []string{"F13", "AudioVolumeUp", "Unidentified", "你好", "NumpadComma"} {
		if _, err := browserKeyToken(raw); err == nil {
			t.Errorf("%q 不应被接受", raw)
		}
	}
}

func TestResolveCapturedKeyPerMode(t *testing.T) {
	// 槽位与启停键允许鼠标键（协议允许鼠标做启停键）
	for _, mode := range []captureMode{captureSlot, captureToggle} {
		if _, err := resolveCapturedKey(mode, "LMB"); err != nil {
			t.Errorf("%s 应当接受鼠标键：%v", mode, err)
		}
	}
	// 方案切换键与强制站立键只能是键盘键（协议禁止鼠标键做切换键）
	for _, mode := range []captureMode{captureProfileHK, captureForceStand, captureHelperKey} {
		_, err := resolveCapturedKey(mode, "X1")
		if err == nil {
			t.Fatalf("%s 不应接受鼠标键", mode)
		}
		if !strings.Contains(err.Error(), "鼠标键") {
			t.Errorf("%s 的拒绝理由应说明不支持鼠标键，实际：%v", mode, err)
		}
	}

	// 清除语义：只有允许清除的目标才接受空输入
	for _, mode := range []captureMode{captureProfileHK, captureForceStand, captureHelperKey} {
		if token, err := resolveCapturedKey(mode, ""); err != nil || token != "" {
			t.Errorf("%s 应当允许清除：token=%q err=%v", mode, token, err)
		}
	}
	if _, err := resolveCapturedKey(captureSlot, ""); err == nil {
		t.Error("槽位不允许清空按键，应当报错")
	}
}

func TestCaptureOptionsMatchRules(t *testing.T) {
	modes := []captureMode{captureSlot, captureToggle, captureProfileHK, captureForceStand, captureHelperKey}
	for _, mode := range modes {
		opts := captureOptionsFor(mode)
		if opts.Title == "" {
			t.Errorf("%s 缺少浮层标题", mode)
		}
		// 选项声明与实际裁决必须一致，否则前端会给出「能点但一定失败」的按钮。
		if _, err := resolveCapturedKey(mode, "RMB"); (err == nil) != opts.MouseAllowed {
			t.Errorf("%s 的 MouseAllowed(%v) 与实际裁决不一致", mode, opts.MouseAllowed)
		}
		if _, err := resolveCapturedKey(mode, ""); (err == nil) != (opts.ClearLabel != "") {
			t.Errorf("%s 的清除选项与实际裁决不一致", mode)
		}
		if opts.DefaultLabel != "" {
			token, err := resolveCapturedKey(mode, opts.DefaultToken)
			if err != nil || token != opts.DefaultToken {
				t.Errorf("%s 的「恢复默认」token %q 不合法：%v", mode, opts.DefaultToken, err)
			}
		}
	}
}

func TestKeyNameCoversProtocolKeys(t *testing.T) {
	cases := map[uint16]string{
		0x01: "鼠标左键",
		0x06: "侧键·前进",
		0x08: "退格",
		0x0D: "回车",
		0x26: "↑",
		0xA0: "LSHIFT",
		0x70: "F1",
		0x31: "1",
	}
	for vk, want := range cases {
		if got := keyName(vk); got != want {
			t.Errorf("keyName(0x%02X) = %q，期望 %q", vk, got, want)
		}
	}
	if got := keyNameOfToken(""); got != "无" {
		t.Errorf("空键名应显示为「无」，实际 %q", got)
	}
	if got := keyNameOfToken("LMB"); got != "鼠标左键" {
		t.Errorf("keyNameOfToken(LMB) = %q", got)
	}
	// 解析失败时宽容显示原值，不阻断界面
	if got := keyNameOfToken("??"); got != "??" {
		t.Errorf("非法键名应原样显示，实际 %q", got)
	}
}
