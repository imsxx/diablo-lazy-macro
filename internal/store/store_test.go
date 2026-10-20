package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imsxx/diablo-lazy-macro/internal/config"
)

func sampleSnapshot() Snapshot {
	return Snapshot{
		Config: config.Raw{
			Version: config.Version,
			Name:    "测试配置",
			Toggle:  "F1",
			Profiles: []config.RawProfile{
				{Name: "速刷", Rules: []config.RawRule{{Trigger: "t", Action: "tap:4", IntervalMS: 250}}},
				{Name: "攻坚", Rules: []config.RawRule{{Trigger: "h:X1", Action: "seq:2,3", IntervalMS: 200}}},
			},
		},
		ProfileIdx: 1,
	}
}

func TestGameKeysValidationAndCleanup(t *testing.T) {
	s := DefaultSettings()
	s.SetGameKeyMap("d3", GameKeys{ForceStandEnabled: true, ForceStandKey: "LSHIFT"})
	if err := s.Validate(); err != nil {
		t.Fatalf("合法按键映射应通过校验：%v", err)
	}
	if got := s.GameKeyMap("d3"); !got.ForceStandEnabled || got.ForceStandKey != "LSHIFT" {
		t.Fatalf("按键映射回读错误：%+v", got)
	}

	s.SetGameKeyMap("d3", GameKeys{ForceStandKey: "??"})
	if err := s.Validate(); err == nil {
		t.Fatal("非法键名应当被拒绝")
	}

	s.SetGameKeyMap("d3", GameKeys{})
	if _, ok := s.GameKeys["d3"]; ok {
		t.Fatal("空记录应被清理，避免存档里留下无意义的空对象")
	}
}

func TestGameKeysRejectsUnknownGame(t *testing.T) {
	s := DefaultSettings()
	s.SetGameKeyMap("d5", GameKeys{ForceStandEnabled: true})
	if err := s.Validate(); err == nil {
		t.Fatal("未知游戏的按键映射应当被拒绝")
	}
}

func TestSettingsApplyDefaults(t *testing.T) {
	// 旧存档缺少新增字段，解析后是零值：必须先补齐默认值，否则校验必然失败。
	var s Settings
	s.applyDefaults()
	if err := s.Validate(); err != nil {
		t.Fatalf("补齐默认值后应当合法：%v", err)
	}
	if s.CurrentGame == "" || s.FloatingHUD.Mode == "" || s.FloatingHUD.Attach == "" {
		t.Fatalf("默认值未补齐：%+v", s)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := sampleSnapshot()
	if err := Save(dir, ConfigFile("d4"), in); err != nil {
		t.Fatalf("Save 失败：%v", err)
	}
	out, err := Load(dir, ConfigFile("d4"))
	if err != nil {
		t.Fatalf("Load 失败：%v", err)
	}
	if out == nil {
		t.Fatalf("Load 返回空存档")
	}
	if out.ProfileIdx != in.ProfileIdx || out.Config.Name != in.Config.Name || len(out.Config.Profiles) != 2 {
		t.Fatalf("回读内容不符：%+v", out)
	}
	if out.Config.Profiles[1].Rules[0].Action != "seq:2,3" {
		t.Fatalf("回读规则不符：%+v", out.Config.Profiles[1].Rules[0])
	}
}

func TestGamesUseSeparateFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, ConfigFile("d4"), sampleSnapshot()); err != nil {
		t.Fatalf("Save d4 失败：%v", err)
	}
	if out, err := Load(dir, ConfigFile("d3")); err != nil || out != nil {
		t.Fatalf("d3 不应读到 d4 的存档：%v %v", out, err)
	}
}

func TestLoadMissingReturnsNil(t *testing.T) {
	s, err := Load(t.TempDir(), ConfigFile("d4"))
	if err != nil {
		t.Fatalf("Load 缺省目录应无错误：%v", err)
	}
	if s != nil {
		t.Fatalf("缺省目录应返回 nil 存档")
	}
}

func TestLoadCorruptThenQuarantine(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir, ConfigFile("d4"))
	if err := os.WriteFile(path, []byte("这不是 JSON"), 0o644); err != nil {
		t.Fatalf("准备损坏文件失败：%v", err)
	}
	if _, err := Load(dir, ConfigFile("d4")); err == nil {
		t.Fatalf("损坏存档应当报错")
	}
	moved, err := Quarantine(dir, ConfigFile("d4"))
	if err != nil {
		t.Fatalf("Quarantine 失败：%v", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("隔离后的文件不存在：%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("原存档应已被移走")
	}
	if s, err := Load(dir, ConfigFile("d4")); err != nil || s != nil {
		t.Fatalf("隔离后应回到干净状态：%v %v", s, err)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	bad := Snapshot{Config: config.Raw{Version: config.Version, Profiles: []config.RawProfile{
		{Rules: []config.RawRule{{Trigger: "t", Action: "tap:1", IntervalMS: 0}}},
	}}}
	if err := Save(t.TempDir(), ConfigFile("d4"), bad); err == nil {
		t.Fatalf("非法配置应当拒绝保存")
	}
}

func TestLoadRejectsBadProfileIndex(t *testing.T) {
	dir := t.TempDir()
	content := `{"config":{"v":1,"p":[{"r":[{"tg":"t","a":"tap:1","i":100}]}]},"profile_index":5}`
	if err := os.WriteFile(Path(dir, ConfigFile("d4")), []byte(content), 0o644); err != nil {
		t.Fatalf("准备文件失败：%v", err)
	}
	if _, err := Load(dir, ConfigFile("d4")); err == nil {
		t.Fatalf("越界方案序号应当报错")
	}
}

func TestSettingsDefaultAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadSettings(dir)
	if err != nil {
		t.Fatalf("缺省设置读取失败：%v", err)
	}
	if s.CurrentGame != "d4" {
		t.Fatalf("默认当前游戏应为 d4，实际 %q", s.CurrentGame)
	}
	proc, err := s.TargetProcess("d4")
	if err != nil || proc != "Diablo IV.exe" {
		t.Fatalf("d4 默认进程不符：%q %v", proc, err)
	}

	s.CurrentGame = "d3"
	s.Processes = map[string]string{"d3": "Diablo III.exe"}
	if err := SaveSettings(dir, *s); err != nil {
		t.Fatalf("SaveSettings 失败：%v", err)
	}
	out, err := LoadSettings(dir)
	if err != nil {
		t.Fatalf("回读设置失败：%v", err)
	}
	if out.CurrentGame != "d3" {
		t.Fatalf("回读当前游戏不符：%q", out.CurrentGame)
	}
	proc, err = out.TargetProcess("d3")
	if err != nil || proc != "Diablo III.exe" {
		t.Fatalf("进程覆盖未生效：%q %v", proc, err)
	}
	if _, err := out.TargetProcess("d9"); err == nil {
		t.Fatalf("未知游戏应当报错")
	}
}

// 不做「未知游戏静默回落」兜底：非法设置应当被拒绝，而不是被悄悄改写。
func TestUnknownGameIsRejected(t *testing.T) {
	dir := t.TempDir()
	content := `{"current_game":"d9"}`
	if err := os.WriteFile(Path(dir, SettingsFile), []byte(content), 0o644); err != nil {
		t.Fatalf("准备文件失败：%v", err)
	}
	if _, err := LoadSettings(dir); err == nil {
		t.Fatal("未知当前游戏应当被拒绝")
	}
	if err := SaveSettings(t.TempDir(), Settings{CurrentGame: "d9"}); err == nil {
		t.Fatalf("非法设置应当拒绝保存")
	}
}

func TestDefaultDirNotEmpty(t *testing.T) {
	dir, err := DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir 失败：%v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("DefaultDir 应返回绝对路径：%s", dir)
	}
}
