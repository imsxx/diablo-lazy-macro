package helper

import (
	"fmt"

	"github.com/imsxx/diablo-lazy-macro/internal/games"
	"github.com/imsxx/diablo-lazy-macro/internal/keys"
)

func init() {
	Register(D3KanaiUpgrade{})
	Register(D3KanaiConvert{})
}

// d3BagSlotCount 是背包格子总数（10 列 x 6 行）。
const d3BagSlotCount = 60

// D3KanaiUpgrade / D3KanaiConvert 对应 D3keyHelper 的两个独立开关
//（extraUpgradeHelperCkbox / extraConvertHelperCkbox）：界面分开勾选，
// 底层共用同一套「把背包逐个送进魔盒」的流程，靠魔盒当前页面区分。
type D3KanaiUpgrade struct{}

func (D3KanaiUpgrade) ID() string   { return "d3.kanai.upgrade" }
func (D3KanaiUpgrade) Name() string { return "卡奈魔盒 · 升级稀有装备" }

func (D3KanaiUpgrade) Run(env *Env) error {
	return runKanaiBatch(env, games.D3KanaiUpgrade, "升级")
}

type D3KanaiConvert struct{}

func (D3KanaiConvert) ID() string   { return "d3.kanai.convert" }
func (D3KanaiConvert) Name() string { return "卡奈魔盒 · 转化材料" }

func (D3KanaiConvert) Run(env *Env) error {
	return runKanaiBatch(env, games.D3KanaiConvert, "材料转化")
}

// runKanaiBatch 是升级/转化共用的批处理：把背包里的装备逐个送进魔盒执行一次。
// 与 D3keyHelper 的 oneButtonUpgradeConvertHelper 等价。
func runKanaiBatch(env *Env, want games.D3KanaiState, label string) error {
	state := games.D3KanaiCubeOpen(env.Lang, env.Sampler())
	if state != want {
		return fmt.Errorf("不在卡奈魔盒的%s界面：请先打开魔盒并切到对应页面", label)
	}
	buttons, ok := games.D3KanaiButtonXY(env.WinH)
	if !ok {
		return fmt.Errorf("无法计算魔盒按钮坐标")
	}

	origX, origY, err := env.MouseRel()
	if err != nil {
		return err
	}
	defer func() { _ = env.MoveTo(origX, origY) }()

	for id := 1; id <= d3BagSlotCount; id++ {
		if env.aborted() {
			return AbortError{Helper: label}
		}
		slot, ok := games.D3InventorySlotXY(id, games.D3GridBag, env.WinW, env.WinH)
		if !ok {
			break
		}
		// 右键把该格装备送进魔盒
		if err := env.MoveTo(slot.CenterX, slot.CenterY); err != nil {
			return err
		}
		env.SleepHalf()
		if err := env.Mouse.Tap(keys.VKRightMouse); err != nil {
			return err
		}
		env.Sleep()

		// 添加材料 → 转化 → 清空魔盒（对应脚本的 k[2] → k[1] → k[4] → k[3]）
		for _, btn := range []games.D3Point{
			buttons.InsertItem, buttons.Transmute, buttons.NextPage, buttons.PrevPage,
		} {
			if err := env.ClickAt(btn.X, btn.Y, keys.VKLeftMouse); err != nil {
				return err
			}
			env.Sleep()
		}
		env.debugf("已处理第 %d 格", id)
	}
	env.logf("卡奈魔盒%s完成：已遍历背包 %d 格", label, d3BagSlotCount)
	return nil
}
