package helper

import (
	"testing"
	"time"

	"github.com/imsxx/diablo-lazy-macro/internal/games"
)

// kanaiUpgradeColors 是一组能让界面判定为「卡奈魔盒 · 升级页」的颜色（英文客户端坐标）。
func kanaiUpgradeColors() map[[2]int]games.D3Color {
	return map[[2]int]games.D3Color{
		{353, 85}:  {R: 20, G: 20, B: 20},
		{278, 147}: {R: 150, G: 10, B: 10},
		{330, 140}: {R: 30, G: 60, B: 62},
		{799, 406}: {R: 220, G: 200, B: 180},
		{795, 592}: {R: 220, G: 200, B: 10},
	}
}

// 两个助手分开注册：一个只认升级页，一个只认转化页。
func TestKanaiHelpersRequireTheirOwnPage(t *testing.T) {
	env, m := newEnv(nil, 2800, 800)
	if err := (D3KanaiUpgrade{}).Run(env); err == nil {
		t.Fatal("不在升级界面时应报错")
	}
	if err := (D3KanaiConvert{}).Run(env); err == nil {
		t.Fatal("不在转化界面时应报错")
	}
	if len(m.taps) != 0 || len(m.moves) != 0 {
		t.Fatalf("不应发送任何输入，实际 taps=%v moves=%v", m.taps, m.moves)
	}
}

func TestKanaiUpgradeIteratesBagAndRestoresMouse(t *testing.T) {
	env, m := newEnv(kanaiUpgradeColors(), 2800, 800)
	env.Delay = time.Millisecond
	if err := (D3KanaiUpgrade{}).Run(env); err != nil {
		t.Fatalf("升级失败：%v", err)
	}
	// 每格 = 1 次右键放进魔盒 + 4 次左键（材料/转化/清空魔盒），共遍历 60 格
	if want := d3BagSlotCount * 5; len(m.taps) != want {
		t.Fatalf("输入次数 = %d，want %d", len(m.taps), want)
	}
	// 全程挪动光标，收尾必须还给玩家
	if len(m.moves) == 0 || m.moves[len(m.moves)-1] != [2]int32{2800, 800} {
		t.Errorf("收尾未还原鼠标位置：%v", m.moves[len(m.moves)-1])
	}
}

func TestKanaiHelpersRegisteredSeparately(t *testing.T) {
	ids := map[string]bool{}
	for _, h := range All() {
		ids[h.ID()] = true
	}
	if !ids["d3.kanai.upgrade"] {
		t.Error("升级助手未注册")
	}
	if !ids["d3.kanai.convert"] {
		t.Error("转化助手未注册")
	}
	if ids["d3.kanai.upgrade_convert"] {
		t.Error("旧的合并助手不应再注册")
	}
}
