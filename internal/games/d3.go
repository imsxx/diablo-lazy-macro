package games

import "math"

// 暗黑破坏神3 适配器：两款游戏中唯一提供屏幕 Buff 守卫的实现。
//
// 识别方式是公开的 GDI 单点取色（不读内存、不注入进程、不加载驱动）。
// 算法 1:1 复刻 D3keyHelper：技能按钮的黄色倒计时发光环尚未走完时，
// 采样点绿色分量会保持在高位，据此判定 Buff 仍在生效，避免抢按打断。

const (
	// d3SlotCount 是暗黑3技能槽位数量：技能一~四 + 左键技能 + 右键技能。
	d3SlotCount = 6
	// d3BuffGreenThreshold 是发光环判定阈值：采样点绿色分量 >= 该值视为 Buff 仍生效。
	d3BuffGreenThreshold = 95
	// d3BuffSamplePercent 是发光环采样点位置百分比（自右端向左倒数 5%）。
	d3BuffSamplePercent = 0.05
	// 参考分辨率与按钮宽度，用于按窗口高度等比换算采样点。
	d3RefWidth    = 3440.0
	d3RefHeight   = 1440.0
	d3ButtonWidth = 63.0
)

// d3BaseX 是 6 个技能槽在 3440 参考宽度下的基准横坐标（左键/右键位于末两位）。
var d3BaseX = [d3SlotCount]float64{1288, 1377, 1465, 1554, 1647, 1734}

type d3Adapter struct{}

func (d3Adapter) ID() string             { return "d3" }
func (d3Adapter) Name() string           { return "暗黑破坏神3" }
func (d3Adapter) DefaultProcess() string { return "Diablo III64.exe" }
func (d3Adapter) Slots() []SlotDef       { return standardSlots() }

// DefaultForceStandKey 暗黑3 的「强制站立」默认绑定左 Shift。
func (d3Adapter) DefaultForceStandKey() string { return standardForceStandKey }

// ForceStandKeys 暗黑3 的鼠标左键既是移动又是主要技能：点它之前按住强制站立键可避免角色走动。
func (d3Adapter) ForceStandKeys() []string { return []string{"LMB"} }

// BuffActive 采样指定槽位发光环的绿色分量，判断该技能的 Buff 是否仍在生效。
func (d3Adapter) BuffActive(hwnd uintptr, slotIdx int, px PixelSource) bool {
	if px == nil {
		return false
	}
	w, h, ox, oy, ok := px.ClientRect(hwnd)
	if !ok || w <= 0 || h <= 0 {
		return false
	}
	sx, sy, ok := d3BuffSamplePoint(slotIdx, w, h, ox, oy, d3BuffSamplePercent)
	if !ok {
		return false
	}
	_, g, _, ok := px.Pixel(sx, sy)
	if !ok {
		return false
	}
	return g >= d3BuffGreenThreshold
}

// d3BuffSamplePoint 按窗口客户区尺寸与屏幕原点，计算指定槽位发光环采样点的屏幕绝对坐标。
// Y 以 1440 为基准等比缩放；X 以 3440 为基准、相对窗口中线换算（与 D3keyHelper 一致）。
func d3BuffSamplePoint(slotIdx int, winW, winH, originX, originY int32, percent float64) (screenX, screenY int32, ok bool) {
	if slotIdx < 0 || slotIdx >= d3SlotCount {
		return 0, 0, false
	}
	scale := float64(winH) / d3RefHeight
	relY := 1328.0 * scale
	relX := float64(winW)/2.0 - (d3RefWidth/2.0-d3BaseX[slotIdx]-percent*d3ButtonWidth)*scale
	return originX + int32(math.Round(relX)), originY + int32(math.Round(relY)), true
}
