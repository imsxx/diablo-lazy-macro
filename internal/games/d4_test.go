package games

import "testing"

// stubD4Band 模拟屏幕：只在给定行、给定横向区间返回绿色（其余是暗色底）。
type stubD4Band struct {
	w, h             int32
	originX, originY int32
	barY             int32
	barX0, barX1     int32
}

func (s stubD4Band) ClientRect(uintptr) (int32, int32, int32, int32, bool) {
	return s.w, s.h, s.originX, s.originY, true
}

func (s stubD4Band) Pixel(x, y int32) (uint8, uint8, uint8, bool) {
	if y >= s.barY-1 && y <= s.barY+1 && x >= s.barX0 && x < s.barX1 {
		return 66, 133, 45, true
	}
	return 40, 38, 36, true
}

func (s stubD4Band) Band(x, y, width int32) ([]byte, bool) {
	out := make([]byte, width*3)
	for i := int32(0); i < width; i++ {
		px := x + i
		if y >= s.barY-1 && y <= s.barY+1 && px >= s.barX0 && px < s.barX1 {
			out[i*3], out[i*3+1], out[i*3+2] = 66, 133, 45
		} else {
			out[i*3], out[i*3+1], out[i*3+2] = 40, 38, 36
		}
	}
	return out, true
}

// clearD4Cache 清掉全局标定缓存，避免用例之间互相影响。
func clearD4Cache() {
	d4BarMu.Lock()
	d4BarCache = map[[2]int32]d4BuffBarLayout{}
	d4BarMu.Unlock()
}

// 标定只用「条宽 + 中轴对称」，所以给任意一段绿条都能推出 6 段位置。
func TestD4CalibratesFromSingleBar(t *testing.T) {
	clearD4Cache()
	// 1920×1080：把绿条放在第 1 段该在的位置（中心 = 960 - 2.5*43.5 ≈ 851）。
	px := stubD4Band{w: 1920, h: 1080, barY: 913, barX0: 832, barX1: 871}
	if !(d4Adapter{}).BuffActive(1, 0, px) {
		t.Fatal("按标定结果，第 1 槽应判为 Buff 生效")
	}
	for _, i := range []int{1, 2, 3, 4, 5} {
		if (d4Adapter{}).BuffActive(1, i, px) {
			t.Errorf("第 %d 槽不该判为生效（那里没有绿条）", i+1)
		}
	}
}

// 换个窗口尺寸（非 16:9）也要能标定：只要 HUD 在底部居中即可。
func TestD4CalibratesOnDifferentWindowSize(t *testing.T) {
	clearD4Cache()
	// 1717×1053：条宽按比例缩小到约 35px，位置仍在中线左侧第一段。
	px := stubD4Band{w: 1717, h: 1053, barY: 890, barX0: 722, barX1: 757}
	if !(d4Adapter{}).BuffActive(1, 0, px) {
		t.Fatal("非 16:9 窗口下也应能标定并命中")
	}
}

// 扫不到绿条时不缓存、也不误拦：返回 false，让引擎照常按。
func TestD4NoBarMeansNoGuard(t *testing.T) {
	clearD4Cache()
	px := stubD4Band{w: 1600, h: 900, barY: -1, barX0: 0, barX1: 0}
	for i := 0; i < 6; i++ {
		if (d4Adapter{}).BuffActive(1, i, px) {
			t.Fatalf("没有绿条时第 %d 槽必须返回 false，否则该技能永远不按", i+1)
		}
	}
}

// 缓存按客户区尺寸区分：尺寸变了必须重标，不能沿用旧坐标。
func TestD4CacheIsKeyedBySize(t *testing.T) {
	clearD4Cache()
	small := stubD4Band{w: 1280, h: 720, barY: 600, barX0: 553, barX1: 581}
	if !(d4Adapter{}).BuffActive(1, 0, small) {
		t.Fatal("1280×720 下应能标定")
	}
	large := stubD4Band{w: 2560, h: 1440, barY: 1210, barX0: 1109, barX1: 1162}
	if !(d4Adapter{}).BuffActive(1, 0, large) {
		t.Fatal("2560×1440 下应重新标定并命中")
	}
}
