package games

import "testing"

// stubD4Pixels 只在一行、一段 x 区间内返回绿色（模拟剩余若干长度的 Buff 条），其余是暗色底。
type stubD4Pixels struct {
	w, h             int32
	originX, originY int32
	barY             int32
	barX0, barX1     int32
}

func (s stubD4Pixels) ClientRect(uintptr) (int32, int32, int32, int32, bool) {
	return s.w, s.h, s.originX, s.originY, true
}

func (s stubD4Pixels) Pixel(x, y int32) (uint8, uint8, uint8, bool) {
	if y == s.barY && x >= s.barX0 && x < s.barX1 {
		return 66, 133, 45, true // 实测条内平均色
	}
	return 40, 38, 36, true // 条底色
}

// 1920×1080 基准下第 i 条第的左右端点（与实现同一套常量）。
func d4BarSpan(i int) (int32, int32) {
	center := d4BuffBarFirst + d4BuffBarStep*float64(i)
	return int32(center - d4BuffBarWidth/2), int32(center + d4BuffBarWidth/2)
}

func TestD4BuffKeepsWhileBarMostlyFull(t *testing.T) {
	x0, x1 := d4BarSpan(2)
	px := stubD4Pixels{w: 1920, h: 1080, barY: 879, barX0: x0, barX1: x1}
	if !(d4Adapter{}).BuffActive(1, 2, px) {
		t.Fatal("满条应当继续让路（不补按）")
	}
}

func TestD4BuffRefreshesWhenBarNearlyGone(t *testing.T) {
	x0, x1 := d4BarSpan(0)
	// 只剩左侧 10%：应当补按（返回 false）。
	px := stubD4Pixels{w: 1920, h: 1080, barY: 879, barX0: x0, barX1: x0 + (x1-x0)/10}
	if (d4Adapter{}).BuffActive(1, 0, px) {
		t.Fatal("条只剩 10% 时应当补按")
	}

	// 剩 30%：仍在阈值之上，继续让路。
	px.barX1 = x0 + (x1-x0)*3/10
	if !(d4Adapter{}).BuffActive(1, 0, px) {
		t.Fatal("条剩 30% 时不该补按")
	}
}

func TestD4BuffRefreshesWhenBarGone(t *testing.T) {
	// 条完全消失（0%）也要补按：这就是「断了必补」的兜底。
	px := stubD4Pixels{w: 1920, h: 1080, barY: 879, barX0: 0, barX1: 0}
	if (d4Adapter{}).BuffActive(1, 0, px) {
		t.Fatal("条消失后必须返回 false，让引擎补按")
	}
}

func TestD4BuffScalesByWidth(t *testing.T) {
	// 2205×1122 实测：条在 y≈1009（按宽度缩放），不是按高度缩放的 913。
	scale := 2205.0 / d4RefWidth
	y := int32(879 * scale)
	center := float64(2205)/2 + (d4BuffBarFirst-d4RefWidth/2)*scale
	half := d4BuffBarWidth * scale / 2
	px := stubD4Pixels{
		w: 2205, h: 1122, barY: y,
		barX0: int32(center-half) - 2, barX1: int32(center+half) + 2,
	}
	if !(d4Adapter{}).BuffActive(1, 0, px) {
		t.Fatal("非 16:9 窗口下应按窗口宽度换算后命中")
	}
}

func TestD4BuffRejectsBadSlot(t *testing.T) {
	px := stubD4Pixels{w: 1920, h: 1080, barY: 879}
	if (d4Adapter{}).BuffActive(1, -1, px) || (d4Adapter{}).BuffActive(1, 6, px) {
		t.Fatal("越界槽位应返回 false")
	}
	if (d4Adapter{}).BuffActive(1, 0, nil) {
		t.Fatal("像素源为 nil 时应返回 false")
	}
}
