package games

import "math"

// standardForceStandKey 是暗黑3/4 共有的「强制站立 / 原地站立」默认键。
const standardForceStandKey = "SHIFT"

// 暗黑破坏神4 的适配器。
//
// Buff 守卫与暗黑3 同源但判据不同：暗黑3 是技能图标外的黄色倒计时环（取绿色分量），
// 暗黑4 是技能图标正上方的一条绿色持续时间条。两者的共同点是「绿色分量显著高于红蓝」，
// 因此都用 G 通道判定，只是采样位置不同。
//
// 坐标基准 1920×1080（实测自游戏内截图）：
//   - 条在 y=879 这一行（1080 高）
//   - 6 条等距，第一条中心 x=877，步长 43.5，条宽约 39
// 换算规则与暗黑3 一致：以窗口高度等比缩放，横坐标相对窗口中线换算。
const (
	d4SlotCount    = 6
	d4RefWidth     = 1920.0
	d4RefHeight    = 1080.0
	d4BuffBarY     = 879.0
	d4BuffBarFirst = 877.0
	d4BuffBarStep  = 43.5

	// 绿色判据：G 足够亮、且明显压过 R 与 B（实测条内平均 RGB 约 66/133/45）。
	d4BuffGreenMin   = 95
	d4BuffGreenOverR = 35
	d4BuffGreenOverB = 35
)

type d4Adapter struct{}

func (d4Adapter) ID() string             { return "d4" }
func (d4Adapter) Name() string           { return "暗黑破坏神4" }
func (d4Adapter) DefaultProcess() string { return "Diablo IV.exe" }
func (d4Adapter) Slots() []SlotDef       { return standardSlots() }

// DefaultForceStandKey 暗黑4 的「原地站立」默认也是左 Shift。
func (d4Adapter) DefaultForceStandKey() string { return standardForceStandKey }

// ForceStandKeys 暗黑4 的左键同样是「移动 + 基础技能」，连续点击前要按住 Shift 才不会走位。
// 与暗黑3 唯一的差别是这里尚未做完整实机回归，先按同一机制启用。
func (d4Adapter) ForceStandKeys() []string { return []string{"LMB"} }

// BuffActive 采样技能槽上方的绿色持续时间条：条还在（还有绿色）就说明 Buff 仍生效。
func (d4Adapter) BuffActive(hwnd uintptr, slotIdx int, px PixelSource) bool {
	if px == nil || slotIdx < 0 || slotIdx >= d4SlotCount {
		return false
	}
	w, h, ox, oy, ok := px.ClientRect(hwnd)
	if !ok || w <= 0 || h <= 0 {
		return false
	}
	scale := float64(h) / d4RefHeight
	// 条随剩余时间从右向左缩短，所以取条中点偏左的三点：只要还有绿色就说明 Buff 未断。
	centerX := d4BuffBarFirst + d4BuffBarStep*float64(slotIdx)
	y := int32(math.Round(d4BuffBarY*scale)) + oy
	for _, dx := range []float64{-14, -6, 0} {
		// 横坐标相对窗口中线换算，再换算成屏幕绝对坐标（与暗黑3 的算法一致）。
		x := int32(math.Round(float64(w)/2+(centerX-d4RefWidth/2+dx)*scale)) + ox
		r, g, b, ok := px.Pixel(x, y)
		if !ok {
			continue
		}
		if g >= d4BuffGreenMin && int(g)-int(r) > d4BuffGreenOverR && int(g)-int(b) > d4BuffGreenOverB {
			return true
		}
	}
	return false
}

// standardSlots 是暗黑3/4 通用的 6 技能槽默认定义。
func standardSlots() []SlotDef {
	return []SlotDef{
		{Name: "技能一：", DefaultKey: "1"},
		{Name: "技能二：", DefaultKey: "2"},
		{Name: "技能三：", DefaultKey: "3"},
		{Name: "技能四：", DefaultKey: "4"},
		{Name: "左键技能：", DefaultKey: "LMB"},
		{Name: "右键技能：", DefaultKey: "RMB"},
	}
}
