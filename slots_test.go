//go:build windows

package main

import (
	"strings"
	"testing"

	"github.com/imsxx/diablo-lazy-macro/internal/config"
	"github.com/imsxx/diablo-lazy-macro/internal/store"
)

// 新游戏只给 1 个方案：一上来摆 4 个空配置只会让玩家多按几次标签页。
func TestDefaultConfigHasSingleProfile(t *testing.T) {
	cfg := defaultConfig()
	if len(cfg.Profiles) != 1 {
		t.Fatalf("预期 1 个默认方案，实际 %d", len(cfg.Profiles))
	}
	parsed, err := config.Resolve(cfg)
	if err != nil {
		t.Fatalf("默认配置校验失败：%v", err)
	}
	if len(parsed.Profiles) != 1 {
		t.Fatalf("解析后方案数量不为 1：%d", len(parsed.Profiles))
	}
}

// 删除方案后的激活位：既不能越界，也不能让玩家「删了当前配置却还停在原序号」。
func TestActiveAfterDelete(t *testing.T) {
	cases := []struct{ active, idx, want int }{
		{0, 0, 0}, // 删第一个（也是当前）→ 仍是 0
		{1, 0, 0}, // 删前面的 → 当前位前移
		{2, 1, 1},
		{1, 1, 0}, // 删当前 → 落到前一个
		{0, 1, 0}, // 删后面的 → 当前位不动
		{3, 2, 2},
	}
	for _, c := range cases {
		if got := activeAfterDelete(c.active, c.idx); got != c.want {
			t.Errorf("删除第 %d 个（当前第 %d 个）应激活 %d，实际 %d", c.idx, c.active, c.want, got)
		}
	}
}

// 界面「+」新增的空白方案（槽位全部禁用）必须能过协议校验：
// 协议 v4 起空方案合法，落盘时不再兜底任何占位规则（否则玩家没配技能就会乱按键）。
func TestAddedBlankProfileResolves(t *testing.T) {
	blank := defaultSlots()
	for i, s := range blank {
		if s.Strategy != strategyDisabled {
			t.Fatalf("新增方案的槽位 %d 应是禁用状态，实际策略 %d", i+1, s.Strategy)
		}
	}
	added := slotsToProfile(blank, "配置 2", "", config.ModeConcurrent)
	if len(added.Rules) != 0 {
		t.Fatalf("空白方案不应含任何规则，实际 %d 条", len(added.Rules))
	}
	first := slotsToProfile(defaultSlots(), "配置 1", "", config.ModeConcurrent)
	if _, err := config.Resolve(config.Raw{
		Version:  config.Version,
		Profiles: []config.RawProfile{first, added},
	}); err != nil {
		t.Fatalf("新增空白方案后应能通过协议校验：%v", err)
	}
}

func TestSlotConversionStrategies(t *testing.T) {
	slots := defaultSlots()
	// Slot 0: 连点 150ms
	slots[0].Strategy = 2
	slots[0].IntervalMS = 150

	// Slot 1: 按住不放
	slots[1].Strategy = 1

	// Slot 2: 保持Buff
	slots[2].Strategy = 3
	slots[2].IntervalMS = 300

	// Slot 3: 禁用
	slots[3].Strategy = 0

	prof := slotsToProfile(slots, "测试方案", "F9", config.ModeConcurrent)
	if prof.Name != "测试方案" || prof.HK != "F9" {
		t.Fatalf("方案元数据不符：%+v", prof)
	}

	converted := profileToSlots(prof)

	if converted[0].Strategy != 2 || converted[0].IntervalMS != 150 {
		t.Errorf("Slot 0 连点转换不符：Strategy=%d, Interval=%d", converted[0].Strategy, converted[0].IntervalMS)
	}
	if converted[1].Strategy != 1 {
		t.Errorf("Slot 1 按住不放转换不符：Strategy=%d", converted[1].Strategy)
	}
	if converted[2].Strategy != 3 {
		t.Errorf("Slot 2 保持Buff转换不符：Strategy=%d", converted[2].Strategy)
	}
	if converted[3].Strategy != 0 {
		t.Errorf("Slot 3 禁用转换不符：Strategy=%d", converted[3].Strategy)
	}
}

func TestSlotExplanation(t *testing.T) {
	sHold := skillSlotData{Key: "1", Strategy: 1}
	expHold := slotExplanation(sHold)
	if expHold != "持续保持按下「1」（旋风斩/扫射/引导类技能专长）" {
		t.Errorf("按住不放说明不符：%s", expHold)
	}

	sBuff := skillSlotData{Key: "2", Strategy: 3}
	expBuff := slotExplanation(sBuff)
	if expBuff != "智能屏幕找色保持「2」Buff（发光环未结束绝不按键）" {
		t.Errorf("保持Buff说明不符：%s", expBuff)
	}

	sSpam := skillSlotData{Key: "3", Strategy: 2, IntervalMS: 1}
	expSpam := slotExplanation(sSpam)
	if expSpam != "每 1ms 持续连点「3」" {
		t.Errorf("连点 1ms 说明不符：%s", expSpam)
	}

	// 延迟与「±随机」已合并：填了幅度，说明里要直接讲清"随机波动"，而不是让玩家去猜。
	sDelayed := skillSlotData{Key: "3", Strategy: 2, IntervalMS: 250, DelayMS: -40}
	if got := slotExplanation(sDelayed); got != "每 250ms 连点「3」，延迟在最多 40ms 内随机波动" {
		t.Errorf("带延迟的连点说明不符：%s", got)
	}
}

func TestSlotStrategySurvivesRoundTrip(t *testing.T) {
	// 策略必须显式写入配置：否则「保持Buff」在引擎侧会丢失屏幕守卫语义。
	slots := defaultSlots()
	slots[0].Strategy = strategyBuff
	slots[0].IntervalMS = 300

	prof := slotsToProfile(slots, "守卫往返", "", config.ModeConcurrent)
	raw := config.Raw{Version: config.Version, Profiles: []config.RawProfile{prof}}
	parsed, err := config.Resolve(raw)
	if err != nil {
		t.Fatalf("策略往返配置校验失败：%v", err)
	}
	if !parsed.Profiles[0].Rules[0].Buff {
		t.Fatalf("保持Buff 策略未落到协议 Buff 标记：%+v", parsed.Profiles[0].Rules[0])
	}
	back := profileToSlots(prof)
	if back[0].Strategy != strategyBuff {
		t.Fatalf("往返后策略丢失：got %d，want %d", back[0].Strategy, strategyBuff)
	}
}

func TestSlotDelaySurvivesRoundTrip(t *testing.T) {
	// 界面上只有一个「延迟」输入：填了幅度就必须同时写出 d 与 rd（随机波动），并能读回。
	slots := defaultSlots()
	slots[0].Strategy = strategySpam
	slots[0].IntervalMS = 150
	slots[0].DelayMS = -40

	prof := slotsToProfile(slots, "延迟往返", "", config.ModeConcurrent)
	raw := config.Raw{Version: config.Version, Profiles: []config.RawProfile{prof}}
	parsed, err := config.Resolve(raw)
	if err != nil {
		t.Fatalf("延迟配置应当合法：%v", err)
	}
	r := parsed.Profiles[0].Rules[0]
	if r.DelayMS != -40 || !r.RandomDelay || r.Strategy != config.StrategySpam {
		t.Fatalf("延迟写入协议有误（填了幅度必须带随机）：%+v", r)
	}

	got := profileToSlots(prof)[0]
	if got.DelayMS != -40 {
		t.Fatalf("延迟往返后丢失：%+v", got)
	}
}

func TestSlotBuffDropsDelay(t *testing.T) {
	// 延迟只对连点有意义：保持Buff 必须把它裁掉，否则协议会拒绝保存。
	slots := defaultSlots()
	slots[1].Strategy = strategyBuff
	slots[1].IntervalMS = 300
	slots[1].DelayMS = 100

	prof := slotsToProfile(slots, "buff 干净字段", "", config.ModeConcurrent)
	raw := config.Raw{Version: config.Version, Profiles: []config.RawProfile{prof}}
	parsed, err := config.Resolve(raw)
	if err != nil {
		t.Fatalf("保持Buff 配置应当合法（延迟须被裁剪）：%v", err)
	}
	r := parsed.Profiles[0].Rules[0]
	if !r.Buff || r.DelayMS != 0 || r.RandomDelay {
		t.Fatalf("保持Buff 不应携带延迟：%+v", r)
	}
}

func TestSlotNoDelayWritesNoRandom(t *testing.T) {
	// 延迟留空（0）时既不该写 d，也不该写 rd（协议要求 rd 与 d 成对）。
	slots := defaultSlots()
	slots[0].Strategy = strategySpam
	slots[0].IntervalMS = 150
	slots[0].DelayMS = 0

	prof := slotsToProfile(slots, "无延迟", "", config.ModeConcurrent)
	raw := config.Raw{Version: config.Version, Profiles: []config.RawProfile{prof}}
	parsed, err := config.Resolve(raw)
	if err != nil {
		t.Fatalf("无延迟配置应当合法：%v", err)
	}
	if r := parsed.Profiles[0].Rules[0]; r.DelayMS != 0 || r.RandomDelay {
		t.Fatalf("无延迟时不应写出 d/rd：%+v", r)
	}
}

func TestSlotStrategyInferredFromLegacyConfig(t *testing.T) {
	// v1 旧码不含策略字段：按动作类型与间隔推断（tap + 300ms 视为保持Buff）。
	prof := config.RawProfile{Rules: []config.RawRule{
		{Trigger: "t", Action: "hold:RMB"},
		{Trigger: "t", Action: "tap:2", IntervalMS: 300},
		{Trigger: "t", Action: "tap:1", IntervalMS: 100},
	}}
	slots := profileToSlots(prof)
	if slots[5].Strategy != strategyHold {
		t.Errorf("按住不放推断错误：%d", slots[5].Strategy)
	}
	if slots[1].Strategy != strategyBuff {
		t.Errorf("保持Buff 推断错误：%d", slots[1].Strategy)
	}
	if slots[0].Strategy != strategySpam {
		t.Errorf("连点推断错误：%d", slots[0].Strategy)
	}
}

func TestProfileModeRoundTrip(t *testing.T) {
	slots := defaultSlots()
	slots[0].Strategy = strategySpam
	slots[0].IntervalMS = 150

	prof := slotsToProfile(slots, "循环模式", "", config.ModeSequential)
	raw := config.Raw{Version: config.Version, Profiles: []config.RawProfile{prof}}
	parsed, err := config.Resolve(raw)
	if err != nil {
		t.Fatalf("循环模式配置应当合法：%v", err)
	}
	if parsed.Profiles[0].Mode != config.ModeSequential {
		t.Fatalf("循环模式未写入协议：%+v", parsed.Profiles[0])
	}
}

func TestProfileModeDefaultsToConcurrent(t *testing.T) {
	// 缺省（空字符串）归一化为并发：旧配置的既有行为不变。
	prof := config.RawProfile{Rules: []config.RawRule{{Trigger: "t", Action: "tap:1", IntervalMS: 250}}}
	parsed, err := config.Resolve(config.Raw{Version: config.Version, Profiles: []config.RawProfile{prof}})
	if err != nil {
		t.Fatalf("缺省模式应当合法：%v", err)
	}
	if parsed.Profiles[0].Mode != config.ModeConcurrent {
		t.Fatalf("缺省模式应为并发，实际 %q", parsed.Profiles[0].Mode)
	}
}

// 界面侧的夹取与上限必须落在协议允许范围内：
// 越界的界面输入不再被写进配置，也就不会再出现「界面接受了、保存却整体失败」的静默失败。
func TestUIClampLimitsWithinProtocol(t *testing.T) {
	for _, delay := range []int{-maxDelayMSUI, maxDelayMSUI} {
		slots := defaultSlots()
		slots[0].Strategy = strategySpam
		slots[0].IntervalMS = 20
		slots[0].DelayMS = delay
		prof := slotsToProfile(slots, "夹取上限", "", config.ModeConcurrent)
		if _, err := config.Resolve(config.Raw{Version: config.Version, Profiles: []config.RawProfile{prof}}); err != nil {
			t.Fatalf("延迟 %d 应当仍被协议接受：%v", delay, err)
		}
	}

	if got := clampDelay(maxDelayMSUI + 1); got != maxDelayMSUI {
		t.Fatalf("clampDelay 未夹取上限：%d", got)
	}
	if got := clampDelay(-maxDelayMSUI - 1); got != -maxDelayMSUI {
		t.Fatalf("clampDelay 未夹取下限：%d", got)
	}
	if got := clampDelay(0); got != 0 {
		t.Fatalf("clampDelay(0) 应为 0：%d", got)
	}

	if got := clampInterval(1, 300); got != 300 {
		t.Fatalf("间隔低于下限应保持原值：%d", got)
	}
	if got := clampInterval(600001, 300); got != 600000 {
		t.Fatalf("间隔上限未被夹取：%d", got)
	}

	name := strings.Repeat("名", maxNameRunesUI)
	prof := slotsToProfile(defaultSlots(), name, "", config.ModeConcurrent)
	if _, err := config.Resolve(config.Raw{Version: config.Version, Profiles: []config.RawProfile{prof}}); err != nil {
		t.Fatalf("长度 %d 的配置名应当合法（说明界面上限未超过协议）：%v", maxNameRunesUI, err)
	}
	tooLong := strings.Repeat("名", maxNameRunesUI+1)
	prof = slotsToProfile(defaultSlots(), tooLong, "", config.ModeConcurrent)
	if _, err := config.Resolve(config.Raw{Version: config.Version, Profiles: []config.RawProfile{prof}}); err == nil {
		t.Fatal("超出界面上限的配置名应被协议拒绝（说明界面上限落后于协议）")
	}
}

// 快速暂停时长留空（0）在引擎里等于「暂停到手动恢复」，界面留空必须读作「沿用原值」。
func TestNormalizeQuickPauseDelay(t *testing.T) {
	prev := store.QuickPauseSettings{DelayMS: 1500}
	if got := normalizeQuickPause(store.QuickPauseSettings{DelayMS: 0}, prev); got.DelayMS != 1500 {
		t.Errorf("空时长应沿用原值，实际 %d", got.DelayMS)
	}
	if got := normalizeQuickPause(store.QuickPauseSettings{DelayMS: 0}, store.QuickPauseSettings{}); got.DelayMS != store.DefaultQuickPause().DelayMS {
		t.Errorf("原值也缺失时应回落默认值，实际 %d", got.DelayMS)
	}
	if got := normalizeQuickPause(store.QuickPauseSettings{DelayMS: 2500}, prev); got.DelayMS != 2500 {
		t.Errorf("正常时长应原样保留，实际 %d", got.DelayMS)
	}
}

// 槽位键快照供 Buff 守卫在 tick 线程只读使用：必须与协议键名一一对应，非法键记为 0。
func TestSlotVKsSnapshot(t *testing.T) {
	slots := defaultSlots()
	slots[0].Key = "LMB"
	slots[1].Key = "0x0D"
	slots[2].Key = "??"
	vks := slotVKsOf(slots)
	if vks[0] != 0x01 {
		t.Errorf("LMB 应解析为 0x01，实际 0x%02X", vks[0])
	}
	if vks[1] != 0x0D {
		t.Errorf("0x0D 应解析为 0x0D，实际 0x%02X", vks[1])
	}
	if vks[2] != 0 {
		t.Errorf("非法键名应记为 0，实际 0x%02X", vks[2])
	}
	if vks[3] != uint16('4') {
		t.Errorf("默认键「4」应解析为 '4'，实际 0x%02X", vks[3])
	}
}

func TestSlotVKsFromRawFollowsProfiles(t *testing.T) {
	raw := config.Raw{Version: config.Version, Profiles: []config.RawProfile{
		{Name: "配置1", Rules: []config.RawRule{{Trigger: "t", Action: "tap:1", IntervalMS: 250}}},
	}}
	vks := slotVKsFromRaw(raw)
	if vks[0][0] != uint16('1') {
		t.Fatalf("首个方案的首槽位应为 1，实际 0x%02X", vks[0][0])
	}
	if vks[1][0] != 0 {
		t.Fatalf("未提供的方案槽位应为空，实际 0x%02X", vks[1][0])
	}
}
