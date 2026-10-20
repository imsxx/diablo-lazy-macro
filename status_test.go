//go:build windows

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/imsxx/diablo-lazy-macro/internal/config"
	"github.com/imsxx/diablo-lazy-macro/internal/engine"
)

type statusSender struct{}

func (statusSender) Tap(uint16) error     { return nil }
func (statusSender) KeyDown(uint16) error { return nil }
func (statusSender) KeyUp(uint16) error   { return nil }

type statusKeys struct{}

func (statusKeys) Pressed(uint16) bool { return false }

func newStatusEngine(t *testing.T) *engine.Engine {
	t.Helper()
	eng, err := engine.New(statusSender{}, statusKeys{}, &config.Parsed{
		Profiles: []config.ParsedProfile{{Rules: []config.ParsedRule{{
			Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 250,
		}}}},
	}, func(error) {})
	if err != nil {
		t.Fatalf("构造引擎失败：%v", err)
	}
	return eng
}

// 主窗口底栏与悬浮窗共用同一套状态判定：优先级 休眠 > 暂停 > 运行 > 就绪。
func TestEngineStatusOf(t *testing.T) {
	eng := newStatusEngine(t)

	if got := engineStatusOf(nil, true); got != statusDormant {
		t.Errorf("没有引擎时应为休眠，实际 %d", got)
	}
	if got := engineStatusOf(eng, false); got != statusDormant {
		t.Errorf("游戏不在前台时应为休眠，实际 %d", got)
	}
	// engine.New 之后总开关默认关闭（由应用层显式打开），先打开再断言「运行中」。
	eng.SetEnabled(true)
	if got := engineStatusOf(eng, true); got != statusRunning {
		t.Errorf("已启用且在前台时应为运行中，实际 %d", got)
	}
	eng.SetEnabled(false)
	if got := engineStatusOf(eng, true); got != statusReady {
		t.Errorf("已停止时应为就绪，实际 %d", got)
	}
	eng.SetEnabled(true)
	eng.QuickPause(time.Now(), time.Second)
	if got := engineStatusOf(eng, true); got != statusPaused {
		t.Errorf("快速暂停中应为已暂停，实际 %d", got)
	}
	// 暂停态优先于运行态，且不因为启用而丢失
	if got := engineStatusOf(eng, false); got != statusDormant {
		t.Errorf("不在前台时以休眠优先，实际 %d", got)
	}
}

func TestEngineStatusTextCoversPause(t *testing.T) {
	// 「已暂停」曾经是不可达分支：这里锁定暂停态必须有可见文案。
	paused := engineStatusText(statusPaused, false, "侧键1")
	if !strings.Contains(paused, "已暂停") {
		t.Errorf("暂停文案缺失：%q", paused)
	}
	if !strings.Contains(paused, "侧键1") {
		t.Errorf("暂停文案应带上启停键名：%q", paused)
	}

	if got := engineStatusText(statusRunning, false, "F5"); !strings.Contains(got, "前台生效") {
		t.Errorf("运行文案应说明前台生效：%q", got)
	}
	if got := engineStatusText(statusReady, false, "F5"); !strings.Contains(got, "已停止") {
		t.Errorf("停止文案应说明已停止：%q", got)
	}
	// 按住生效模式没有「启停」动作，文案不能再说「按 X 停止」
	hold := engineStatusText(statusReady, true, "F5")
	if strings.Contains(hold, "停止") {
		t.Errorf("按住生效模式不应出现启停描述：%q", hold)
	}
	if !strings.Contains(hold, "按住") {
		t.Errorf("按住生效模式应说明按住触发：%q", hold)
	}
}

func TestStatusClassMapping(t *testing.T) {
	cases := map[engineStatus]string{
		statusReady:   "ready",
		statusRunning: "running",
		statusPaused:  "paused",
		statusDormant: "dormant",
	}
	for st, want := range cases {
		if got := statusClass(st); got != want {
			t.Errorf("statusClass(%d) = %q，期望 %q", st, got, want)
		}
		if got := engineStatusShort(st); got == "" {
			t.Errorf("statusClass %q 的短标签为空", want)
		}
	}
}
