package games

import "testing"

// stubD4Pixels 只回答预先埋好的点，其余返回暗色（无 Buff）。
type stubD4Pixels struct {
	w, h    int32
	originX int32
	originY int32
	green   map[[2]int32]bool
}

func (s stubD4Pixels) ClientRect(uintptr) (int32, int32, int32, int32, bool) {
	return s.w, s.h, s.originX, s.originY, true
}

func (s stubD4Pixels) Pixel(x, y int32) (uint8, uint8, uint8, bool) {
	if s.green[[2]int32{x, y}] {
		return 66, 133, 45, true // 实测条内平均色
	}
	return 40, 38, 36, true // 条底色（暗）
}

// 1920×1080 下第 i 个技能条的采样点（与实现同一套换算）。
func d4SampleX(i int, dx float64) int32 {
	center := d4BuffBarFirst + d4BuffBarStep*float64(i)
	return int32(center + dx)
}

func TestD4BuffActiveDetectsGreenBar(t *testing.T) {
	// 第 3 个槽位（索引 2）上方有条：只有它应当判为 Buff 生效。
	green := map[[2]int32]bool{}
	for _, dx := range []float64{-14, -6, 0} {
		green[[2]int32{d4SampleX(2, dx), 879}] = true
	}
	px := stubD4Pixels{w: 1920, h: 1080, green: green}

	if !(d4Adapter{}).BuffActive(1, 2, px) {
		t.Fatal("第 3 槽应有 Buff")
	}
	for _, i := range []int{0, 1, 3, 4, 5} {
		if (d4Adapter{}).BuffActive(1, i, px) {
			t.Errorf("第 %d 槽不应判为有 Buff", i+1)
		}
	}
}

func TestD4BuffActiveIgnoresNonGreen(t *testing.T) {
	// 条的位置上是暗色底：不应当判为 Buff 生效。
	px := stubD4Pixels{w: 1920, h: 1080, green: map[[2]int32]bool{}}
	if (d4Adapter{}).BuffActive(1, 0, px) {
		t.Fatal("没有绿色条时不应判为 Buff 生效")
	}
}

func TestD4BuffActiveScalesWithHeight(t *testing.T) {
	// 2560×1440（scale=1.333…）：条应当在 879*1440/1080 ≈ 1172 这一行。
	scale := 1440.0 / d4RefHeight
	y := int32(879 * scale)
	green := map[[2]int32]bool{}
	for _, dx := range []float64{-14, -6, 0} {
		x := int32(float64(2560)/2 + (d4BuffBarFirst+dx-d4RefWidth/2)*scale)
		green[[2]int32{x, y}] = true
	}
	px := stubD4Pixels{w: 2560, h: 1440, green: green}
	if !(d4Adapter{}).BuffActive(1, 0, px) {
		t.Fatal("1440p 下应按高度等比换算后命中")
	}
}

func TestD4BuffActiveRejectsBadSlot(t *testing.T) {
	px := stubD4Pixels{w: 1920, h: 1080, green: map[[2]int32]bool{}}
	if (d4Adapter{}).BuffActive(1, -1, px) || (d4Adapter{}).BuffActive(1, 6, px) {
		t.Fatal("越界槽位应返回 false")
	}
	if (d4Adapter{}).BuffActive(1, 0, nil) {
		t.Fatal("像素源为 nil 时应返回 false")
	}
}
