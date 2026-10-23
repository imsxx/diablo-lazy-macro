package main

import (
	"fmt"

	"github.com/imsxx/diablo-lazy-macro/internal/games"
	"github.com/imsxx/diablo-lazy-macro/internal/win32"
)

// 在真实暗黑4 窗口上跑一遍 Buff 判定：打印每个槽位的结论，判断是否会误拦。
func main() {
	adapter, ok := games.For("d4")
	if !ok {
		fmt.Println("d4 未注册")
		return
	}
	hwnd, found := win32.FindWindowByProcess("Diablo IV.exe")
	if !found {
		fmt.Println("没找到 Diablo IV 窗口")
		return
	}
	w, h, ox, oy, ok := win32.Screen{}.ClientRect(uintptr(hwnd))
	fmt.Printf("客户区 %dx%d 原点 (%d,%d)\n", w, h, ox, oy)
	for i := 0; i < 6; i++ {
		active := adapter.BuffActive(uintptr(hwnd), i, win32.Screen{})
		fmt.Printf("槽位 %d: BuffActive=%v\n", i+1, active)
	}
}
