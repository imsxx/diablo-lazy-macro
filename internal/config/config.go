// Package config 实现分享码协议 v5 的解析、编码与校验（契约文档：docs/share-code.md）。
// 解析必须 Fail Fast：任何不合法输入整段拒绝，禁止部分应用或静默降级。
// 向后兼容：仍可解析 v1–v4 分享码（旧码导入后按当前版本重新导出）。
package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/imsxx/diablo-lazy-macro/internal/games"
	"github.com/imsxx/diablo-lazy-macro/internal/keys"
)

const (
	// Prefix 是当前版本分享码前缀，字母大小写不敏感；其后为 base64url 编码的紧凑 JSON。
	Prefix = "DLM5:"
	// Version 是当前协议版本。
	Version = 5
	// MinVersion 是仍可解析的最旧协议版本（旧分享码与旧存档可继续使用）。
	MinVersion = 1

	maxCodeChars       = 4096
	maxDecodedBytes    = 3000
	maxNameRunes       = 24
	maxProfiles        = 4
	maxRulesPerProfile = 8
	maxSequenceKeys    = 6
	// minIntervalMS 是执行间隔下限。工具不替玩家设「实用上限」：2ms/5ms 是常见配置，
	// 实际能否生效由系统定时器与游戏处理决定，协议不掺和。
	minIntervalMS = 1
	maxIntervalMS = 600000
	// maxDelayMS 是「延迟」的绝对值上限（正数延后、负数提前）。
	maxDelayMS = 30000
)

// 槽位释放策略取值。协议中可显式声明（st 字段），未声明（0）时由动作类型推断。
const (
	StrategyDisabled = 0 // 禁用：不自动触发
	StrategyHold     = 1 // 按住不放
	StrategySpam     = 2 // 连点
	StrategyBuff     = 3 // 保持Buff（需要屏幕守卫）
)

// DefaultToggleKey 是启停键的默认值：鼠标侧键 1（X1）。
//
// 选它而不是键盘功能键，依据是暗黑3/4 的默认键位：
// 暗黑3 的 F1 是「收藏」、F2–F12 虽未绑定但位置偏远；Shift 在暗黑3/4 都是「强制站立」；
// 而两款游戏的默认键位都不会占用鼠标额外按键。侧键在「仅按下时」的启动方式下也比键盘功能键省力。
const DefaultToggleKey = "X1"

// 触发模式：决定连点类技能是各自独立计时，还是按槽位顺序一轮一轮触发。
// 两者是玩家能直接感知的行为差异（罗技/雷蛇式顺序宏 vs 独立计时），因此对用户可见。
const (
	ModeConcurrent = "con" // 并发（默认）：各技能独立计时，到点即按，互不影响
	ModeSequential = "seq" // 循环：按槽位顺序一轮一轮按，一轮走完再从第一条继续
)

// ActionKind 是规则动作类型。
type ActionKind string

const (
	ActionTap  ActionKind = "tap"  // 连点：按间隔重复点击
	ActionOnce ActionKind = "once" // 单点：每次触发开始时点击一次
	ActionHold ActionKind = "hold" // 按住不放：触发期间保持按下
	ActionSeq  ActionKind = "seq"  // 序列：按间隔循环点击多个键
)

// Raw 与分享码 JSON 一一对应，仅用于编解码。
type Raw struct {
	Version  int          `json:"v"`
	Game     string       `json:"g,omitempty"`
	Name     string       `json:"n,omitempty"`
	Author   string       `json:"a,omitempty"`
	Toggle   string       `json:"tg,omitempty"`
	Profiles []RawProfile `json:"p"`
}

// RawProfile 是一套可切换的规则集合的 JSON 形态。
type RawProfile struct {
	Name  string    `json:"n,omitempty"`
	HK    string    `json:"hk,omitempty"` // 方案切换键（仅键盘键，可留空）
	Mode  string    `json:"m,omitempty"`  // 触发模式：seq 循环 / con 并发（缺省即并发）
	Rules []RawRule `json:"r"`
}

// RawRule 是单条规则的 JSON 形态。
type RawRule struct {
	Trigger    string `json:"tg"`
	Action     string `json:"a"`
	IntervalMS int    `json:"i,omitempty"`
	Strategy   int    `json:"st,omitempty"` // 释放策略：0 未声明 1 按住不放 2 连点 3 保持Buff
	// DelayMS 是延迟区间的上界（毫秒）：正数在本周期内后延、负数推迟到周期末尾前，0 关闭。
	DelayMS int `json:"d,omitempty"`
	// DelayMinMS 是延迟区间的下界（毫秒，非负）：缺省（0）表示与 |d| 相同，即固定延迟。
	// 与 d 一起表达「每次在 [DelayMinMS, |d|] 之间随机取一个幅度」。
	DelayMinMS int `json:"dn,omitempty"`
	// RandomDelay 是 v1–v4 的「延迟随机」开关，v5 起由 d + dn 取代。
	// 仍保留字段用于解析旧码（rd=true 等价于 dn=10，保持旧行为）。
	RandomDelay bool `json:"rd,omitempty"`
}

// Parsed 是校验并解析后的可执行形式。
type Parsed struct {
	Game     string
	Name     string
	Author   string
	ToggleVK uint16
	Profiles []ParsedProfile
}

// ParsedProfile 是解析后的方案；SwitchVK == 0 表示未设置方案切换键。
type ParsedProfile struct {
	Name     string
	SwitchVK uint16
	Mode     string // 触发模式：ModeSequential / ModeConcurrent
	Rules    []ParsedRule
}

// ParsedRule 是解析后的规则；HoldVK == 0 表示宏开启期间持续触发，否则为按住触发键。
type ParsedRule struct {
	HoldVK     uint16
	Kind       ActionKind
	Keys       []uint16
	IntervalMS int
	Strategy   int  // 释放策略（0 表示未声明）
	Buff       bool // 是否需要屏幕 Buff 守卫（策略为保持Buff）
	// DelayMS 是延迟区间上界（正数后延、负数末尾前）；DelayMinMS 是下界（非负）。
	// DelayMinMS == 0 表示固定延迟 |DelayMS|。
	DelayMS    int
	DelayMinMS int
}

// Decode 解析分享码，同时返回原始形态（用于持久化与再导出）与可执行形式。
// 整段不合法即返回错误（Fail Fast）。
func Decode(code string) (*Raw, *Parsed, error) {
	raw, err := decode(code)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := resolve(raw)
	if err != nil {
		return nil, nil, err
	}
	return raw, parsed, nil
}

// Parse 仅需可执行形式时的便捷入口。
func Parse(code string) (*Parsed, error) {
	_, parsed, err := Decode(code)
	return parsed, err
}

// Resolve 校验并解析原始形态（用于从本地存档恢复）。
func Resolve(raw Raw) (*Parsed, error) {
	return resolve(&raw)
}

// Encode 校验并编码分享码（用于导出与分享）。
func Encode(r Raw) (string, error) {
	if _, err := resolve(&r); err != nil {
		return "", err
	}
	b, err := json.Marshal(&r)
	if err != nil {
		return "", fmt.Errorf("编码 JSON 失败：%v", err)
	}
	return Prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func decode(code string) (*Raw, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("分享码为空")
	}
	if len(code) > maxCodeChars {
		return nil, fmt.Errorf("分享码过长（%d 字符，上限 %d）", len(code), maxCodeChars)
	}
	payload, prefixVersion, err := splitPrefix(code)
	if err != nil {
		return nil, err
	}
	payload = stripSpace(payload)
	if payload == "" {
		return nil, fmt.Errorf("分享码正文为空")
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(payload, "="))
	if err != nil {
		return nil, fmt.Errorf("分享码正文不是合法的 Base64：%v", err)
	}
	if len(data) > maxDecodedBytes {
		return nil, fmt.Errorf("分享码解码后过大（%d 字节，上限 %d）", len(data), maxDecodedBytes)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("分享码解码结果不是合法 UTF-8 文本")
	}
	var raw Raw
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("分享码 JSON 解析失败：%v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("分享码 JSON 末尾存在多余内容")
	}
	if raw.Version != prefixVersion {
		return nil, fmt.Errorf("分享码前缀声明版本 %d，正文却是 %d，可能已被改动", prefixVersion, raw.Version)
	}
	return &raw, nil
}

// splitPrefix 解析 "DLM<n>:" 前缀，返回正文与其声明的版本号。
// 支持解析 v1 旧码；版本超出支持范围时拒绝并提示升级。
func splitPrefix(code string) (payload string, version int, err error) {
	idx := strings.IndexByte(code, ':')
	if idx < 0 {
		return "", 0, fmt.Errorf("分享码必须以 %s 开头", Prefix)
	}
	tag := strings.ToUpper(strings.TrimSpace(code[:idx]))
	if len(tag) != 4 || !strings.HasPrefix(tag, "DLM") || tag[3] < '0' || tag[3] > '9' {
		return "", 0, fmt.Errorf("分享码必须以 %s 开头", Prefix)
	}
	version = int(tag[3] - '0')
	if version < MinVersion || version > Version {
		return "", 0, fmt.Errorf("分享码协议版本为 %d，本工具支持 %d–%d（请升级工具）", version, MinVersion, Version)
	}
	return code[idx+1:], version, nil
}

func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
}

func resolve(raw *Raw) (*Parsed, error) {
	if raw.Version < MinVersion || raw.Version > Version {
		return nil, fmt.Errorf("分享码版本为 %d，本工具支持 %d–%d（请升级工具）", raw.Version, MinVersion, Version)
	}
	if raw.Game != "" {
		if _, ok := games.Lookup(raw.Game); !ok {
			return nil, fmt.Errorf("游戏标识 %q 无效（支持 %s）", raw.Game, gameIDList())
		}
	}
	if err := checkName("配置名", raw.Name); err != nil {
		return nil, err
	}
	if err := checkName("作者署名", raw.Author); err != nil {
		return nil, err
	}

	toggleName := raw.Toggle
	if strings.TrimSpace(toggleName) == "" {
		toggleName = DefaultToggleKey
	}
	toggleVK, err := keys.Parse(toggleName)
	if err != nil {
		return nil, fmt.Errorf("启停键无效：%v", err)
	}

	if len(raw.Profiles) < 1 || len(raw.Profiles) > maxProfiles {
		return nil, fmt.Errorf("方案数量必须在 1–%d 之间，实际 %d", maxProfiles, len(raw.Profiles))
	}
	parsed := &Parsed{Game: raw.Game, Name: raw.Name, Author: raw.Author, ToggleVK: toggleVK}
	for pi, rp := range raw.Profiles {
		if err := checkName(fmt.Sprintf("方案 #%d 名称", pi+1), rp.Name); err != nil {
			return nil, err
		}
		// 空方案（0 条规则）合法：玩家新增的配置在填好技能前不按任何键，
		// 比用一条 tap:1 占位更安全。
		if len(rp.Rules) > maxRulesPerProfile {
			return nil, fmt.Errorf("方案 #%d 的规则数量不能超过 %d 条，实际 %d", pi+1, maxRulesPerProfile, len(rp.Rules))
		}
		mode, err := normalizeMode(pi+1, rp.Mode)
		if err != nil {
			return nil, err
		}
		pp := ParsedProfile{Name: rp.Name, Mode: mode}
		if hk := strings.TrimSpace(rp.HK); hk != "" {
			vk, err := keys.Parse(hk)
			if err != nil {
				return nil, fmt.Errorf("方案 #%d 的切换键无效：%v", pi+1, err)
			}
			if keys.IsMouse(vk) {
				return nil, fmt.Errorf("方案 #%d 的切换键不支持鼠标键（%s），请改用键盘键", pi+1, keys.Label(vk))
			}
			pp.SwitchVK = vk
		}
		for ri, rr := range rp.Rules {
			rule, err := resolveRule(pi+1, ri+1, raw.Version, rr)
			if err != nil {
				return nil, err
			}
			pp.Rules = append(pp.Rules, *rule)
		}
		parsed.Profiles = append(parsed.Profiles, pp)
	}
	return parsed, nil
}

func resolveRule(profileNo, ruleNo, version int, r RawRule) (*ParsedRule, error) {
	where := fmt.Sprintf("方案 #%d 规则 #%d", profileNo, ruleNo)
	rule := &ParsedRule{IntervalMS: r.IntervalMS}

	trigger := strings.TrimSpace(r.Trigger)
	switch {
	case strings.EqualFold(trigger, "t"):
		// HoldVK 保持零值：宏开启期间持续触发
	case strings.HasPrefix(strings.ToLower(trigger), "h:"):
		keyName := strings.TrimSpace(trigger[2:])
		if keyName == "" {
			return nil, fmt.Errorf("%s 的触发方式是「按住指定键」，但没有选择触发键", where)
		}
		vk, err := keys.Parse(keyName)
		if err != nil {
			return nil, fmt.Errorf("%s 的触发键无效：%v", where, err)
		}
		rule.HoldVK = vk
	default:
		return nil, fmt.Errorf("%s 的触发方式 %q 无效（应为 t 或 h:<键名>）", where, r.Trigger)
	}

	kindRaw, arg, ok := strings.Cut(r.Action, ":")
	if !ok {
		return nil, fmt.Errorf("%s 的动作 %q 无效（应为 tap:<键> / once:<键> / hold:<键> / seq:<键,键...>）", where, r.Action)
	}
	kind := ActionKind(strings.ToLower(strings.TrimSpace(kindRaw)))
	switch kind {
	case ActionTap, ActionOnce, ActionHold:
		if strings.TrimSpace(arg) == "" {
			return nil, fmt.Errorf("%s 的动作 %s 没有选择按键", where, kind)
		}
		vk, err := keys.Parse(arg)
		if err != nil {
			return nil, fmt.Errorf("%s 的动作键无效：%v", where, err)
		}
		rule.Kind = kind
		rule.Keys = []uint16{vk}
	case ActionSeq:
		parts := strings.Split(arg, ",")
		if len(parts) < 1 || len(parts) > maxSequenceKeys {
			return nil, fmt.Errorf("%s 的序列长度必须在 1–%d 之间，实际 %d", where, maxSequenceKeys, len(parts))
		}
		for i, p := range parts {
			if strings.TrimSpace(p) == "" {
				return nil, fmt.Errorf("%s 的序列第 %d 个按键为空", where, i+1)
			}
			vk, err := keys.Parse(p)
			if err != nil {
				return nil, fmt.Errorf("%s 的序列键无效：%v", where, err)
			}
			rule.Keys = append(rule.Keys, vk)
		}
		rule.Kind = ActionSeq
	default:
		return nil, fmt.Errorf("%s 的动作类型 %q 无效", where, kindRaw)
	}

	switch rule.Kind {
	case ActionTap, ActionSeq:
		if r.IntervalMS < minIntervalMS || r.IntervalMS > maxIntervalMS {
			return nil, fmt.Errorf("%s 的间隔必须在 %d–%d 毫秒之间，实际 %d", where, minIntervalMS, maxIntervalMS, r.IntervalMS)
		}
	default:
		if r.IntervalMS != 0 {
			return nil, fmt.Errorf("%s 的动作 %s 不接受间隔字段（i）", where, rule.Kind)
		}
	}

	if err := resolveRuleTuning(where, version, r, rule); err != nil {
		return nil, err
	}
	return rule, nil
}

// InferStrategy 按动作类型与间隔推断释放策略。
// 仅用于 v1 旧数据迁移（v1 没有策略字段）：tap + 300ms 是历史约定中的「保持Buff」。
// 序列与单点没有对应的界面策略，返回未声明，避免改写其原有语义。
func InferStrategy(kind ActionKind, intervalMS int) int {
	switch kind {
	case ActionHold:
		return StrategyHold
	case ActionTap:
		if intervalMS == 300 {
			return StrategyBuff
		}
		return StrategySpam
	default:
		return StrategyDisabled
	}
}

// UpgradeLegacy 把旧版原始配置就地升级到当前协议版本：
// 回填缺失的释放策略（v1 缺少该字段），并把版本号提升为当前版本。
// 已声明策略的规则不会被改写（幂等）；当前版本的配置直接返回。
func UpgradeLegacy(r *Raw) {
	if r == nil || r.Version >= Version {
		return
	}
	for pi := range r.Profiles {
		if r.Profiles[pi].Mode == "" {
			r.Profiles[pi].Mode = ModeConcurrent
		}
		for ri := range r.Profiles[pi].Rules {
			rule := &r.Profiles[pi].Rules[ri]
			// v4 及更早用 rd 表达随机：折算成显式区间，否则升级到 v5 后会被当作已回收字段拒绝。
			if rule.RandomDelay {
				rule.DelayMinMS = legacyRandomDelayFloorMS
				rule.RandomDelay = false
			}
			if rule.Strategy != StrategyDisabled {
				continue
			}
			kind, _, _ := strings.Cut(rule.Action, ":")
			rule.Strategy = InferStrategy(ActionKind(strings.ToLower(strings.TrimSpace(kind))), rule.IntervalMS)
		}
	}
	r.Version = Version
}

// legacyRandomDelayFloorMS 是 v1–v4「延迟随机」的实际下限（对齐 D3keyHelper 的 Random(10, 延迟)）。
// 仅用于把旧码折算成区间；v5 起区间完全由玩家给定，不再有内置下限。
const legacyRandomDelayFloorMS = 10

// resolveRuleTuning 校验并写入调参字段（延迟区间与释放策略）。
// 刻意不提供「重复次数」「优先级」这类配置：宏本身按轮循环触发，玩家无需理解它们。
func resolveRuleTuning(where string, version int, r RawRule, rule *ParsedRule) error {
	// 延迟（d / dn）：只对连点有意义——序列尚无偏移语义，静默忽略会误导使用者。
	if r.DelayMS != 0 || r.DelayMinMS != 0 || r.RandomDelay {
		if rule.Kind != ActionTap {
			return fmt.Errorf("%s 的动作 %s 不接受延迟字段（d/dn）", where, rule.Kind)
		}
	}
	if r.DelayMS < -maxDelayMS || r.DelayMS > maxDelayMS {
		return fmt.Errorf("%s 的延迟必须在 -%d–%d 毫秒之间，实际 %d", where, maxDelayMS, maxDelayMS, r.DelayMS)
	}
	if r.DelayMinMS < 0 || r.DelayMinMS > maxDelayMS {
		return fmt.Errorf("%s 的最小延迟必须在 0–%d 毫秒之间，实际 %d", where, maxDelayMS, r.DelayMinMS)
	}
	if r.RandomDelay {
		if version >= 5 {
			return fmt.Errorf("%s 使用了已回收字段 rd：v5 起延迟随机改由 d + dn 表达", where)
		}
		if r.DelayMS == 0 {
			return fmt.Errorf("%s 勾选了延迟随机但没有设置延迟", where)
		}
		if r.DelayMinMS != 0 {
			return fmt.Errorf("%s 同时给出 rd 与 dn，无法判断以哪个为准", where)
		}
		// 旧码的「随机」等价于 [10ms, |d|]，折算成显式区间。
		rule.DelayMS = r.DelayMS
		rule.DelayMinMS = legacyRandomDelayFloorMS
	} else {
		if r.DelayMinMS != 0 {
			if r.DelayMS == 0 {
				return fmt.Errorf("%s 设置了最小延迟（dn）却没有最大延迟（d）", where)
			}
			hi := r.DelayMS
			if hi < 0 {
				hi = -hi
			}
			if r.DelayMinMS > hi {
				return fmt.Errorf("%s 的最小延迟（%dms）不能大于最大延迟（%dms）", where, r.DelayMinMS, hi)
			}
		}
		rule.DelayMS = r.DelayMS
		rule.DelayMinMS = r.DelayMinMS
	}

	// 策略（st，可选）：显式声明时必须与动作类型相容。
	if r.Strategy < 0 || r.Strategy > StrategyBuff {
		return fmt.Errorf("%s 的策略取值 %d 无效（0–%d）", where, r.Strategy, StrategyBuff)
	}
	rule.Strategy = r.Strategy
	switch r.Strategy {
	case StrategyDisabled:
	case StrategyHold:
		if rule.Kind != ActionHold {
			return fmt.Errorf("%s 声明策略为「按住不放」，动作必须是 hold:<键>", where)
		}
	case StrategySpam:
		if rule.Kind != ActionTap {
			return fmt.Errorf("%s 声明策略为「连点」，动作必须是 tap:<键>", where)
		}
	case StrategyBuff:
		if rule.Kind != ActionTap {
			return fmt.Errorf("%s 声明策略为「保持Buff」，要求动作是 tap:<键>", where)
		}
		rule.Buff = true
	}
	return nil
}

// normalizeMode 校验并归一化方案的触发模式；空值表示并发（默认）。
func normalizeMode(profileNo int, mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModeConcurrent:
		return ModeConcurrent, nil
	case ModeSequential:
		return ModeSequential, nil
	default:
		return "", fmt.Errorf("方案 #%d 的触发模式 %q 无效（应为 seq 循环 或 con 并发）", profileNo, mode)
	}
}

func checkName(field, name string) error {
	if utf8.RuneCountInString(name) > maxNameRunes {
		return fmt.Errorf("%s 超过 %d 个字符上限", field, maxNameRunes)
	}
	return nil
}

func gameIDList() string {
	ids := make([]string, 0, 4)
	for _, g := range games.All() {
		ids = append(ids, g.ID)
	}
	return strings.Join(ids, "/")
}
