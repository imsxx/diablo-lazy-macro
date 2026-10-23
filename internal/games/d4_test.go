package games

import (
	"testing"
	"time"
)

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

func (s stubD4Band) Rect(x, y, w, h int32) ([]byte, bool) {
	out := make([]byte, w*h*3)
	for row := int32(0); row < h; row++ {
		for i := int32(0); i < w; i++ {
			p := (row*w + i) * 3
			px, py := x+i, y+row
			if py >= s.barY-1 && py <= s.barY+1 && px >= s.barX0 && px < s.barX1 {
				out[p], out[p+1], out[p+2] = 66, 133, 45
			} else {
				out[p], out[p+1], out[p+2] = 40, 38, 36
			}
		}
	}
	return out, true
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

// clearD4Cache 清掉全局标定缓存与重试冷却，避免用例之间互相影响。
func clearD4Cache() {
	d4BarMu.Lock()
	d4BarCache = map[[2]int32]d4BuffBarLayout{}
	d4BarCalibrating = false
	d4BarNextTry = time.Time{}
	d4BarAttempts = 0
	d4BarMu.Unlock()
}

// 标定只用「条宽 + 中轴对称」，给任意一段绿条就能推出 6 段位置。
func TestD4CalibrateFromSingleBar(t *testing.T) {
	clearD4Cache()
	// 1920×1080：绿条放在第 1 段该在的位置（中心 = 960 - 2.5*43.5 ≈ 851，宽 39）。
	px := stubD4Band{w: 1920, h: 1080, barY: 913, barX0: 832, barX1: 871}
	layout, ok := calibrateD4Bar(1920, 1080, 0, 0, px)
	if !ok {
		t.Fatal("应当能标定成功")
	}
	if layout.x0[0] > 845 || layout.x1[0] < 858 {
		t.Fatalf("第 1 段区间不合理：%d..%d", layout.x0[0], layout.x1[0])
	}
	if layout.y < 900 || layout.y > 925 {
		t.Fatalf("条所在行不合理：%d", layout.y)
	}
}

// 换尺寸（非 16:9）也能标定：只依赖「底部居中」。
func TestD4CalibrateOnDifferentWindowSize(t *testing.T) {
	clearD4Cache()
	px := stubD4Band{w: 1717, h: 1053, barY: 890, barX0: 722, barX1: 757}
	if _, ok := calibrateD4Bar(1717, 1053, 0, 0, px); !ok {
		t.Fatal("非 16:9 窗口下也应能标定")
	}
}

// 扫不到绿条：标定失败，守卫不启用（不会误拦）。
func TestD4CalibrateFailsWithoutBar(t *testing.T) {
	clearD4Cache()
	px := stubD4Band{w: 1600, h: 900, barY: -1, barX0: 0, barX1: 0}
	if _, ok := calibrateD4Bar(1600, 900, 0, 0, px); ok {
		t.Fatal("没有绿条时不应标定成功")
	}
}

// enableGuard 临时打开守卫（默认关闭，坐标还没在实机核对）。
func enableGuard(t *testing.T) {
	t.Helper()
	old := d4BuffGuardReady
	d4BuffGuardReady = true
	t.Cleanup(func() { d4BuffGuardReady = old })
}

// 命中缓存时按剩余比例判定：满条让路、条消失补按。
func TestD4BuffActiveUsesCachedLayout(t *testing.T) {
	enableGuard(t)
	clearD4Cache()
	const w, h = 1920, 1080
	px := stubD4Band{w: w, h: h, barY: 913, barX0: 832, barX1: 871}

	// 手动写入缓存，模拟「标定已完成」。
	layout := d4BuffBarLayout{y: 913}
	step := 39 * d4StepPerBar
	for i := 0; i < d4SlotCount; i++ {
		center := float64(w)/2 + (float64(i)-2.5)*step
		layout.x0[i] = int32(center - 19)
		layout.x1[i] = int32(center + 19)
	}
	d4BarMu.Lock()
	d4BarCache[[2]int32{w, h}] = layout
	d4BarMu.Unlock()

	if !(d4Adapter{}).BuffActive(1, 0, px) {
		t.Fatal("第 1 槽有条时应判为生效")
	}
	for _, i := range []int{1, 2, 3, 4, 5} {
		if (d4Adapter{}).BuffActive(1, i, px) {
			t.Errorf("第 %d 槽没有绿条，不该判为生效", i+1)
		}
	}
}

// 未标定时不得在调用线程里抓屏：d4Layout 必须立即返回 false 并转为后台标定。
func TestD4LayoutNeverBlocks(t *testing.T) {
	clearD4Cache()
	px := stubD4Band{w: 1920, h: 1080, barY: 913, barX0: 832, barX1: 871}
	start := time.Now()
	if _, ok := d4Layout(1920, 1080, 0, 0, px); ok {
		t.Fatal("第一次调用应返回未标定（标定在后台进行）")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Fatalf("d4Layout 不应在调用线程抓屏，实际耗时 %v", elapsed)
	}

	// 后台标定完成后，再次调用应命中。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := d4Layout(1920, 1080, 0, 0, px); ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("后台标定没有在 2 秒内完成")
}
