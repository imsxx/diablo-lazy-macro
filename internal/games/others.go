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
//
// 缩放规则：实测 2205×1122（玩家随手拉的窗口）下条落在 y=1016，
// 按高度等比算应是 913、按宽度等比算应是 1009 —— 接近后者，说明暗黑4 的 HUD 是
// 按窗口宽度缩放的（与暗黑3 按高度缩放不同，所以这里不能复用 d3 的公式）。
const (
	d4SlotCount    = 6
	d4RefWidth     = 1920.0
	d4BuffBarY     = 879.0
	d4BuffBarFirst = 877.0
	d4BuffBarStep  = 43.5
	d4BuffBarWidth = 39.0

	// 绿色判据：G 足够亮、且明显压过 R 与 B（实测条内平均 RGB 约 66/133/45）。
	d4BuffGreenMin   = 95
	d4BuffGreenOverR = 35
	d4BuffGreenOverB = 35

	// d4BuffKeepRatio 是「还剩多少条才继续让路」：低于该比例就补一次。
	// 取 15% —— 比 D3keyHelper 的 5% 早一些，避免高攻速流派里条跑太快、补按还没落地就断了。
	// 条完全消失（0%）时比例必然低于阈值，所以「断了必补」是这条判据的天然兜底。
	d4BuffKeepRatio = 0.15
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

// BandSource 是 PixelSource 的可选扩展：一次取回一行连续像素（RGB 紧凑排列，长度 = 3*width）。
// 实现了它的像素源会被优先使用——单点 GetPixel 在部分环境要数毫秒，扫一条 Buff 条
// 用单点要几十毫秒，会直接把主循环拖到无响应。
type BandSource interface {
	Band(x, y, width int32) ([]byte, bool)
}

func d4IsGreen(r, g, b uint8) bool {
	return g >= d4BuffGreenMin &&
		int(g)-int(r) > d4BuffGreenOverR &&
		int(g)-int(b) > d4BuffGreenOverB
}

// BuffActive 采样技能槽上方的绿色持续时间条，按「剩余比例」判断是否还该让路：
// 只剩不到 15% 时返回 false（引擎据此补按一次），条整个消失同样返回 false。
// 这样既做到「快结束就续」，也保留「断了必补」的兜底。
func (d4Adapter) BuffActive(hwnd uintptr, slotIdx int, px PixelSource) bool {
	if px == nil || slotIdx < 0 || slotIdx >= d4SlotCount {
		return false
	}
	w, _, ox, oy, ok := px.ClientRect(hwnd)
	if !ok || w <= 0 {
		return false
	}
	scale := float64(w) / d4RefWidth
	centerX := d4BuffBarFirst + d4BuffBarStep*float64(slotIdx)
	barCenter := float64(w)/2 + (centerX-d4RefWidth/2)*scale
	y := int32(math.Round(d4BuffBarY*scale)) + oy
	half := d4BuffBarWidth * scale / 2

	// 优先一次抓整行（一次 BitBlt），拿完整条再算比例。
	if band, ok := px.(BandSource); ok {
		barWidth := int32(math.Round(2*half)) + 1
		left := int32(math.Round(barCenter-half)) + ox
		if data, ok := band.Band(left, y, barWidth); ok {
			green, total := 0, 0
			for i := 0; i+2 < len(data); i += 3 {
				total++
				if d4IsGreen(data[i], data[i+1], data[i+2]) {
					green++
				}
			}
			if total == 0 {
				return false
			}
			return float64(green)/float64(total) >= d4BuffKeepRatio
		}
	}

	// 回退：像素源不支持整行抓取时，按稀疏单点估算（约 7 点）。
	const step = 6.0
	green, total := 0, 0
	for dx := -half; dx <= half; dx += step {
		x := int32(math.Round(barCenter+dx)) + ox
		r, g, b, ok := px.Pixel(x, y)
		if !ok {
			continue
		}
		total++
		if d4IsGreen(r, g, b) {
			green++
		}
	}
	if total == 0 {
		return false
	}
	return float64(green)/float64(total) >= d4BuffKeepRatio
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
