package games

import (
	"math"
	"sync"
	"time"
)

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
	d4SlotCount = 6

	// 绿色判据：G 足够亮、且明显压过 R 与 B（实测条内平均 RGB 约 66/133/45）。
	d4BuffGreenMin   = 95
	d4BuffGreenOverR = 35
	d4BuffGreenOverB = 35

	// d4BuffKeepRatio 是「还剩多少条才继续让路」：低于该比例就补一次。
	// 取 15% —— 比 D3keyHelper 的 5% 早一些，避免高攻速流派里条跑太快、补按还没落地就断了。
	// 条完全消失（0%）时比例必然低于阈值，所以「断了必补」是这条判据的天然兜底。
	d4BuffKeepRatio = 0.15

	// 自标定的扫描范围（相对客户区）：技能栏固定在底部正中，只需要扫这一带。
	d4ScanTopRatio    = 0.70
	d4ScanBottomRatio = 0.94
	d4ScanLeftRatio   = 0.28
	d4ScanRightRatio  = 0.72
	// 条间距 / 条宽（1920×1080 实测：条 39px、段距 43.5px）。是 UI 设计比例，不随分辨率变。
	d4StepPerBar = 43.5 / 39.0
	// 一行里少于这么多绿色像素，就认为当时没有 Buff，标定失败（下次再试）。
	d4MinBarPixels = 12
)

// d4BuffBarLayout 是一次自标定的结果：条所在行 + 6 个槽位的横向区间（客户区相对坐标）。
// 只依赖「技能栏固定在底部正中」这一条与分辨率无关的事实，不写死任何像素。
type d4BuffBarLayout struct {
	y  int32
	x0 [d4SlotCount]int32
	x1 [d4SlotCount]int32
}

// 标定结果按客户区尺寸缓存：同一尺寸算一次即可，窗口尺寸一变就自动重算。
// 每次启动工具都是新进程、缓存为空，等价于「每次启动重标一次」。
var (
	d4BarMu    sync.Mutex
	d4BarCache = map[[2]int32]d4BuffBarLayout{}
	// 标定在后台 goroutine 里做：一次要扫几十行、每行一次抓屏（5~6ms），
	// 合计几百毫秒。这个调用发生在引擎的锁内（Buff 判定），一旦同步执行，
	// 主循环会长时间持锁 → 宏像是「只按了一次」、悬浮窗不刷新、
	// 而界面线程改配置又要同一把锁 → 两边互相等，最后界面无响应被结束。
	d4BarCalibrating bool
	d4BarNextTry     time.Time
	d4BarAttempts    int
)

const (
	d4CalibrateRetry = 3 * time.Second
	d4CalibrateMax   = 5
)

// d4BuffGuardReady 只是总开关（测试用）。守卫是否真的生效，取决于能否标定到 Buff 条。
var d4BuffGuardReady = true

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

// RectSource 是 PixelSource 的另一个可选扩展：一次取回一块区域（RGB 紧凑排列，长度 = 3*w*h）。
// 自标定优先用它——整个扫描带抓一次就够，比逐行抓几十次快一个数量级。
type RectSource interface {
	Rect(x, y, w, h int32) ([]byte, bool)
}

func d4IsGreen(r, g, b uint8) bool {
	return g >= d4BuffGreenMin &&
		int(g)-int(r) > d4BuffGreenOverR &&
		int(g)-int(b) > d4BuffGreenOverB
}

// BuffActive 判断某槽位的 Buff 是否仍该让路（true = 还在生效，本轮跳过）。
// 坐标不写死：第一次需要时在技能栏一带自标定一次，按客户区尺寸缓存；
// 窗口尺寸一变就自动重标。标定不到 Buff 条时返回 false（照常按），绝不误拦。
func (d4Adapter) BuffActive(hwnd uintptr, slotIdx int, px PixelSource) bool {
	if !d4BuffGuardReady || px == nil || slotIdx < 0 || slotIdx >= d4SlotCount {
		return false
	}
	w, h, ox, oy, ok := px.ClientRect(hwnd)
	if !ok || w <= 0 || h <= 0 {
		return false
	}
	band, ok := px.(BandSource)
	if !ok {
		return false
	}
	layout, ok := d4Layout(w, h, ox, oy, px)
	if !ok {
		return false
	}
	data, ok := band.Band(layout.x0[slotIdx]+ox, layout.y+oy, layout.x1[slotIdx]-layout.x0[slotIdx]+1)
	if !ok {
		return false
	}
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

// d4Layout 返回当前窗口尺寸下的标定结果；未标定过就标一次，失败不缓存（下次再试）。
// d4Layout 只读缓存，永不在这里抓屏。
// 没有缓存时顺手在后台起一个标定任务，本次返回「未标定」——调用方照常按键，
// 等标定完成后下一次判定自然就用上了。
func d4Layout(w, h, ox, oy int32, px PixelSource) (d4BuffBarLayout, bool) {
	key := [2]int32{w, h}
	d4BarMu.Lock()
	cached, hit := d4BarCache[key]
	if hit {
		d4BarMu.Unlock()
		return cached, true
	}
	if d4BarCalibrating || time.Now().Before(d4BarNextTry) || d4BarAttempts >= d4CalibrateMax {
		d4BarMu.Unlock()
		return d4BuffBarLayout{}, false
	}
	d4BarCalibrating = true
	d4BarMu.Unlock()

	go func() {
		layout, ok := calibrateD4Bar(w, h, ox, oy, px)
		d4BarMu.Lock()
		d4BarCalibrating = false
		d4BarAttempts++
		if ok {
			d4BarCache[key] = layout
		} else {
			d4BarNextTry = time.Now().Add(d4CalibrateRetry)
		}
		d4BarMu.Unlock()
	}()
	return d4BuffBarLayout{}, false
}

// calibrateD4Bar 在「底部居中」的技能栏一带扫描绿色 Buff 条，得到行与 6 个槽位区间。
//
// 只依赖两个与分辨率无关的事实：
//  1. 技能栏固定在客户区底部正中（所以 6 个槽位以中线左右对称：3 个在左、3 个在右）；
//  2. 条的宽度与间距是固定比例（d4StepPerBar）。
//
// 因此只要扫到任意一条绿条，就能用「宽度 + 中线」推出全部 6 段的位置，
// 完全不需要知道游戏是按高度还是按宽度缩放 HUD。
func calibrateD4Bar(w, h, ox, oy int32, px PixelSource) (d4BuffBarLayout, bool) {
	left := int32(float64(w) * d4ScanLeftRatio)
	width := int32(float64(w) * (d4ScanRightRatio - d4ScanLeftRatio))
	top := int32(float64(h) * d4ScanTopRatio)
	height := int32(float64(h) * (d4ScanBottomRatio - d4ScanTopRatio))
	if width <= 0 || height <= 0 {
		return d4BuffBarLayout{}, false
	}
	// 一次抓整块扫描带：逐行抓屏要几十次、每次 5~6ms，整块只要一次。
	rs, ok := px.(RectSource)
	if !ok {
		return d4BuffBarLayout{}, false
	}
	buf, ok := rs.Rect(left+ox, top+oy, width, height)
	if !ok || len(buf) < int(width*height*3) {
		return d4BuffBarLayout{}, false
	}

	// 逐行数绿点，找条所在的那一行。
	bestRow, best := -1, 0
	stride := int(width) * 3
	for row := int32(0); row < height; row++ {
		base := int(row) * stride
		n := 0
		for i := 0; i < int(width); i++ {
			p := base + i*3
			if d4IsGreen(buf[p], buf[p+1], buf[p+2]) {
				n++
			}
		}
		if n > best {
			best, bestRow = n, int(row)
		}
	}
	if bestRow < 0 || best < d4MinBarPixels {
		return d4BuffBarLayout{}, false // 当时没有 Buff 条：等下次（引擎会先按一次技能）
	}
	row := top + int32(bestRow)

	// 在该行找最左一段绿条的宽度。
	base := bestRow * stride
	runStart, runEnd := -1, -1
	for i := 0; i < int(width); i++ {
		p := base + i*3
		green := d4IsGreen(buf[p], buf[p+1], buf[p+2])
		if green && runStart < 0 {
			runStart = i
		}
		if !green && runStart >= 0 && runEnd < 0 {
			runEnd = i - 1
			break
		}
	}
	if runStart < 0 {
		return d4BuffBarLayout{}, false
	}
	if runEnd < 0 {
		runEnd = int(width) - 1
	}
	barW := int32(runEnd - runStart + 1)
	if barW < 8 {
		return d4BuffBarLayout{}, false
	}

	// 6 段以中线对称：第 0 段中心 = 中线 - 2.5 * 段距。
	step := float64(barW) * d4StepPerBar
	var out d4BuffBarLayout
	out.y = row
	for i := 0; i < d4SlotCount; i++ {
		center := float64(w)/2 + (float64(i)-2.5)*step
		out.x0[i] = int32(math.Round(center - float64(barW)/2))
		out.x1[i] = int32(math.Round(center + float64(barW)/2))
	}
	return out, true
}

func countD4Green(data []byte) int {
	n := 0
	for i := 0; i+2 < len(data); i += 3 {
		if d4IsGreen(data[i], data[i+1], data[i+2]) {
			n++
		}
	}
	return n
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
