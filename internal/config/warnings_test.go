package config

import "testing"

func TestWarningsDetectsSelfTrigger(t *testing.T) {
	p := &Parsed{
		ToggleVK: 0x70, // F1
		Profiles: []ParsedProfile{{Rules: []ParsedRule{
			{HoldVK: 0x05, Kind: ActionTap, Keys: []uint16{0x31}, IntervalMS: 100}, // 按住 X1 连点 1，没问题
			{Kind: ActionHold, Keys: []uint16{0x05}},                               // 动作按住 X1，与触发键重合
			{Kind: ActionTap, Keys: []uint16{0x70}, IntervalMS: 500},               // 动作按 F1，与启停键重合
			{Kind: ActionTap, Keys: []uint16{0x31}, IntervalMS: 500},               // 键 1 重复作为动作，但一次提示即可
		}}},
	}
	warnings := Warnings(p)
	if len(warnings) != 2 {
		t.Fatalf("期望 2 条警告，实际 %d 条：%v", len(warnings), warnings)
	}
}

func TestWarningsCleanConfig(t *testing.T) {
	p := &Parsed{
		ToggleVK: 0x70,
		Profiles: []ParsedProfile{{Rules: []ParsedRule{
			{HoldVK: 0x05, Kind: ActionTap, Keys: []uint16{0x31}, IntervalMS: 100},
			{Kind: ActionOnce, Keys: []uint16{0x32}},
		}}},
	}
	if warnings := Warnings(p); len(warnings) != 0 {
		t.Fatalf("干净配置不应有警告，实际：%v", warnings)
	}
}

// 方案切换键在 v6 已回收：同一键被多个方案"占用"不再是问题，
// 但「触发键与动作键重合」仍要提示。
func TestWarningsDetectsTriggerCollision(t *testing.T) {
	p := &Parsed{
		ToggleVK: 0x70, // F1
		Profiles: []ParsedProfile{
			{Name: "速刷", Rules: []ParsedRule{
				{Kind: ActionTap, Keys: []uint16{0x70}, IntervalMS: 100}, // 动作键 = 启停键
			}},
		},
	}
	warnings := Warnings(p)
	if len(warnings) != 1 {
		t.Fatalf("期望 1 条警告（触发键与启停键重合），实际 %d 条：%v", len(warnings), warnings)
	}
}
