// Package store 把各游戏配置与工具设置持久化到本地文件（默认 %APPDATA%\diablo-lazy-macro）。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/imsxx/diablo-lazy-macro/internal/config"
	"github.com/imsxx/diablo-lazy-macro/internal/games"
	"github.com/imsxx/diablo-lazy-macro/internal/keys"
)

const (
	// DirName 是默认数据目录名。
	DirName = "diablo-lazy-macro"
	// SettingsFile 是工具级设置文件名。
	SettingsFile = "settings.json"
)

// ConfigFile 返回某款游戏的配置文件名。
func ConfigFile(gameID string) string {
	return "config-" + gameID + ".json"
}

// Snapshot 是一款游戏的持久化配置。
type Snapshot struct {
	Config     config.Raw `json:"config"`
	ProfileIdx int        `json:"profile_index"`
	StartMode  int        `json:"start_mode,omitempty"` // 0: 懒人模式(开关切换), 1: 仅按下时(按住生效)
}

// GameKeys 是一款游戏的按键映射：对应玩家自己的游戏内按键设置，与分享码无关、只存本机。
// 玩家的技能交互 / 强制站立 / 强制移动等键位因人而异，因此全部可自定义，这里只提供默认值。
type GameKeys struct {
	// ForceStandEnabled 是否在连点「需要强制站立」的技能时自动按住强制站立键（暗黑3 为左键技能）。
	ForceStandEnabled bool `json:"force_stand_enabled,omitempty"`
	// ForceStandKey 强制站立键的协议键名；留空表示使用该游戏适配器给出的默认值。
	ForceStandKey string `json:"force_stand_key,omitempty"`
}

// 助手功能的默认值与取值范围。
const (
	// DefaultHelperHotkey 是触发助手的默认快捷键（与 D3keyHelper 一致）。
	DefaultHelperHotkey = "F5"
	// DefaultHelperDelayMS 是动作间隔默认值，对应 D3keyHelper 的「标准」档。
	DefaultHelperDelayMS = 150
	HelperMinDelayMS     = 50
	HelperMaxDelayMS     = 1000
)

// HelperSettings 是助手功能的设置（工具级，不随分享码分发）。
//
// 助手目前是暗黑3 专属能力：界面识别用的取色点与判据都来自暗黑3，其它游戏没有对应数据，
// 因此调用方需要按游戏过滤。
type HelperSettings struct {
	// Hotkey 是触发助手的快捷键（协议键名；留空表示不绑定）。
	Hotkey string `json:"hotkey,omitempty"`
	// Enabled 按助手 ID 记录开关；默认全关，需要玩家显式开启。
	Enabled map[string]bool `json:"enabled,omitempty"`
	// ReforgeTarget 是重铸助手的目标品质（0 = 只重铸一次）。
	ReforgeTarget int `json:"reforge_target,omitempty"`
	// DelayMS 是每个动作之间的等待时长（动画需要时间，太快会丢点击）。
	DelayMS int `json:"delay_ms,omitempty"`
	// DevMode 是开发者模式：输出详细日志（每步取色、判定结果、鼠标去向），
	// 便于玩家复现问题时把日志整段回传。
	DevMode bool `json:"dev_mode,omitempty"`
}

// DefaultHelpers 返回助手功能的缺省设置：快捷键 F5，各助手开关默认关闭。
func DefaultHelpers() HelperSettings {
	return HelperSettings{Hotkey: DefaultHelperHotkey, DelayMS: DefaultHelperDelayMS}
}

// IsEnabled 返回指定助手是否启用。
func (h HelperSettings) IsEnabled(id string) bool {
	return h.Enabled != nil && h.Enabled[id]
}

// SetEnabled 设置指定助手的开关；关闭时删除键，避免存档里堆积 false。
func (h *HelperSettings) SetEnabled(id string, on bool) {
	if h.Enabled == nil {
		h.Enabled = map[string]bool{}
	}
	if on {
		h.Enabled[id] = true
		return
	}
	delete(h.Enabled, id)
}

// Validate 校验助手设置（供界面在写盘前先校验，避免整份设置因一处非法而全部不落盘）。
func (h HelperSettings) Validate() error {
	if h.Hotkey != "" {
		if _, err := keys.Parse(h.Hotkey); err != nil {
			return fmt.Errorf("助手快捷键无效：%v", err)
		}
	}
	if h.DelayMS != 0 && (h.DelayMS < HelperMinDelayMS || h.DelayMS > HelperMaxDelayMS) {
		return fmt.Errorf("助手动作间隔必须在 %d–%d 毫秒之间，实际 %d", HelperMinDelayMS, HelperMaxDelayMS, h.DelayMS)
	}
	if h.ReforgeTarget < 0 || h.ReforgeTarget > 5 {
		return fmt.Errorf("重铸目标品质取值 %d 无效（0–5）", h.ReforgeTarget)
	}
	return nil
}

// Settings 是工具级设置（不随分享码分发）。
type Settings struct {
	CurrentGame string              `json:"current_game"`
	Processes   map[string]string   `json:"processes,omitempty"`    // 各游戏目标进程名的本地覆盖
	GameKeys    map[string]GameKeys `json:"game_keys,omitempty"`    // 各游戏的按键映射（强制站立等）
	SmartPause  bool                `json:"smart_pause,omitempty"`  // 智能暂停：按 Tab 暂停/恢复，按回车 / T / M 停止宏
	SoundCue    bool                `json:"sound_cue,omitempty"`    // 切换配置成功时播放提示音
	Helpers     HelperSettings      `json:"helpers,omitempty"`      // 助手功能（暗黑3 专属）
	FloatingHUD FloatingHUDSettings `json:"floating_hud,omitempty"` // 游戏悬浮状态指示器（Mini HUD）
}

// FloatingHUDSettings 是游戏悬浮状态指示器的设置。
type FloatingHUDSettings struct {
	Enabled  bool   `json:"enabled"`             // 是否开启悬浮窗
	Mode     string `json:"mode,omitempty"`      // "bar"（战术横条）或 "badge"（紧凑方标）
	X        int    `json:"x,omitempty"`         // 自由定位：屏幕 X 坐标
	Y        int    `json:"y,omitempty"`         // 自由定位：屏幕 Y 坐标
	Locked   bool   `json:"locked,omitempty"`    // 是否锁定位置（禁止拖拽）
	GameOnly bool   `json:"game_only,omitempty"` // 是否仅游戏位于前台时显示
	// Attach 是定位方式："free" 自由放在屏幕上（用 X/Y），
	// "game" 贴在游戏窗口客户区上（用 OffsetX/OffsetY，随游戏窗口移动、最小化时隐藏）。
	Attach  string `json:"attach,omitempty"`
	OffsetX int    `json:"offset_x,omitempty"`
	OffsetY int    `json:"offset_y,omitempty"`
}

// DefaultFloatingHUD 返回悬浮窗默认设置。
// GameOnly 默认关闭：默认开启却「只在游戏前台才出现」，等于玩家第一次打开工具根本看不见它，
// 会以为功能坏了。嫌占地方的人可以自己勾上「仅前台显示」。
func DefaultFloatingHUD() FloatingHUDSettings {
	return FloatingHUDSettings{
		Enabled:  true,
		Mode:     "bar",
		X:        80,
		Y:        80,
		Locked:   false,
		GameOnly: false,
		Attach:   "free",
	}
}

func (f FloatingHUDSettings) validate() error {
	if f.Mode != "" && f.Mode != "bar" && f.Mode != "badge" {
		return fmt.Errorf("悬浮窗形态 %q 无效（有效值：bar / badge）", f.Mode)
	}
	if f.Attach != "" && f.Attach != "free" && f.Attach != "game" {
		return fmt.Errorf("悬浮窗定位方式 %q 无效（有效值：free / game）", f.Attach)
	}
	return nil
}

// DefaultDir 返回默认数据目录（%APPDATA%\diablo-lazy-macro）。
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法定位用户配置目录：%v", err)
	}
	return filepath.Join(base, DirName), nil
}

// Path 返回数据目录下某文件的完整路径。
func Path(dir, name string) string {
	return filepath.Join(dir, name)
}

// DefaultSettings 返回缺省设置（当前游戏为默认游戏）。
func DefaultSettings() *Settings {
	return &Settings{
		CurrentGame: games.Default().ID,
		FloatingHUD: DefaultFloatingHUD(),
	}
}

// applyDefaults 补齐缺失字段（旧存档升级用）：JSON 里没有的字段是零值，
// 直接校验会失败，因此加载后先按默认值填平。
func (s *Settings) applyDefaults() {
	// 首次启动（空值）与「当前游戏已从工具中移除」（如暗黑2R）都回落到默认游戏（暗黑4）。
	if _, ok := games.Lookup(s.CurrentGame); !ok {
		s.CurrentGame = games.Default().ID
	}
	// 顺带清理属于已下架游戏的进程覆盖与按键映射，否则整份设置会因「未知游戏」被拒绝加载，
	// 玩家升级后直接启动失败。
	for id := range s.Processes {
		if _, ok := games.Lookup(id); !ok {
			delete(s.Processes, id)
		}
	}
	for id := range s.GameKeys {
		if _, ok := games.Lookup(id); !ok {
			delete(s.GameKeys, id)
		}
	}
	h := DefaultHelpers()
	if s.Helpers.Hotkey == "" {
		s.Helpers.Hotkey = h.Hotkey
	}
	if s.Helpers.DelayMS == 0 {
		s.Helpers.DelayMS = h.DelayMS
	}
	if s.FloatingHUD.Mode == "" {
		s.FloatingHUD.Mode = "bar"
	}
	// 旧存档没有定位方式字段：默认「自由放在屏幕上」，与升级前行为一致。
	if s.FloatingHUD.Attach == "" {
		s.FloatingHUD.Attach = "free"
	}
	if s.FloatingHUD.X <= 0 && s.FloatingHUD.Y <= 0 {
		s.FloatingHUD.X = 80
		s.FloatingHUD.Y = 80
	}
}

// Validate 校验设置内容（Fail Fast）。
func (s *Settings) Validate() error {
	if _, ok := games.Lookup(s.CurrentGame); !ok {
		return fmt.Errorf("当前游戏 %q 无效", s.CurrentGame)
	}
	for id := range s.Processes {
		if _, ok := games.Lookup(id); !ok {
			return fmt.Errorf("进程覆盖表包含未知游戏 %q", id)
		}
	}
	for id, gk := range s.GameKeys {
		if _, ok := games.Lookup(id); !ok {
			return fmt.Errorf("按键映射表包含未知游戏 %q", id)
		}
		if gk.ForceStandKey == "" {
			continue
		}
		if _, err := keys.Parse(gk.ForceStandKey); err != nil {
			return fmt.Errorf("%s 的强制站立键无效：%v", id, err)
		}
	}
	if err := s.Helpers.Validate(); err != nil {
		return err
	}
	if err := s.FloatingHUD.validate(); err != nil {
		return err
	}
	return nil
}

// GameKeyMap 返回某游戏的按键映射；无记录时返回零值。
func (s *Settings) GameKeyMap(gameID string) GameKeys {
	return s.GameKeys[gameID]
}

// SetGameKeyMap 写入某游戏的按键映射；空记录会被清理，避免存档里留下无意义的空对象。
func (s *Settings) SetGameKeyMap(gameID string, gk GameKeys) {
	if gk.ForceStandKey == "" && !gk.ForceStandEnabled {
		delete(s.GameKeys, gameID)
		return
	}
	if s.GameKeys == nil {
		s.GameKeys = map[string]GameKeys{}
	}
	s.GameKeys[gameID] = gk
}

// TargetProcess 返回某游戏的目标进程名（含本地覆盖）。
func (s *Settings) TargetProcess(gameID string) (string, error) {
	if p := s.Processes[gameID]; p != "" {
		return p, nil
	}
	g, ok := games.Lookup(gameID)
	if !ok {
		return "", fmt.Errorf("未知游戏 %q", gameID)
	}
	return g.DefaultProcess, nil
}

// Load 读取某游戏配置；文件不存在返回 (nil, nil)，表示尚未保存。
// 存档存在但损坏、或其中配置不合法时返回错误，由调用方决定处置（禁止静默忽略）。
func Load(dir, name string) (*Snapshot, error) {
	path := Path(dir, name)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败：%v", path, err)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("解析 %s 失败：%v", path, err)
	}
	if _, err := config.Encode(s.Config); err != nil {
		return nil, fmt.Errorf("存档中的配置不合法（%s）：%v", path, err)
	}
	if s.ProfileIdx < 0 || s.ProfileIdx >= len(s.Config.Profiles) {
		return nil, fmt.Errorf("存档中的方案序号越界（%s）：%d", path, s.ProfileIdx)
	}
	return &s, nil
}

// Save 校验并原子写入某游戏配置。
func Save(dir, name string, s Snapshot) error {
	if _, err := config.Encode(s.Config); err != nil {
		return fmt.Errorf("拒绝保存非法配置：%v", err)
	}
	if s.ProfileIdx < 0 || s.ProfileIdx >= len(s.Config.Profiles) {
		return fmt.Errorf("方案序号越界：%d", s.ProfileIdx)
	}
	data, err := json.MarshalIndent(&s, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化存档失败：%v", err)
	}
	return writeAtomic(dir, name, data)
}

// LoadSettings 读取工具设置；文件不存在返回默认设置。
func LoadSettings(dir string) (*Settings, error) {
	path := Path(dir, SettingsFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败：%v", path, err)
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("解析 %s 失败：%v", path, err)
	}
	s.applyDefaults()
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("%s 内容不合法：%v", path, err)
	}
	return &s, nil
}

// SaveSettings 校验并原子写入工具设置。
func SaveSettings(dir string, s Settings) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("拒绝保存非法设置：%v", err)
	}
	data, err := json.MarshalIndent(&s, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化设置失败：%v", err)
	}
	return writeAtomic(dir, SettingsFile, data)
}

func writeAtomic(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录 %s 失败：%v", dir, err)
	}
	path := Path(dir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败：%v", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换 %s 失败：%v", path, err)
	}
	return nil
}

// Quarantine 把损坏的文件重命名为 .bad-<时间戳> 保留现场，返回新路径。
func Quarantine(dir, name string) (string, error) {
	path := Path(dir, name)
	target := fmt.Sprintf("%s.bad-%s", path, time.Now().Format("20060102-150405"))
	if err := os.Rename(path, target); err != nil {
		return "", fmt.Errorf("重命名损坏文件失败：%v", err)
	}
	return target, nil
}
