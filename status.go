//go:build windows

package main

import "github.com/imsxx/diablo-lazy-macro/internal/engine"

// engineStatus 是宏运行状态的唯一模型：主窗口底栏与游戏悬浮窗都从这里取值，
// 避免两处各写一套判断（历史上底栏曾出现「已暂停」分支不可达、与悬浮窗说法不一致的问题）。
type engineStatus int

const (
	statusReady   engineStatus = iota // 就绪：已启用但游戏不在前台，或还没按启停键
	statusRunning                     // 运行中：已启用且游戏在前台
	statusPaused                      // 已暂停：快速暂停 / 智能暂停 / 按住期间暂停
	statusDormant                     // 休眠：目标游戏不在前台
)

// engineStatusOf 按「优先级：休眠 > 暂停 > 运行 > 就绪」判定状态，与悬浮窗原有语义一致。
func engineStatusOf(eng *engine.Engine, foreground bool) engineStatus {
	if eng == nil {
		return statusDormant
	}
	if !foreground {
		return statusDormant
	}
	if eng.Paused() {
		return statusPaused
	}
	if eng.Enabled() {
		return statusRunning
	}
	return statusReady
}

// engineStatusShort 是悬浮窗用的状态文案。
// 刻意写长：悬浮窗是玩家唯一能随时看到的状态来源，「就绪 / 休眠」这类两字标签
// 需要玩家自己去猜含义，反而增加认知负担。
func engineStatusShort(st engineStatus) string {
	switch st {
	case statusRunning:
		return "技能宏运行中"
	case statusPaused:
		return "宏已暂停"
	case statusDormant:
		return "游戏在后台"
	default:
		return "按启停键启动"
	}
}

// engineStatusText 是主窗口底栏用的完整文案。
// startMode == 1（仅按下时 / 按住生效）时没有「启停」动作，文案必须区分开。
func engineStatusText(st engineStatus, holdMode bool, toggleLabel string) string {
	if holdMode {
		switch st {
		case statusRunning:
			return "● 宏运行中（松开 " + toggleLabel + " 即停止）"
		case statusPaused:
			return "⏸ 宏已暂停（快速暂停/智能暂停中）"
		case statusDormant:
			return "· 待命中（按住 " + toggleLabel + " 生效 · 目标游戏不在前台）"
		default:
			return "● 按住生效模式（按住 " + toggleLabel + " 触发）"
		}
	}
	switch st {
	case statusRunning:
		return "● 宏已就绪（前台生效 · 按 " + toggleLabel + " 暂停）"
	case statusPaused:
		return "⏸ 宏已暂停（按 " + toggleLabel + " 恢复）"
	case statusDormant:
		return "· 待命中（进入游戏后自动生效）"
	default:
		return "○ 宏已停止（按 " + toggleLabel + " 启动）"
	}
}

// statusClass 是底栏标签的样式类（对应 web/style.css 的 .status-tag.*）。
func statusClass(st engineStatus) string {
	switch st {
	case statusRunning:
		return "running"
	case statusPaused:
		return "paused"
	case statusDormant:
		return "dormant"
	default:
		return "ready"
	}
}
