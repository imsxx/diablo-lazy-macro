// Package helper 实现暗黑3 的「助手」功能（自动分解、重铸、拾取等）。
//
// 与技能宏的关系：两者互斥。助手运行时不会与技能宏抢输入——调用方负责在启动助手前
// 停掉技能宏（D3keyHelper 同样如此：助手入口一进来就检查宏是否在运行）。
//
// 与技能宏的一致性：同样只用公开 API——键盘/鼠标合成 + 屏幕单点取色，
// 不读游戏内存、不注入进程、不加载驱动。
package helper

import (
	"fmt"
	"sort"
	"time"

	"github.com/imsxx/diablo-lazy-macro/internal/games"
)

// Mouse 是助手需要的鼠标能力（由 win32 实现；测试注入替身）。
// 坐标一律是屏幕绝对坐标，相对坐标的换算由 Env 负责。
type Mouse interface {
	// Position 返回鼠标当前的屏幕绝对坐标。
	Position() (x, y int32, err error)
	// Move 把鼠标移到屏幕绝对坐标。
	Move(x, y int32) error
	// Tap 敲一下指定键：既可以是鼠标键，也可以是键盘键（如回车确认对话框）。
	Tap(vk uint16) error
}

// Env 是一次助手运行的环境快照。
type Env struct {
	WinW, WinH       int32 // 客户区尺寸
	OriginX, OriginY int32 // 客户区左上角的屏幕坐标
	Lang             games.D3ClientLang
	// Delay 是每个动作之间的等待：动画需要时间播完，太快会丢点击。
	Delay time.Duration
	// Dev 为真时输出更详细的日志，便于玩家复现问题时回传。
	Dev bool

	Pixel   games.PixelSource
	Mouse   Mouse
	Aborted func() bool
	Logf    func(format string, args ...any)
}

// Sleep 等待一个动作间隔。
func (e *Env) Sleep() {
	if e.Delay > 0 {
		time.Sleep(e.Delay)
	}
}

// SleepHalf 等待半个动作间隔（用于「移动鼠标 → 点击」这类需要稍快衔接的场合）。
func (e *Env) SleepHalf() {
	if e.Delay > 0 {
		time.Sleep(e.Delay / 2)
	}
}

// SleepQuarter 等待四分之一个动作间隔（脚本里连续点击之间的节奏 delay//4）。
func (e *Env) SleepQuarter() {
	if e.Delay > 0 {
		time.Sleep(e.Delay / 4)
	}
}

// aborted 报告用户是否已请求中断（再按一次助手键即可打断）。
func (e *Env) aborted() bool {
	return e.Aborted != nil && e.Aborted()
}

// logf 记录日志；nil 时静默（便于测试）。
func (e *Env) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// debugf 只在开发模式下记录。
func (e *Env) debugf(format string, args ...any) {
	if e.Dev {
		e.logf(format, args...)
	}
}

// Sampler 返回按 3440x1440 基准坐标取色的采样器。
func (e *Env) Sampler() games.D3ColorAt {
	return games.D3ColorSampler(e.Pixel, e.WinW, e.WinH, e.OriginX, e.OriginY)
}

// MouseRel 返回鼠标相对客户区的坐标。
func (e *Env) MouseRel() (int32, int32, error) {
	x, y, err := e.Mouse.Position()
	if err != nil {
		return 0, 0, err
	}
	return x - e.OriginX, y - e.OriginY, nil
}

// MoveTo 把鼠标移到客户区相对坐标。
func (e *Env) MoveTo(relX, relY int32) error {
	return e.Mouse.Move(e.OriginX+relX, e.OriginY+relY)
}

// ClickAt 把鼠标移到客户区相对坐标后点击。
// 移动与点击之间留半拍：游戏需要一帧来更新鼠标悬停状态，否则点击会落在旧位置。
func (e *Env) ClickAt(relX, relY int32, vk uint16) error {
	if err := e.MoveTo(relX, relY); err != nil {
		return err
	}
	e.SleepHalf()
	return e.Mouse.Tap(vk)
}

// Helper 是一个可执行的助手。
type Helper interface {
	// ID 是稳定标识（用于配置与日志）。
	ID() string
	// Name 是显示名。
	Name() string
	// Run 执行一次助手动作；返回错误表示没能完成（会被记入日志并提示玩家）。
	Run(env *Env) error
}

// registry 保存已注册的助手，顺序即界面展示顺序。
var registry []Helper

// Register 注册一个助手（由各助手的 init 调用）。
func Register(h Helper) {
	registry = append(registry, h)
}

// displayOrder 决定界面里助手的排列：与 D3keyHelper 的辅助功能面板一致，
// 而不是按 Go 的 init 执行顺序（那个顺序取决于文件名，玩家看是没有逻辑的）。
var displayOrder = []string{
	"d3.gamble",
	"d3.loot",
	"d3.salvage.quick",
	"d3.kanai.reforge",
	"d3.kanai.upgrade",
	"d3.kanai.convert",
}

// All 返回全部已注册的助手，按 displayOrder 排序；未列入的排在末尾。
func All() []Helper {
	rank := make(map[string]int, len(displayOrder))
	for i, id := range displayOrder {
		rank[id] = i
	}
	out := make([]Helper, len(registry))
	copy(out, registry)
	sort.SliceStable(out, func(i, j int) bool {
		ri, okI := rank[out[i].ID()]
		rj, okJ := rank[out[j].ID()]
		if !okI {
			ri = len(displayOrder)
		}
		if !okJ {
			rj = len(displayOrder)
		}
		return ri < rj
	})
	return out
}

// AbortError 表示运行被用户中断。
type AbortError struct{ Helper string }

func (e AbortError) Error() string {
	return fmt.Sprintf("%s 已被用户中断", e.Helper)
}
