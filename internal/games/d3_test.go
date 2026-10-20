package games

import (
	"math"
	"testing"
)

// fakePixel 是 PixelSource 的测试替身。
type fakePixel struct {
	w, h, ox, oy int32
	rectOK       bool
	pixels       map[[2]int32][3]uint8
}

func (f fakePixel) ClientRect(uintptr) (int32, int32, int32, int32, bool) {
	return f.w, f.h, f.ox, f.oy, f.rectOK
}

func (f fakePixel) Pixel(x, y int32) (uint8, uint8, uint8, bool) {
	if c, ok := f.pixels[[2]int32{x, y}]; ok {
		return c[0], c[1], c[2], true
	}
	return 0, 0, 0, false
}

func TestD3BuffSamplePointMatchesMeasuredBaseline(t *testing.T) {
	// 2026-10-13 实机标定基线（见 docs/d3-buff-calibration.md）：
	// 游戏窗口化运行，客户区 1817x1025。按下技能一后，槽位 1 的采样点
	// 绿色分量由 11 升到 164 并持续上升，证明该点确实落在 Buff 指示上。
	// 这是唯一被逐像素验证过的槽位，固化为回归基线。
	sx, sy, ok := d3BuffSamplePoint(0, 1817, 1025, 0, 0, d3BuffSamplePercent)
	if !ok {
		t.Fatal("应能算出采样点")
	}
	if sx != 603 || sy != 945 {
		t.Fatalf("与实机标定不符：got (%d,%d)，want (603,945)", sx, sy)
	}
}

func TestD3BuffSampleSpacingMatchesGameLayout(t *testing.T) {
	// 槽位 2~6 无法用「按下 Buff 技能」逐个验证（它们未必是 Buff 技能），
	// 因此改用几何交叉验证：D3keyHelper 的基准横坐标是手工测量的，间距并不严格等距
	// （87~93），而实机扫描得到 6 个图标中心间距恒为 63px（客户区 1817x1025）。
	// 只要换算后的采样点间距与实机间距的偏差足够小，采样点就不会滑出 Buff 指示区。
	const (
		winW, winH = 1817, 1025
		measured   = 63.0 // 实机扫描的图标间距
		tolerance  = 5.0  // 允许偏差（像素）；基准数据的固有误差约 ±3px
	)
	var xs [d3SlotCount]int32
	for i := range xs {
		x, _, ok := d3BuffSamplePoint(i, winW, winH, 0, 0, d3BuffSamplePercent)
		if !ok {
			t.Fatalf("槽位 %d 采样点计算失败", i)
		}
		xs[i] = x
	}
	for i := 1; i < d3SlotCount; i++ {
		got := float64(xs[i] - xs[i-1])
		if diff := math.Abs(got - measured); diff > tolerance {
			t.Errorf("槽位 %d→%d 采样点间距 %.0fpx，与实机图标间距 %.0fpx 偏差 %.1fpx（上限 %.0fpx）",
				i, i+1, got, measured, diff, tolerance)
		}
	}
	// 采样点必须落在图标自身宽度内：图标宽 63px，采样点应位于其内侧。
	if span := xs[d3SlotCount-1] - xs[0]; span <= 0 {
		t.Fatalf("采样点横坐标未按槽位递增：%v", xs)
	}
}

func TestD3BuffSamplePointReferenceResolution(t *testing.T) {
	// 3440x1440、原点 (0,0)：scale = 1，槽位 0 的采样点应为
	// X = 1288 + 0.05*63 = 1291.15 -> 1291；Y = 1328。
	sx, sy, ok := d3BuffSamplePoint(0, 3440, 1440, 0, 0, d3BuffSamplePercent)
	if !ok {
		t.Fatal("参考分辨率下应能算出采样点")
	}
	if sx != 1291 || sy != 1328 {
		t.Fatalf("参考分辨率采样点不符：got (%d,%d)，want (1291,1328)", sx, sy)
	}
}

func TestD3BuffSamplePointScalesWithHeight(t *testing.T) {
	// 高度翻倍（1440 -> 2880）：Y 翻倍，X 偏移量随 scale 放大。
	sx, sy, ok := d3BuffSamplePoint(0, 6880, 2880, 0, 0, d3BuffSamplePercent)
	if !ok {
		t.Fatal("缩放后应能算出采样点")
	}
	if sy != 2656 {
		t.Errorf("Y 应随高度等比缩放：got %d，want 2656", sy)
	}
	// relX = 6880/2 - (1720-1288-3.15)*2 = 3440 - 857.7 = 2582.3 -> 2582
	if sx != 2582 {
		t.Errorf("X 缩放不符：got %d，want 2582", sx)
	}
}

func TestD3BuffSamplePointAppliesWindowOrigin(t *testing.T) {
	// 窗口位于副屏时，采样点必须叠加客户端原点。
	sx, sy, ok := d3BuffSamplePoint(0, 3440, 1440, 100, -50, d3BuffSamplePercent)
	if !ok {
		t.Fatal("应能算出采样点")
	}
	if sx != 1391 || sy != 1278 {
		t.Fatalf("原点偏移未生效：got (%d,%d)，want (1391,1278)", sx, sy)
	}
}

func TestD3BuffSamplePointRejectsBadSlot(t *testing.T) {
	if _, _, ok := d3BuffSamplePoint(-1, 3440, 1440, 0, 0, d3BuffSamplePercent); ok {
		t.Error("负数槽位应被拒绝")
	}
	if _, _, ok := d3BuffSamplePoint(d3SlotCount, 3440, 1440, 0, 0, d3BuffSamplePercent); ok {
		t.Error("越界槽位应被拒绝")
	}
}

func TestD3BuffActiveThreshold(t *testing.T) {
	// 采样点 (1291,1328)：绿色分量 >= 95 视为 Buff 仍在生效。
	cases := []struct {
		name string
		g    uint8
		want bool
	}{
		{"发光环未结束", 120, true},
		{"恰好达到阈值", 95, true},
		{"阈值下方一点", 94, false},
		{"Buff 已断", 10, false},
	}
	for _, c := range cases {
		px := fakePixel{
			w: 3440, h: 1440, rectOK: true,
			pixels: map[[2]int32][3]uint8{{1291, 1328}: {0, c.g, 0}},
		}
		if got := (d3Adapter{}).BuffActive(0, 0, px); got != c.want {
			t.Errorf("%s：BuffActive = %v，want %v（G=%d）", c.name, got, c.want, c.g)
		}
	}
}

func TestD3BuffActiveFailSafe(t *testing.T) {
	if (d3Adapter{}).BuffActive(0, 0, nil) {
		t.Error("像素源为空时应返回 false")
	}
	badRect := fakePixel{rectOK: false}
	if (d3Adapter{}).BuffActive(0, 0, badRect) {
		t.Error("窗口几何不可用时应返回 false")
	}
	noPixel := fakePixel{w: 3440, h: 1440, rectOK: true}
	if (d3Adapter{}).BuffActive(0, 0, noPixel) {
		t.Error("取色失败时应返回 false")
	}
}

func TestOtherGamesHaveNoBuffGuard(t *testing.T) {
	px := fakePixel{
		w: 3440, h: 1440, rectOK: true,
		pixels: map[[2]int32][3]uint8{{1291, 1328}: {0, 255, 0}},
	}
	for _, id := range []string{"d4"} {
		a, ok := For(id)
		if !ok {
			t.Fatalf("适配器 %s 未注册", id)
		}
		if a.BuffActive(0, 0, px) {
			t.Errorf("%s 暂无 Buff 守卫，应恒返回 false", id)
		}
	}
}

func TestForceStandCapabilities(t *testing.T) {
	d3, ok := For("d3")
	if !ok {
		t.Fatal("暗黑3 适配器未注册")
	}
	if got := d3.DefaultForceStandKey(); got != "SHIFT" {
		t.Errorf("暗黑3 的强制站立默认键应为 SHIFT，实际 %q", got)
	}
	keys := d3.ForceStandKeys()
	if len(keys) != 1 || keys[0] != "LMB" {
		t.Errorf("暗黑3 应对鼠标左键应用强制站立，实际 %v", keys)
	}
	// 其余游戏尚未实测，不擅自启用。
	for _, id := range []string{"d4"} {
		a, ok := For(id)
		if !ok {
			t.Fatalf("适配器 %s 未注册", id)
		}
		if got := a.ForceStandKeys(); len(got) != 0 {
			t.Errorf("%s 尚未启用强制站立，应为空，实际 %v", id, got)
		}
		if a.DefaultForceStandKey() == "" {
			t.Errorf("%s 应给出强制站立的默认键", id)
		}
	}
}

func TestAdapterRegistryConsistency(t *testing.T) {
	if len(All()) != 2 {
		t.Fatalf("预期注册 2 款游戏（暗黑4 / 暗黑3），实际 %d", len(All()))
	}
	// 展示顺序即注册顺序：暗黑4 在前，首次启动默认暗黑4。
	if All()[0].ID != "d4" || All()[1].ID != "d3" {
		t.Errorf("游戏顺序应为 d4、d3，实际 %s、%s", All()[0].ID, All()[1].ID)
	}
	if Default().ID != "d4" {
		t.Errorf("默认游戏应为 d4，实际 %s", Default().ID)
	}
	if _, ok := For("d2r"); ok {
		t.Error("暗黑2R 已从工具中移除，不应再注册适配器")
	}
	for _, g := range All() {
		a, ok := For(g.ID)
		if !ok {
			t.Fatalf("All() 中的 %s 无法用 For 取得", g.ID)
		}
		if a.Name() != g.Name || a.DefaultProcess() != g.DefaultProcess {
			t.Errorf("%s 的适配器与 Game 视图不一致", g.ID)
		}
		if len(a.Slots()) != 6 {
			t.Errorf("%s 的槽位数量应为 6，实际 %d", g.ID, len(a.Slots()))
		}
	}
	if _, ok := For("d2"); ok {
		t.Error("未支持的游戏不应返回适配器")
	}
}
