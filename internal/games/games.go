// Package games 定义受支持的暗黑系列游戏，是协议标识、显示名、默认目标进程与
// 游戏差异化适配能力（槽位布局、屏幕 Buff 守卫）的唯一事实源。
//
// 约定：通用层（引擎、协议、存储、界面骨架）不得出现游戏标识判断；
// 游戏差异一律通过本包的 Adapter 表达，避免特例渗入公共代码。
package games

// SlotDef 描述一个技能槽位的默认定义。
type SlotDef struct {
	Name       string // 界面显示名
	DefaultKey string // 默认按键（协议键名）
}

// PixelSource 抽象屏幕取色与窗口几何，由 internal/win32 注入实现，便于单元测试。
type PixelSource interface {
	// ClientRect 返回窗口客户区的宽高与左上角屏幕绝对物理坐标。
	ClientRect(hwnd uintptr) (w, h, originX, originY int32, ok bool)
	// Pixel 返回屏幕绝对物理坐标 (x, y) 处的 RGB 分量。
	Pixel(x, y int32) (r, g, b uint8, ok bool)
}

// Adapter 描述一款游戏的适配能力。
type Adapter interface {
	// ID 是协议标识（分享码 g 字段）。
	ID() string
	// Name 是界面显示名。
	Name() string
	// DefaultProcess 是默认目标进程名。
	DefaultProcess() string
	// Slots 返回技能槽位的默认定义（界面默认值与方案预设使用）。
	Slots() []SlotDef
	// DefaultForceStandKey 返回该游戏「强制站立 / 原地站立」的默认按键（协议键名）；无此机制返回空串。
	// 玩家可在工具内改成自己的游戏设置，这里只提供默认值。
	DefaultForceStandKey() string
	// ForceStandKeys 返回「点击前需先按住强制站立键」的技能按键（协议键名）。
	// 返回空表示该游戏暂未启用这一机制（未实测的游戏不擅自猜测行为）。
	ForceStandKeys() []string
	// BuffActive 报告指定槽位的技能 Buff 是否仍在生效；true 表示「仍在生效、不应抢按」。
	// 无屏幕守卫的游戏恒返回 false。slotIdx 为槽位序号（0 起）。
	BuffActive(hwnd uintptr, slotIdx int, px PixelSource) bool
}

// Game 是适配器的对外视图（协议与设置使用）。
type Game struct {
	ID             string // 协议标识（分享码 g 字段）
	Name           string // 显示名
	DefaultProcess string // 默认目标进程名
}

// adapters 是受支持游戏的唯一注册表；顺序即界面展示顺序，首个为默认游戏。
// 暗黑破坏神2：重制版 不需要技能宏，已从工具中整体移除（2026-10-20）。
var adapters = []Adapter{d4Adapter{}, d3Adapter{}}

// All 返回全部受支持游戏（副本，避免外部修改内部表）。
func All() []Game {
	out := make([]Game, 0, len(adapters))
	for _, a := range adapters {
		out = append(out, Game{ID: a.ID(), Name: a.Name(), DefaultProcess: a.DefaultProcess()})
	}
	return out
}

// Lookup 按协议标识查找游戏。
func Lookup(id string) (Game, bool) {
	for _, a := range adapters {
		if a.ID() == id {
			return Game{ID: a.ID(), Name: a.Name(), DefaultProcess: a.DefaultProcess()}, true
		}
	}
	return Game{}, false
}

// For 按协议标识取得游戏适配器。
func For(id string) (Adapter, bool) {
	for _, a := range adapters {
		if a.ID() == id {
			return a, true
		}
	}
	return nil, false
}

// Default 返回默认游戏（暗黑破坏神4）。
func Default() Game {
	a := adapters[0]
	return Game{ID: a.ID(), Name: a.Name(), DefaultProcess: a.DefaultProcess()}
}

// DefaultAdapter 返回默认游戏的适配器。
func DefaultAdapter() Adapter { return adapters[0] }
