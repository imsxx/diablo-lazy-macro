// Package engine 实现规则引擎：把分享码配置翻译成实际的键鼠动作。
// 纯逻辑：输入合成与按键状态通过接口注入，不直接触碰 Win32，便于单元测试。
package engine

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/imsxx/diablo-lazy-macro/internal/config"
)

const (
	// buffResampleDelay 是「Buff 仍在生效」时的重采样间隔：既不频繁取色，也不会错过 Buff 断裂。
	buffResampleDelay = 150 * time.Millisecond
)

// Sender 负责实际合成输入（由 internal/win32 的实现注入）。
type Sender interface {
	Tap(vk uint16) error
	KeyDown(vk uint16) error
	KeyUp(vk uint16) error
}

// KeyState 提供按键当前是否被按下的状态（由 Win32 轮询实现注入）。
type KeyState interface {
	Pressed(vk uint16) bool
}

// Engine 是规则运行时的状态机。公共方法内部加锁，可被 UI 线程与 tick 线程并发调用。
//
// 总开关语义（协议 v2）：enabled 为 false 时任何规则都不触发；为 true 时，
// t 规则（HoldVK == 0）持续运行，h 规则仅在触发键被按住时运行。
// 按住（hold）动作在触发结束时释放；失去前台、关闭总开关、切换方案、退出前也必须释放。
type Engine struct {
	mu      sync.Mutex
	sender  Sender
	state   KeyState
	onError func(error)
	cfg     *config.Parsed

	profile     int
	enabled     bool
	rules       []ruleState
	buffChecker func(vk uint16) bool

	// 顺序（循环按键）模式的调度状态：游标指向下一个候选规则，seqNext 是下一次发送时刻。
	mode      string
	seqCursor int
	seqNext   time.Time

	// 强制站立：点击 forceStandApply 键（暗黑3 为鼠标左键）之前，先按住 forceStandVK。
	// 两者都为 0 表示未启用；玩家自己已按住该键时直接点击，不重复按下。
	forceStandApply uint16
	forceStandVK    uint16

	// 方案级「运行期间一直按住」的键（强制站立 / 强制移动 / 强制交互）。
	// profileHolds 是当前方案的完整列表；holdsActive 是真正由我们按下的那些，
	// 释放时只松开自己按下的，别把玩家手动按住的一起抬起来。
	profileHolds []uint16
	holdsActive  []uint16

	// 暂停（智能暂停用）：暂停期间不发送任何规则按键。
	// pauseUntil 为零值表示持续暂停，直到 Resume 才恢复。
	paused     bool
	pauseUntil time.Time

	// randIntn 是延迟随机使用的随机源；默认 math/rand.Intn，测试可替换为确定性实现。
	randIntn func(n int) int
}

type ruleState struct {
	rule     config.ParsedRule
	active   bool
	holding  bool
	nextFire time.Time // 下一次连点周期的起点
	fireAt   time.Time // 本周期内的实际发送时刻（叠加延迟偏移后）
	cursor   int
}

// New 创建引擎。onError 不得为 nil：发送失败必须被上报，禁止静默吞错。
func New(sender Sender, state KeyState, cfg *config.Parsed, onError func(error)) (*Engine, error) {
	if sender == nil || state == nil || cfg == nil || onError == nil {
		return nil, fmt.Errorf("engine: 构造参数不得为空")
	}
	if len(cfg.Profiles) == 0 {
		return nil, fmt.Errorf("engine: 配置没有任何方案")
	}
	e := &Engine{sender: sender, state: state, onError: onError, cfg: cfg, randIntn: rand.Intn}
	e.loadProfileLocked(0)
	return e, nil
}

func (e *Engine) loadProfileLocked(i int) {
	e.profile = i
	e.mode = e.cfg.Profiles[i].Mode
	if e.mode == "" {
		e.mode = config.ModeConcurrent
	}
	e.seqCursor = 0
	e.seqNext = time.Time{}
	e.profileHolds = e.cfg.Profiles[i].HoldVKs
	rules := e.cfg.Profiles[i].Rules
	e.rules = make([]ruleState, 0, len(rules))
	for _, r := range rules {
		e.rules = append(e.rules, ruleState{rule: r})
	}
}

// Enabled 返回总开关状态。
func (e *Engine) Enabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enabled
}

// SetEnabled 设置总开关；按住中的键会在下一次 Tick 释放。
func (e *Engine) SetEnabled(on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enabled = on
}

// SetBuffChecker 注入屏幕 Buff 状态检测器：入参为技能键，返回该键的 Buff 是否仍在生效。
// 只有声明了「保持Buff」策略（Buff == true）的规则会调用它，且仅在即将发送按键时调用。
func (e *Engine) SetBuffChecker(checker func(vk uint16) bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.buffChecker = checker
}

// SetForceStand 启用「点击指定技能键前先按住修饰键」（暗黑3：左键技能自动强制站立，避免角色走动）。
// applyVK 是需要保护的技能键，modifierVK 是强制站立键；任一为 0 即关闭。
func (e *Engine) SetForceStand(applyVK, modifierVK uint16) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.forceStandApply = applyVK
	e.forceStandVK = modifierVK
}

// QuickPause 暂停宏：d > 0 表示暂停 d 后自动恢复，d <= 0 表示一直暂停直到 Resume。
// 目前只有智能暂停（Tab / 聊天 / 地图）在用。
// now 由调用方（主循环）传入，便于测试。
func (e *Engine) QuickPause(now time.Time, d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused = true
	if d > 0 {
		e.pauseUntil = now.Add(d)
	} else {
		e.pauseUntil = time.Time{}
	}
}

// Resume 结束暂停。
func (e *Engine) Resume() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused = false
	e.pauseUntil = time.Time{}
}

// Paused 返回是否处于暂停中。
func (e *Engine) Paused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

// RuntimeState 是引擎的开关/暂停态快照。
// 界面在重建引擎（例如编辑规则）时用它继承玩家当前的运行态，
// 避免「改一个槽位就把宏重新打开」或把暂停状态丢掉。
type RuntimeState struct {
	Enabled bool
	Paused  bool
}

// RuntimeState 返回当前运行态。
func (e *Engine) RuntimeState() RuntimeState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return RuntimeState{Enabled: e.enabled, Paused: e.paused}
}

// ApplyRuntimeState 恢复运行态：paused 为 true 时保持暂停直到 Resume（等价「压住」触发的暂停语义）。
func (e *Engine) ApplyRuntimeState(s RuntimeState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enabled = s.Enabled
	e.paused = s.Paused
	e.pauseUntil = time.Time{}
}

// tickPausedLocked 处理暂停期间的行为：释放按住中的键、到期自动恢复。
func (e *Engine) tickPausedLocked(now time.Time) {
	// 暂停期间不能留着按住不放的键，否则技能会一直按着。
	e.releaseHoldsLocked()
	if !e.pauseUntil.IsZero() && !now.Before(e.pauseUntil) {
		e.paused = false
		e.pauseUntil = time.Time{}
	}
}

// Toggle 切换总开关并返回新状态。
func (e *Engine) Toggle() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enabled = !e.enabled
	return e.enabled
}

// ProfileIndex 返回当前方案序号。
func (e *Engine) ProfileIndex() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.profile
}

// ProfileCount 返回方案数量。
func (e *Engine) ProfileCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.cfg.Profiles)
}

// ProfileName 返回指定方案名；序号必须合法（编程错误直接 panic，Fail Fast）。
func (e *Engine) ProfileName(i int) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i < 0 || i >= len(e.cfg.Profiles) {
		panic(fmt.Sprintf("engine: 方案序号越界：%d", i))
	}
	return e.cfg.Profiles[i].Name
}

// SetProfile 切换方案：先释放旧方案按住中的键，再装载新方案。
func (e *Engine) SetProfile(i int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i < 0 || i >= len(e.cfg.Profiles) {
		return fmt.Errorf("方案序号 %d 超出范围（共 %d 个方案）", i, len(e.cfg.Profiles))
	}
	e.releaseHoldsLocked()
	e.loadProfileLocked(i)
	return nil
}

// ReleaseAll 立即释放所有按住中的键（幂等）。退出前与切换方案前必须调用。
func (e *Engine) ReleaseAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.releaseHoldsLocked()
}

// syncProfileHoldsLocked 维护方案级的按住键：宏开启且游戏在前台时保持按下，
// 停止 / 暂停 / 失焦立即松开。只松开自己按下的键，玩家手动按住的保持原样。
func (e *Engine) syncProfileHoldsLocked(foreground bool) {
	if e.enabled && foreground && !e.paused {
		for _, vk := range e.profileHolds {
			if containsVK(e.holdsActive, vk) || e.state.Pressed(vk) {
				continue
			}
			if err := e.sender.KeyDown(vk); err != nil {
				e.onError(err)
				continue
			}
			e.holdsActive = append(e.holdsActive, vk)
		}
		return
	}
	for _, vk := range e.holdsActive {
		if err := e.sender.KeyUp(vk); err != nil {
			e.onError(err)
		}
	}
	e.holdsActive = nil
}

func containsVK(list []uint16, vk uint16) bool {
	for _, v := range list {
		if v == vk {
			return true
		}
	}
	return false
}

func (e *Engine) releaseHoldsLocked() {
	e.syncProfileHoldsLocked(false)
	for i := range e.rules {
		st := &e.rules[i]
		if !st.holding {
			continue
		}
		if err := e.sender.KeyUp(st.rule.Keys[0]); err != nil {
			e.onError(err)
		}
		st.holding = false
	}
}

// Tick 推进一次状态机；调用方（主循环）按约 10ms 周期驱动。
// foreground 表示目标进程是否位于前台；非前台时不发送、按住动作释放，恢复后重新激活。
func (e *Engine) Tick(now time.Time, foreground bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.paused {
		e.syncProfileHoldsLocked(false) // 暂停期间不能继续按住强制站立/移动
		e.tickPausedLocked(now)
		return
	}
	e.syncProfileHoldsLocked(foreground)
	if e.mode == config.ModeSequential {
		e.tickSequentialLocked(now, foreground)
		return
	}
	for i := range e.rules {
		st := &e.rules[i]
		e.updateActiveLocked(st, now, foreground)
		if !st.active {
			continue
		}
		switch st.rule.Kind {
		case config.ActionTap:
			e.tickTapLocked(st, now)
		case config.ActionSeq:
			if !now.Before(st.nextFire) {
				vk := st.rule.Keys[st.cursor%len(st.rule.Keys)]
				st.cursor++
				e.fireLocked(st, vk, now)
			}
		}
	}
}

// updateActiveLocked 依据总开关、前台与按住触发键维护一条规则的激活状态。
func (e *Engine) updateActiveLocked(st *ruleState, now time.Time, foreground bool) {
	active := e.enabled && foreground
	if active && st.rule.HoldVK != 0 {
		active = e.state.Pressed(st.rule.HoldVK)
	}
	if active == st.active {
		return
	}
	st.active = active
	if active {
		e.activateLocked(st, now)
	} else {
		e.deactivateLocked(st)
	}
}

// tickSequentialLocked 顺序（循环按键）模式：
// 按住/单点类规则仍按各自语义独立维持；连点类规则按槽位顺序轮流触发，
// 每条规则触发后等待自己的执行间隔，一轮走完再从第一条继续——即罗技/雷蛇式顺序宏的心智。
func (e *Engine) tickSequentialLocked(now time.Time, foreground bool) {
	for i := range e.rules {
		st := &e.rules[i]
		if st.rule.Kind == config.ActionTap {
			continue // 连点由顺序轮统一调度
		}
		e.updateActiveLocked(st, now, foreground)
	}
	if !e.enabled || !foreground || len(e.rules) == 0 {
		return
	}
	if now.Before(e.seqNext) {
		return
	}
	// 从游标处找下一条可发送的连点规则：触发键未按住、或 Buff 仍在生效的规则跳过，游标继续前进。
	for n := 0; n < len(e.rules); n++ {
		st := &e.rules[e.seqCursor%len(e.rules)]
		e.seqCursor = (e.seqCursor + 1) % len(e.rules)
		if st.rule.Kind != config.ActionTap {
			continue
		}
		if st.rule.HoldVK != 0 && !e.state.Pressed(st.rule.HoldVK) {
			continue
		}
		if e.buffGateLocked(st) {
			continue
		}
		e.tapLocked(st.rule.Keys[0])
		e.seqNext = now.Add(e.delayOffsetLocked(st.rule) + time.Duration(st.rule.IntervalMS)*time.Millisecond)
		return
	}
	// 一轮下来没有任何技能可发送（全部被 Buff 守卫拦下）：稍后重试。
	e.seqNext = now.Add(buffResampleDelay)
}

// tickTapLocked 推进连点策略：周期起点 → 延迟偏移 → 发送 → 周期推进。
// 需要屏幕守卫时，若该技能 Buff 仍在生效，本轮让路、稍后重新采样。
func (e *Engine) tickTapLocked(st *ruleState, now time.Time) {
	if now.Before(st.nextFire) {
		return
	}
	if st.fireAt.IsZero() {
		st.fireAt = st.nextFire.Add(e.delayOffsetLocked(st.rule))
	}
	if now.Before(st.fireAt) {
		return
	}
	if e.buffGateLocked(st) {
		// Buff 仍在生效：不按键，稍后重新采样。
		st.fireAt = time.Time{}
		st.nextFire = now.Add(buffResampleDelay)
		return
	}
	e.tapLocked(st.rule.Keys[0])
	st.nextFire = now.Add(time.Duration(st.rule.IntervalMS) * time.Millisecond)
	st.fireAt = time.Time{}
}

// tapLocked 合成一次点击。若该键需要强制站立（暗黑3/4 的左键技能）且启用中，
// 则先按住强制站立键、点完立即释放；玩家自己已按住该键时直接点击、不重复按下。
func (e *Engine) tapLocked(vk uint16) {
	if e.forceStandVK != 0 && vk == e.forceStandApply && !e.state.Pressed(e.forceStandVK) {
		if err := e.sender.KeyDown(e.forceStandVK); err != nil {
			e.onError(err)
			return
		}
		if err := e.sender.Tap(vk); err != nil {
			e.onError(err)
		}
		if err := e.sender.KeyUp(e.forceStandVK); err != nil {
			e.onError(err)
		}
		return
	}
	if err := e.sender.Tap(vk); err != nil {
		e.onError(err)
	}
}

// buffGateLocked 判断本次连点是否应因「该技能 Buff 仍在生效」而让路。
func (e *Engine) buffGateLocked(st *ruleState) bool {
	return st.rule.Buff && e.buffChecker != nil && e.buffChecker(st.rule.Keys[0])
}

// delayOffsetLocked 计算本周期内发送时点的偏移：
// 正延迟在周期内偏后等待；负延迟推迟到周期末尾前（等价于 interval - 幅度）。
// 幅度在 [DelayMinMS, |DelayMS|] 之间随机取（两者相等即固定延迟）。
// 刻意不设「太小视为关闭」的门槛：2ms 就是 2ms，实际精度由系统定时器与游戏决定。
func (e *Engine) delayOffsetLocked(r config.ParsedRule) time.Duration {
	raw := r.DelayMS
	if raw == 0 {
		return 0
	}
	hi := raw
	if hi < 0 {
		hi = -hi
	}
	lo := r.DelayMinMS
	if lo < 0 {
		lo = -lo
	}
	if lo > hi {
		lo = hi
	}
	mag := hi
	if lo > 0 {
		mag = lo + e.randIntn(hi-lo+1)
	}
	offset := mag
	if raw < 0 {
		offset = r.IntervalMS - mag
		if offset < 0 {
			offset = 0
		}
	}
	return time.Duration(offset) * time.Millisecond
}

// NextDue 返回下一次「需要精确唤醒」的时刻（发送、Buff 重采样或快速暂停的连点）。
// 零值表示当前没有待办：调用方按自己的检测周期继续轮询即可。
// 主循环据此睡到该时刻，发送精度由系统定时器决定，而不是被轮询周期量化——
// 这样 2ms 的间隔不会被向上取整成 10ms。
func (e *Engine) NextDue(now time.Time) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()

	var next time.Time
	consider := func(t time.Time) {
		if t.IsZero() || !t.After(now) {
			return // 已过期的时刻不需要"精确唤醒"：本次 Tick 已经处理过
		}
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}

	if e.paused {
		if !e.pauseUntil.IsZero() {
			consider(e.pauseUntil)
		}
		return next
	}
	if !e.enabled {
		return time.Time{}
	}
	if e.mode == config.ModeSequential {
		consider(e.seqNext)
		return next
	}
	for i := range e.rules {
		st := &e.rules[i]
		if !st.active {
			continue
		}
		switch st.rule.Kind {
		case config.ActionTap:
			if st.fireAt.IsZero() {
				consider(st.nextFire)
			} else {
				consider(st.fireAt)
			}
		case config.ActionSeq:
			consider(st.nextFire)
		}
	}
	return next
}

func (e *Engine) activateLocked(st *ruleState, now time.Time) {
	switch st.rule.Kind {
	case config.ActionOnce:
		e.tapLocked(st.rule.Keys[0])
	case config.ActionHold:
		if err := e.sender.KeyDown(st.rule.Keys[0]); err != nil {
			e.onError(err)
			return
		}
		st.holding = true
	default: // tap / seq：激活即触发一次
		st.nextFire = now
		st.fireAt = time.Time{}
	}
}

func (e *Engine) deactivateLocked(st *ruleState) {
	if st.holding {
		if err := e.sender.KeyUp(st.rule.Keys[0]); err != nil {
			e.onError(err)
		}
		st.holding = false
	}
	st.nextFire = time.Time{}
	st.fireAt = time.Time{}
	st.cursor = 0
}

func (e *Engine) fireLocked(st *ruleState, vk uint16, now time.Time) {
	e.tapLocked(vk)
	st.nextFire = now.Add(time.Duration(st.rule.IntervalMS) * time.Millisecond)
}
