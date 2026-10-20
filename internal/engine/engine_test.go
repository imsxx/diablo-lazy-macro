package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/imsxx/diablo-lazy-macro/internal/config"
	"github.com/imsxx/diablo-lazy-macro/internal/keys"
)

type fakeSender struct {
	events []string
	fail   bool
}

func (f *fakeSender) record(op string, vk uint16) error {
	if f.fail {
		return fmt.Errorf("模拟发送失败")
	}
	f.events = append(f.events, fmt.Sprintf("%s:%s", op, keys.Label(vk)))
	return nil
}

func (f *fakeSender) Tap(vk uint16) error     { return f.record("tap", vk) }
func (f *fakeSender) KeyDown(vk uint16) error { return f.record("down", vk) }
func (f *fakeSender) KeyUp(vk uint16) error   { return f.record("up", vk) }

type fakeKeys map[uint16]bool

func (f fakeKeys) Pressed(vk uint16) bool { return f[vk] }

var base = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func newTestEngine(t *testing.T, profiles ...[]config.ParsedRule) (*Engine, *fakeSender, fakeKeys, *[]string) {
	t.Helper()
	sender := &fakeSender{}
	state := fakeKeys{}
	var errs []string
	var ps []config.ParsedProfile
	for _, rules := range profiles {
		ps = append(ps, config.ParsedProfile{Rules: rules})
	}
	e, err := New(sender, state, &config.Parsed{ToggleVK: 0x70, Profiles: ps}, func(err error) {
		errs = append(errs, err.Error())
	})
	if err != nil {
		t.Fatalf("New 失败：%v", err)
	}
	return e, sender, state, &errs
}

func expectEvents(t *testing.T, sender *fakeSender, want ...string) {
	t.Helper()
	if got := strings.Join(sender.events, ","); got != strings.Join(want, ",") {
		t.Fatalf("事件序列不符\n实际：%s\n期望：%s", got, strings.Join(want, ","))
	}
}

func TestDisabledSendsNothing(t *testing.T) {
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{{Kind: config.ActionTap, Keys: []uint16{0x34}, IntervalMS: 100}})
	e.Tick(base, true)
	e.Tick(base.Add(time.Second), true)
	expectEvents(t, sender)
}

func TestForegroundGate(t *testing.T) {
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{{Kind: config.ActionTap, Keys: []uint16{0x34}, IntervalMS: 100}})
	e.SetEnabled(true)
	e.Tick(base, false)
	e.Tick(base.Add(time.Second), false)
	expectEvents(t, sender)
	e.Tick(base.Add(2*time.Second), true)
	expectEvents(t, sender, "tap:4")
}

func TestTapFiresOnInterval(t *testing.T) {
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{{Kind: config.ActionTap, Keys: []uint16{0x34}, IntervalMS: 250}})
	e.SetEnabled(true)
	e.Tick(base, true)
	e.Tick(base.Add(100*time.Millisecond), true)
	e.Tick(base.Add(249*time.Millisecond), true)
	e.Tick(base.Add(250*time.Millisecond), true)
	expectEvents(t, sender, "tap:4", "tap:4")
}

func TestHoldTriggerGatesRule(t *testing.T) {
	e, sender, state, _ := newTestEngine(t, []config.ParsedRule{
		{HoldVK: 0x05, Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100},
	})
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender)
	state[0x05] = true
	e.Tick(base.Add(10*time.Millisecond), true)
	expectEvents(t, sender, "tap:1")
	state[0x05] = false
	e.Tick(base.Add(20*time.Millisecond), true)
	e.Tick(base.Add(30*time.Millisecond), true)
	expectEvents(t, sender, "tap:1")
}

func TestOnceFiresPerActivation(t *testing.T) {
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{{Kind: config.ActionOnce, Keys: []uint16{0x31}}})
	e.SetEnabled(true)
	e.Tick(base, true)
	e.Tick(base.Add(time.Second), true)
	expectEvents(t, sender, "tap:1")
	e.SetEnabled(false)
	e.Tick(base.Add(2*time.Second), true)
	e.SetEnabled(true)
	e.Tick(base.Add(3*time.Second), true)
	expectEvents(t, sender, "tap:1", "tap:1")
}

func TestHoldActionPressAndRelease(t *testing.T) {
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{{Kind: config.ActionHold, Keys: []uint16{0x02}}})
	e.SetEnabled(true)
	e.Tick(base, true)
	e.Tick(base.Add(time.Second), true)
	expectEvents(t, sender, "down:RMB")
	e.SetEnabled(false)
	e.Tick(base.Add(2*time.Second), true)
	expectEvents(t, sender, "down:RMB", "up:RMB")
}

func TestHoldReleasedOnForegroundLossAndReleaseAll(t *testing.T) {
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{{Kind: config.ActionHold, Keys: []uint16{0x02}}})
	e.SetEnabled(true)
	e.Tick(base, true)
	e.Tick(base.Add(time.Second), false)
	expectEvents(t, sender, "down:RMB", "up:RMB")
	e.Tick(base.Add(2*time.Second), true)
	e.ReleaseAll()
	e.ReleaseAll()
	expectEvents(t, sender, "down:RMB", "up:RMB", "down:RMB", "up:RMB")
}

func TestSeqCyclesKeys(t *testing.T) {
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{{Kind: config.ActionSeq, Keys: []uint16{0x32, 0x33}, IntervalMS: 100}})
	e.SetEnabled(true)
	e.Tick(base, true)
	e.Tick(base.Add(100*time.Millisecond), true)
	e.Tick(base.Add(200*time.Millisecond), true)
	expectEvents(t, sender, "tap:2", "tap:3", "tap:2")
}

func TestSetProfileReleasesAndSwitches(t *testing.T) {
	p0 := []config.ParsedRule{{Kind: config.ActionHold, Keys: []uint16{0x02}}}
	p1 := []config.ParsedRule{{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100}}
	e, sender, _, _ := newTestEngine(t, p0, p1)
	e.SetEnabled(true)
	e.Tick(base, true)
	if err := e.SetProfile(1); err != nil {
		t.Fatalf("SetProfile 失败：%v", err)
	}
	e.Tick(base.Add(time.Second), true)
	expectEvents(t, sender, "down:RMB", "up:RMB", "tap:1")
	if err := e.SetProfile(9); err == nil {
		t.Fatalf("越界方案序号应当报错")
	}
}

func TestToggleReturnsState(t *testing.T) {
	e, _, _, _ := newTestEngine(t, []config.ParsedRule{{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100}})
	if e.Toggle() != true || e.Enabled() != true {
		t.Fatalf("Toggle 应返回 true")
	}
	if e.Toggle() != false || e.Enabled() != false {
		t.Fatalf("再次 Toggle 应返回 false")
	}
}

func TestSendErrorsReported(t *testing.T) {
	e, sender, _, errs := newTestEngine(t, []config.ParsedRule{{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100}})
	sender.fail = true
	e.SetEnabled(true)
	e.Tick(base, true)
	if len(*errs) != 1 || !strings.Contains((*errs)[0], "模拟发送失败") {
		t.Fatalf("发送失败未被上报：%v", *errs)
	}
}

func TestBuffCheckerSkipsWhenActive(t *testing.T) {
	// 只有声明「保持Buff」策略（Buff: true）的规则才会调用 Buff 检测器。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100, Buff: true},
	})
	e.SetEnabled(true)

	buffActive := true
	e.SetBuffChecker(func(vk uint16) bool {
		return buffActive
	})

	// 初始 Tick：Buff 处于激活状态，应该跳过不发送
	e.Tick(base, true)
	expectEvents(t, sender)

	// 150ms 后：Buff 仍然激活，继续跳过
	e.Tick(base.Add(150*time.Millisecond), true)
	expectEvents(t, sender)

	// Buff 消失
	buffActive = false
	e.Tick(base.Add(300*time.Millisecond), true)
	expectEvents(t, sender, "tap:1")
}


func TestTapDelayPostponesWithinCycle(t *testing.T) {
	// 正延迟：周期起点后等待 100ms 才发送。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 500, DelayMS: 100},
	})
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender)
	e.Tick(base.Add(99*time.Millisecond), true)
	expectEvents(t, sender)
	e.Tick(base.Add(100*time.Millisecond), true)
	expectEvents(t, sender, "tap:1")
}

func TestTapNegativeDelayShiftsToCycleEnd(t *testing.T) {
	// 负延迟：推迟到周期末尾前 100ms（500 - 100 = 400ms 处）发送。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 500, DelayMS: -100},
	})
	e.SetEnabled(true)
	e.Tick(base, true)
	e.Tick(base.Add(399*time.Millisecond), true)
	expectEvents(t, sender)
	e.Tick(base.Add(400*time.Millisecond), true)
	expectEvents(t, sender, "tap:1")
}

func TestTapSmallDelayIsHonored(t *testing.T) {
	// 不再有「20ms 以下视为关闭」的门槛：2ms 就是 2ms，实不实现在于系统与游戏。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 200, DelayMS: 2},
	})
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender)
	e.Tick(base.Add(time.Millisecond), true)
	expectEvents(t, sender)
	e.Tick(base.Add(2*time.Millisecond), true)
	expectEvents(t, sender, "tap:1")
}

func TestTapDelayIntervalUsesInjectedSource(t *testing.T) {
	rules := []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 500, DelayMS: 100, DelayMinMS: 10},
	}

	// 注入上界：区间 [10, 100] 取到 100ms。
	e, sender, _, _ := newTestEngine(t, rules)
	e.randIntn = func(n int) int { return n - 1 }
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender)
	e.Tick(base.Add(99*time.Millisecond), true)
	expectEvents(t, sender)
	e.Tick(base.Add(100*time.Millisecond), true)
	expectEvents(t, sender, "tap:1")

	// 注入下界：同一区间取到 10ms。
	e2, sender2, _, _ := newTestEngine(t, rules)
	e2.randIntn = func(int) int { return 0 }
	e2.SetEnabled(true)
	e2.Tick(base, true)
	e2.Tick(base.Add(9*time.Millisecond), true)
	expectEvents(t, sender2)
	e2.Tick(base.Add(10*time.Millisecond), true)
	expectEvents(t, sender2, "tap:1")
}


// NextDue 是「事件驱动调度」的核心：主循环据此睡到到期时刻，而不是按固定周期轮询。
func TestNextDueReportsEarliestDeadline(t *testing.T) {
	e, _, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 250},
	})
	if got := e.NextDue(base); !got.IsZero() {
		t.Fatalf("未启用时不应要求精确唤醒：%v", got)
	}
	e.SetEnabled(true)
	e.Tick(base, true)
	if got := e.NextDue(base); !got.Equal(base.Add(250 * time.Millisecond)) {
		t.Fatalf("应返回下一次连点时刻（base+250ms），实际 %v", got)
	}
	// 已过期的时刻不该反复唤醒：调用方按检测周期轮询即可，避免 1ms 空转。
	if got := e.NextDue(base.Add(time.Second)); !got.IsZero() {
		t.Fatalf("过期时刻应返回零值，实际 %v", got)
	}
}

func TestNextDueIncludesDelayOffset(t *testing.T) {
	e, _, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 500, DelayMS: 40, DelayMinMS: 40},
	})
	e.SetEnabled(true)
	e.Tick(base, true) // 激活并算出 fireAt = base + 40ms
	if got := e.NextDue(base); !got.Equal(base.Add(40 * time.Millisecond)) {
		t.Fatalf("延迟后的到期时刻不符：%v", got)
	}
}

func TestBuffGuardOnlyAppliesToBuffRules(t *testing.T) {
	// 普通连点规则（未声明保持Buff）不受 Buff 检测器影响。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100},
	})
	e.SetBuffChecker(func(vk uint16) bool { return true })
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender, "tap:1")
}

// newTestEngineWithMode 构造指定触发模式的引擎。
func newTestEngineWithMode(t *testing.T, mode string, rules []config.ParsedRule) (*Engine, *fakeSender, fakeKeys, *[]string) {
	t.Helper()
	sender := &fakeSender{}
	state := fakeKeys{}
	var errs []string
	e, err := New(sender, state, &config.Parsed{
		ToggleVK: 0x70,
		Profiles: []config.ParsedProfile{{Mode: mode, Rules: rules}},
	}, func(err error) { errs = append(errs, err.Error()) })
	if err != nil {
		t.Fatalf("New 失败：%v", err)
	}
	return e, sender, state, &errs
}

func TestSequentialModeCyclesRules(t *testing.T) {
	// 循环模式：按规则顺序一轮一轮按，每条规则触发后等待自己的间隔，一轮走完从头再来。
	e, sender, _, _ := newTestEngineWithMode(t, config.ModeSequential, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100},
		{Kind: config.ActionTap, Keys: []uint16{0x32}, IntervalMS: 200},
	})
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender, "tap:1")
	e.Tick(base.Add(100*time.Millisecond), true)
	expectEvents(t, sender, "tap:1", "tap:2")
	// 技能2 的间隔是 200ms：到 300ms 才轮到技能1（一轮走完，从头继续）
	e.Tick(base.Add(299*time.Millisecond), true)
	expectEvents(t, sender, "tap:1", "tap:2")
	e.Tick(base.Add(300*time.Millisecond), true)
	expectEvents(t, sender, "tap:1", "tap:2", "tap:1")
}

func TestSequentialModeSkipsBuffActiveRule(t *testing.T) {
	// 轮到某技能时其 Buff 仍在生效：跳过它，本轮直接轮到下一条。
	e, sender, _, _ := newTestEngineWithMode(t, config.ModeSequential, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100, Buff: true},
		{Kind: config.ActionTap, Keys: []uint16{0x32}, IntervalMS: 100},
	})
	e.SetBuffChecker(func(vk uint16) bool { return vk == 0x31 })
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender, "tap:2")
}

func TestSequentialModeKeepsHoldIndependent(t *testing.T) {
	// 按住类技能不参与顺序轮：仍按自身语义保持按下，失活时立即释放。
	e, sender, _, _ := newTestEngineWithMode(t, config.ModeSequential, []config.ParsedRule{
		{Kind: config.ActionHold, Keys: []uint16{0x02}},
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100},
	})
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender, "down:RMB", "tap:1")
	e.SetEnabled(false)
	e.Tick(base.Add(50*time.Millisecond), true)
	expectEvents(t, sender, "down:RMB", "tap:1", "up:RMB")
}

func TestSequentialModeRespectsForeground(t *testing.T) {
	e, sender, _, _ := newTestEngineWithMode(t, config.ModeSequential, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100},
	})
	e.SetEnabled(true)
	e.Tick(base, false)
	expectEvents(t, sender)
	e.Tick(base.Add(10*time.Millisecond), true)
	expectEvents(t, sender, "tap:1")
}

func TestForceStandWrapsProtectedKey(t *testing.T) {
	// 启用强制站立后，点击受保护的技能键（暗黑3 左键）会先按住强制站立键、点完立即释放。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x01}, IntervalMS: 100}, // 0x01 = 鼠标左键
	})
	e.SetForceStand(0x01, 0xA0) // 左键 -> LShift
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender, "down:LSHIFT", "tap:LMB", "up:LSHIFT")
}

func TestForceStandSkippedWhenPlayerAlreadyHolds(t *testing.T) {
	// 玩家自己已按住强制站立键时，不重复按下、也不代他释放，直接点击。
	e, sender, state, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x01}, IntervalMS: 100},
	})
	e.SetForceStand(0x01, 0xA0)
	state[0xA0] = true
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender, "tap:LMB")
}

func TestForceStandOnlyAppliesToProtectedKey(t *testing.T) {
	// 只对配置的技能键生效，其余按键照常点击。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100},
	})
	e.SetForceStand(0x01, 0xA0)
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender, "tap:1")
}

func TestForceStandDisabledDoesNothing(t *testing.T) {
	// 未启用（默认）时不包裹任何按键。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x01}, IntervalMS: 100},
	})
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender, "tap:LMB")
}

func TestQuickPauseStopsSendingAndAutoResumes(t *testing.T) {
	// 暂停期间不发送规则按键；到期后自动恢复。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100},
	})
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender, "tap:1")

	e.QuickPause(base.Add(10*time.Millisecond), 200*time.Millisecond, 0)
	if !e.Paused() {
		t.Fatal("应当处于暂停状态")
	}
	e.Tick(base.Add(100*time.Millisecond), true)
	e.Tick(base.Add(150*time.Millisecond), true)
	expectEvents(t, sender, "tap:1") // 暂停期间不再发送

	// 暂停起点 10ms + 200ms = 210ms 到期，自动恢复
	e.Tick(base.Add(210*time.Millisecond), true)
	if e.Paused() {
		t.Fatal("到期后应当自动恢复")
	}
	e.Tick(base.Add(300*time.Millisecond), true)
	expectEvents(t, sender, "tap:1", "tap:1")
}

func TestQuickPauseReleasesHeldKeys(t *testing.T) {
	// 暂停时「按住不放」的技能必须立即释放，否则技能会一直按着。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionHold, Keys: []uint16{0x02}},
	})
	e.SetEnabled(true)
	e.Tick(base, true)
	expectEvents(t, sender, "down:RMB")

	e.QuickPause(base, 0, 0) // 持续暂停
	e.Tick(base.Add(10*time.Millisecond), true)
	expectEvents(t, sender, "down:RMB", "up:RMB")
}

func TestQuickPauseClicksLeftButton(t *testing.T) {
	// 「暂停宏且连点左键」：暂停期间以 50ms 节奏继续点击左键。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100},
	})
	e.SetEnabled(true)
	e.QuickPause(base, 0, 0x01)
	e.Tick(base.Add(10*time.Millisecond), true)
	expectEvents(t, sender, "tap:LMB")
	e.Tick(base.Add(30*time.Millisecond), true)
	expectEvents(t, sender, "tap:LMB") // 未到 50ms
	e.Tick(base.Add(60*time.Millisecond), true)
	expectEvents(t, sender, "tap:LMB", "tap:LMB")
}

func TestQuickPauseResumeReturnsToRules(t *testing.T) {
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100},
	})
	e.SetEnabled(true)
	e.QuickPause(base, 0, 0x01)
	e.Tick(base.Add(10*time.Millisecond), true)
	expectEvents(t, sender, "tap:LMB")

	e.Resume()
	if e.Paused() {
		t.Fatal("Resume 后不应仍处于暂停")
	}
	e.Tick(base.Add(60*time.Millisecond), true)
	expectEvents(t, sender, "tap:LMB", "tap:1") // 恢复后回到规则触发
}

func TestQuickPauseClicksUseForceStand(t *testing.T) {
	// 暂停期间连点左键同样走强制站立包裹（暗黑3 左键既是移动又是主要技能）。
	e, sender, _, _ := newTestEngine(t, []config.ParsedRule{
		{Kind: config.ActionTap, Keys: []uint16{0x31}, IntervalMS: 100},
	})
	e.SetForceStand(0x01, 0xA0)
	e.SetEnabled(true)
	e.QuickPause(base, 0, 0x01)
	e.Tick(base.Add(10*time.Millisecond), true)
	expectEvents(t, sender, "down:LSHIFT", "tap:LMB", "up:LSHIFT")
}
