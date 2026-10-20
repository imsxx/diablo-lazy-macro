package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docExample 是 docs/share-code.md 中的示例码（v1）：必须始终可解析，用于守护向后兼容。
const docExample = `DLM1:eyJ2IjoxLCJnIjoiZDQiLCJuIjoi5YWJ55-b5rOV5biILemAn-WItyIsImEiOiLmn5BCROS9nOiAhSIsInRnIjoiRjEiLCJwIjpbeyJuIjoi6YCf5Yi3IiwiciI6W3sidGciOiJ0IiwiYSI6InRhcDo0IiwiaSI6MjUwfSx7InRnIjoiaDpYMSIsImEiOiJzZXE6MiwzIiwiaSI6MjAwfSx7InRnIjoidCIsImEiOiJ0YXA6MyIsImkiOjMwMDAwfSx7InRnIjoidCIsImEiOiJob2xkOlJNQiJ9LHsidGciOiJoOlgyIiwiYSI6Im9uY2U6MSJ9XX1dfQ`

// docExampleV2 是 v2 历史示例码：不再是最新协议，但必须始终可解析（向后兼容）。
const docExampleV2 = `DLM2:eyJ2IjoyLCJnIjoiZDMiLCJuIjoi5pqX6buRMyDpgJ_liLciLCJhIjoi5p-QQkTkvZzogIUiLCJ0ZyI6IkYxIiwicCI6W3sibiI6IumAn-WItyIsInIiOlt7InRnIjoidCIsImEiOiJ0YXA6MiIsImkiOjMwMCwic3QiOjN9LHsidGciOiJ0IiwiYSI6ImhvbGQ6Uk1CIn0seyJ0ZyI6InQiLCJhIjoidGFwOjEiLCJpIjoxNTAsInN0IjoyLCJkIjotNDAsInJkIjp0cnVlfV19XX0`

// docExampleV3 是 v3 历史示例码：不再是最新协议，但必须始终可解析（向后兼容）。
const docExampleV3 = `DLM3:eyJ2IjozLCJnIjoiZDMiLCJuIjoi5pqX6buRMyDpgJ_liLciLCJhIjoi5p-QQkTkvZzogIUiLCJ0ZyI6IlgxIiwicCI6W3sibiI6IumAn-WItyIsIm0iOiJzZXEiLCJyIjpbeyJ0ZyI6InQiLCJhIjoidGFwOjIiLCJpIjozMDAsInN0IjozfSx7InRnIjoidCIsImEiOiJob2xkOlJNQiJ9LHsidGciOiJ0IiwiYSI6InRhcDoxIiwiaSI6MTUwLCJzdCI6MiwiZCI6LTQwLCJyZCI6dHJ1ZX1dfV19`

// docExampleV4 是 v4 历史示例码：不再是最新协议，但必须始终可解析（空方案 + rd 折算）。
const docExampleV4 = `DLM4:eyJ2Ijo0LCJnIjoiZDMiLCJuIjoi5pqX6buRMyDpgJ_liLciLCJhIjoi5p-QQkTkvZzogIUiLCJ0ZyI6IlgxIiwicCI6W3sibiI6IumAn-WItyIsIm0iOiJzZXEiLCJyIjpbeyJ0ZyI6InQiLCJhIjoidGFwOjIiLCJpIjozMDAsInN0IjozfSx7InRnIjoidCIsImEiOiJob2xkOlJNQiJ9LHsidGciOiJ0IiwiYSI6InRhcDoxIiwiaSI6MTUwLCJzdCI6MiwiZCI6LTQwLCJyZCI6dHJ1ZX1dfSx7Im4iOiLmgqzotY8iLCJoayI6IkY5IiwiciI6W119XX0`

// docExampleV5 是 docs/share-code.md 中的示例码（v5）：必须始终可解析。
// 第三个规则展示延迟区间（d=40 / dn=2），第二个方案是空方案。
const docExampleV5 = `DLM5:eyJ2Ijo1LCJnIjoiZDMiLCJuIjoi5pqX6buRMyDpgJ_liLciLCJhIjoi5p-QQkTkvZzogIUiLCJ0ZyI6IlgxIiwicCI6W3sibiI6IumAn-WItyIsIm0iOiJzZXEiLCJyIjpbeyJ0ZyI6InQiLCJhIjoidGFwOjIiLCJpIjozMDAsInN0IjozfSx7InRnIjoidCIsImEiOiJob2xkOlJNQiJ9LHsidGciOiJ0IiwiYSI6InRhcDoxIiwiaSI6MTUwLCJzdCI6MiwiZCI6NDAsImRuIjoyfV19LHsibiI6IuaCrOi1jyIsImhrIjoiRjkiLCJyIjpbXX1dfQ`

// legacyPrefix 是 v1 前缀（仅测试使用）。
const legacyPrefix = "DLM1:"

func mustParse(t *testing.T, code string) *Parsed {
	t.Helper()
	p, err := Parse(code)
	if err != nil {
		t.Fatalf("Parse 失败：%v", err)
	}
	return p
}

func TestParseDocExample(t *testing.T) {
	p := mustParse(t, docExample)
	if p.Game != "d4" {
		t.Fatalf("游戏标识解析错误：%q", p.Game)
	}
	if p.Name != "光矛法师-速刷" || p.Author != "某BD作者" {
		t.Fatalf("名称/作者解析错误：%q / %q", p.Name, p.Author)
	}
	if p.ToggleVK != 0x70 {
		t.Fatalf("启停键应为 F1(0x70)，实际 0x%02X", p.ToggleVK)
	}
	if len(p.Profiles) != 1 || len(p.Profiles[0].Rules) != 5 {
		t.Fatalf("方案/规则数量错误：%d / %d", len(p.Profiles), len(p.Profiles[0].Rules))
	}
	rules := p.Profiles[0].Rules
	if rules[0].Kind != ActionTap || rules[0].Keys[0] != 0x34 || rules[0].IntervalMS != 250 || rules[0].HoldVK != 0 {
		t.Fatalf("规则 1 解析错误：%+v", rules[0])
	}
	if rules[1].HoldVK != 0x05 || rules[1].Kind != ActionSeq || len(rules[1].Keys) != 2 ||
		rules[1].Keys[0] != 0x32 || rules[1].Keys[1] != 0x33 || rules[1].IntervalMS != 200 {
		t.Fatalf("规则 2 解析错误：%+v", rules[1])
	}
	if rules[2].Kind != ActionTap || rules[2].Keys[0] != 0x33 || rules[2].IntervalMS != 30000 {
		t.Fatalf("规则 3 解析错误：%+v", rules[2])
	}
	if rules[3].Kind != ActionHold || rules[3].Keys[0] != 0x02 || rules[3].IntervalMS != 0 {
		t.Fatalf("规则 4 解析错误：%+v", rules[3])
	}
	if rules[4].Kind != ActionOnce || rules[4].HoldVK != 0x06 || rules[4].Keys[0] != 0x31 {
		t.Fatalf("规则 5 解析错误：%+v", rules[4])
	}
	// v1 码没有策略声明：全部为「未声明」，且不触发 Buff 守卫。
	for i, r := range rules {
		if r.Strategy != StrategyDisabled || r.Buff || r.DelayMS != 0 || r.DelayMinMS != 0 {
			t.Fatalf("规则 %d 不应携带 v2 调参：%+v", i+1, r)
		}
	}
}

func TestV1CodeReencodesAsCurrentVersion(t *testing.T) {
	raw, _, err := Decode(docExample)
	if err != nil {
		t.Fatalf("v1 旧码应当可解析：%v", err)
	}
	if raw.Version != 1 {
		t.Fatalf("原始形态版本应保持 1，实际 %d", raw.Version)
	}
	raw.Version = Version // 重新导出时升级为当前版本
	code, err := Encode(*raw)
	if err != nil {
		t.Fatalf("升级导出失败：%v", err)
	}
	if !strings.HasPrefix(code, Prefix) {
		t.Fatalf("升级导出应使用当前前缀：%s", code)
	}
	if len(mustParse(t, code).Profiles[0].Rules) != 5 {
		t.Fatalf("升级后的码应保持规则数量")
	}
}

func TestUpgradeLegacyFillsStrategies(t *testing.T) {
	raw := Raw{Version: 1, Profiles: []RawProfile{{Rules: []RawRule{
		{Trigger: "t", Action: "hold:RMB"},
		{Trigger: "t", Action: "tap:2", IntervalMS: 300},
		{Trigger: "t", Action: "tap:1", IntervalMS: 150},
		{Trigger: "t", Action: "seq:1,2", IntervalMS: 200},
	}}}}
	UpgradeLegacy(&raw)

	if raw.Version != Version {
		t.Fatalf("迁移后版本应为 %d，实际 %d", Version, raw.Version)
	}
	rules := raw.Profiles[0].Rules
	if rules[0].Strategy != StrategyHold {
		t.Errorf("按住不放应回填策略 1，实际 %d", rules[0].Strategy)
	}
	if rules[1].Strategy != StrategyBuff {
		t.Errorf("tap+300ms 应回填保持Buff，实际 %d", rules[1].Strategy)
	}
	if rules[2].Strategy != StrategySpam {
		t.Errorf("普通连点应回填策略 2，实际 %d", rules[2].Strategy)
	}
	if rules[3].Strategy != StrategyDisabled {
		t.Errorf("序列不应被声明策略，实际 %d", rules[3].Strategy)
	}

	// 迁移结果必须能通过校验，且保持Buff 的屏幕守卫标记已生效。
	parsed, err := Resolve(raw)
	if err != nil {
		t.Fatalf("迁移后的配置应合法：%v", err)
	}
	if !parsed.Profiles[0].Rules[1].Buff {
		t.Fatalf("迁移后保持Buff 规则应带守卫标记：%+v", parsed.Profiles[0].Rules[1])
	}
}

func TestUpgradeLegacyIsIdempotent(t *testing.T) {
	// 已声明策略的规则不会被改写，当前版本的配置原样通过。
	raw := Raw{Version: Version, Profiles: []RawProfile{{Rules: []RawRule{
		{Trigger: "t", Action: "tap:2", IntervalMS: 300, Strategy: StrategySpam},
	}}}}
	UpgradeLegacy(&raw)
	if raw.Profiles[0].Rules[0].Strategy != StrategySpam {
		t.Fatalf("已声明策略被改写：%d", raw.Profiles[0].Rules[0].Strategy)
	}
}

func TestToggleKeyDefaultsToMouseSideButton(t *testing.T) {
	// 缺省启停键为鼠标侧键 1：暗黑3/4 的默认键位都不占用鼠标额外按键
	// （暗黑3 的 F1 是「收藏」，Shift 两款都是「强制站立」）。
	p := mustParse(t, codeFromJSON(`{"v":5,"p":[{"r":[{"tg":"t","a":"tap:1","i":100}]}]}`))
	if p.ToggleVK != 0x05 {
		t.Fatalf("缺省启停键应为 X1(0x05)，实际 0x%02X", p.ToggleVK)
	}
	// 显式使用鼠标键作为启停键应当被接受。
	p2 := mustParse(t, codeFromJSON(`{"v":5,"tg":"X2","p":[{"r":[{"tg":"t","a":"tap:1","i":100}]}]}`))
	if p2.ToggleVK != 0x06 {
		t.Fatalf("鼠标键作为启停键应被接受，实际 0x%02X", p2.ToggleVK)
	}
}

func TestParseRejectsInvalidMode(t *testing.T) {
	valid := `{"tg":"t","a":"tap:1","i":100}`
	code := codeFromJSON(`{"v":5,"p":[{"m":"round","r":[` + valid + `]}]}`)
	if _, err := Parse(code); err == nil {
		t.Fatal("非法触发模式应当报错")
	}
}

func TestParseDocExampleV3(t *testing.T) {
	p := mustParse(t, docExampleV3)
	if p.Game != "d3" || p.ToggleVK != 0x05 {
		t.Fatalf("v3 示例基础字段解析错误：%+v", p)
	}
	if p.Profiles[0].Mode != ModeSequential {
		t.Fatalf("v3 示例触发模式解析错误：%q", p.Profiles[0].Mode)
	}
	rules := p.Profiles[0].Rules
	if len(rules) != 3 {
		t.Fatalf("v3 示例规则数量错误：%d", len(rules))
	}
	if !rules[0].Buff || rules[0].Strategy != StrategyBuff {
		t.Fatalf("v3 示例保持Buff 规则解析错误：%+v", rules[0])
	}
	if rules[1].Kind != ActionHold || rules[1].Strategy != StrategyDisabled {
		t.Fatalf("v3 示例按住规则解析错误：%+v", rules[1])
	}
	spam := rules[2]
	// v3 的 rd=true 在解析时折算成 [10ms, 40ms] 区间（保持旧行为）。
	if spam.Strategy != StrategySpam || spam.DelayMS != -40 || spam.DelayMinMS != legacyRandomDelayFloorMS {
		t.Fatalf("v3 示例连点延迟解析错误：%+v", spam)
	}
}

// 文档里的示例码必须与测试常量一字不差：AGENTS 要求示例码真实可解析，
// 但手工复制粘贴容易漂移，这里直接读文档比对并解析一次。
func TestDocExampleMatchesMarkdown(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "share-code.md"))
	if err != nil {
		t.Fatalf("读取协议文档失败：%v", err)
	}
	found := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, Prefix) {
			found = line
			break
		}
	}
	if found == "" {
		t.Fatalf("docs/share-code.md 里没有以 %s 开头的示例码", Prefix)
	}
	if found != docExampleV5 {
		t.Fatalf("文档示例码与测试常量不一致（文档 %d 字符 / 常量 %d 字符）", len(found), len(docExampleV5))
	}
	if _, err := Parse(found); err != nil {
		t.Fatalf("文档示例码不可解析：%v", err)
	}
}

// TestParseDocExampleV5 守护 v5 示例码：延迟区间与空方案。
func TestParseDocExampleV5(t *testing.T) {
	p := mustParse(t, docExampleV5)
	if p.Game != "d3" || len(p.Profiles) != 2 {
		t.Fatalf("v5 示例基础字段解析错误：%+v", p)
	}
	rules := p.Profiles[0].Rules
	if len(rules) != 3 {
		t.Fatalf("v5 示例规则数量错误：%d", len(rules))
	}
	if rules[2].DelayMS != 40 || rules[2].DelayMinMS != 2 {
		t.Fatalf("v5 示例延迟区间解析错误：%+v", rules[2])
	}
	if len(p.Profiles[1].Rules) != 0 {
		t.Fatalf("v5 示例的空方案应保持为空：%+v", p.Profiles[1])
	}
}

// TestParseDocExampleV4 守护 v4 历史码：rd 折算成区间、空方案合法。
func TestParseDocExampleV4(t *testing.T) {
	p := mustParse(t, docExampleV4)
	if p.Game != "d3" || p.ToggleVK != 0x05 {
		t.Fatalf("v4 示例基础字段解析错误：%+v", p)
	}
	if len(p.Profiles) != 2 {
		t.Fatalf("v4 示例应有 2 个方案，实际 %d", len(p.Profiles))
	}
	if len(p.Profiles[0].Rules) != 3 {
		t.Fatalf("第一个方案应有 3 条规则，实际 %d", len(p.Profiles[0].Rules))
	}
	empty := p.Profiles[1]
	if len(empty.Rules) != 0 {
		t.Fatalf("第二个方案应是空方案，实际 %d 条规则", len(empty.Rules))
	}
	if empty.SwitchVK != 0x78 {
		t.Fatalf("空方案的切换键应为 F9(0x78)，实际 0x%02X", empty.SwitchVK)
	}
}

// 空方案（0 条规则）在 v4 起合法：玩家新增的配置在填好技能前不该按任何键。
func TestParseAllowsEmptyProfile(t *testing.T) {
	p := mustParse(t, codeFromJSON(`{"v":5,"p":[{"r":[]},{"n":"有规则","r":[{"tg":"t","a":"tap:1","i":100}]}]}`))
	if len(p.Profiles) != 2 || len(p.Profiles[0].Rules) != 0 || len(p.Profiles[1].Rules) != 1 {
		t.Fatalf("空方案解析错误：%+v", p.Profiles)
	}
}

// TestParseDocExampleV2 守护 v2 历史码的向后兼容，并确认缺省模式归一化为并发。
func TestParseDocExampleV2(t *testing.T) {
	p := mustParse(t, docExampleV2)
	if p.Game != "d3" || p.ToggleVK != 0x70 {
		t.Fatalf("v2 示例基础字段解析错误：%+v", p)
	}
	rules := p.Profiles[0].Rules
	if len(rules) != 3 {
		t.Fatalf("v2 示例规则数量错误：%d", len(rules))
	}
	if !rules[0].Buff || rules[0].Strategy != StrategyBuff {
		t.Fatalf("v2 示例保持Buff 规则解析错误：%+v", rules[0])
	}
	if rules[1].Kind != ActionHold || rules[1].Strategy != StrategyDisabled {
		t.Fatalf("v2 示例按住规则解析错误：%+v", rules[1])
	}
	spam := rules[2]
	if spam.Strategy != StrategySpam || spam.DelayMS != -40 || spam.DelayMinMS != legacyRandomDelayFloorMS {
		t.Fatalf("v2 示例连点延迟解析错误：%+v", spam)
	}
}

func TestParseToleratesWhitespacePrefixCaseAndPadding(t *testing.T) {
	payload := strings.TrimPrefix(docExample, legacyPrefix)
	messy := "dlm1:" + payload[:20] + "\n\t " + payload[20:] + "=="
	p := mustParse(t, messy)
	if len(p.Profiles) != 1 || len(p.Profiles[0].Rules) != 5 {
		t.Fatalf("容错解析结果错误：%+v", p)
	}
}

func codeFromJSON(js string) string {
	return Prefix + base64.RawURLEncoding.EncodeToString([]byte(js))
}

func legacyCodeFromJSON(js string) string {
	return legacyPrefix + base64.RawURLEncoding.EncodeToString([]byte(js))
}

func TestParseRejectsInvalid(t *testing.T) {
	valid := `{"tg":"t","a":"tap:1","i":100}`
	nine := strings.TrimSuffix(strings.Repeat(valid+",", 9), ",")
	cases := map[string]string{
		"空码":        "",
		"前缀错误":      "D4LM2:" + strings.TrimPrefix(docExample, legacyPrefix),
		"前缀版本非数字":   "DLMX:" + strings.TrimPrefix(docExample, legacyPrefix),
		"版本超出支持":    "DLM9:" + base64.RawURLEncoding.EncodeToString([]byte(`{"v":9,"p":[{"r":[`+valid+`]}]}`)),
		"前缀与正文版本不一致": legacyCodeFromJSON(`{"v":3,"p":[{"r":[` + valid + `]}]}`),
		"非 Base64": Prefix + "不是base64!!",
		"游戏标识非法":   codeFromJSON(`{"v":5,"g":"d5","p":[{"r":[` + valid + `]}]}`),
		"未知字段":     codeFromJSON(`{"v":5,"x":1,"p":[{"r":[` + valid + `]}]}`),
		"没有方案":     codeFromJSON(`{"v":5,"p":[]}`),
		"规则过多":     codeFromJSON(`{"v":5,"p":[{"r":[` + nine + `]}]}`),
		"间隔过小":     codeFromJSON(`{"v":5,"p":[{"r":[{"tg":"t","a":"tap:1","i":0}]}]}`),
		"间隔缺失":     codeFromJSON(`{"v":5,"p":[{"r":[{"tg":"t","a":"tap:1"}]}]}`),
		"单点带间隔":    codeFromJSON(`{"v":5,"p":[{"r":[{"tg":"t","a":"once:1","i":100}]}]}`),
		"按键名非法":    codeFromJSON(`{"v":5,"p":[{"r":[{"tg":"t","a":"tap:ZZ","i":100}]}]}`),
		"序列过长":     codeFromJSON(`{"v":5,"p":[{"r":[{"tg":"t","a":"seq:1,2,3,4,5,6,7","i":100}]}]}`),
		"触发方式非法":   codeFromJSON(`{"v":5,"p":[{"r":[{"tg":"x","a":"tap:1","i":100}]}]}`),
		"触发键非法":    codeFromJSON(`{"v":5,"p":[{"r":[{"tg":"h:??","a":"tap:1","i":100}]}]}`),

		"切换键用鼠标":   codeFromJSON(`{"v":5,"p":[{"r":[` + valid + `],"hk":"X1"}]}`),
		"切换键非法":    codeFromJSON(`{"v":5,"p":[{"r":[` + valid + `],"hk":"??"}]}`),
		"名称过长":     codeFromJSON(`{"v":5,"n":"` + strings.Repeat("长", 25) + `","p":[{"r":[` + valid + `]}]}`),
		"JSON 有尾巴":  codeFromJSON(`{"v":5,"p":[{"r":[` + valid + `]}]}{}`),
		"JSON 未闭合":  codeFromJSON(`{"v":5,"p":[{"r":[` + valid),
	}
	for name, code := range cases {
		if _, err := Parse(code); err == nil {
			t.Errorf("%s：应当报错但没有", name)
		}
	}
}

func TestParseV2TuningFields(t *testing.T) {
	js := `{"v":5,"p":[{"r":[` +
		`{"tg":"t","a":"tap:2","i":300,"st":3},` +
		`{"tg":"t","a":"tap:1","i":1000,"st":2,"d":-40,"dn":40},` +
		`{"tg":"t","a":"hold:RMB","st":1}]}]}`
	p := mustParse(t, codeFromJSON(js))
	rules := p.Profiles[0].Rules

	if rules[0].Strategy != StrategyBuff || !rules[0].Buff {
		t.Fatalf("保持Buff 规则解析错误：%+v", rules[0])
	}
	if rules[1].Strategy != StrategySpam || rules[1].Buff {
		t.Fatalf("连点策略解析错误：%+v", rules[1])
	}
	if rules[1].DelayMS != -40 || rules[1].DelayMinMS != 40 {
		t.Fatalf("延迟区间解析错误：%+v", rules[1])
	}
	if rules[2].Strategy != StrategyHold || rules[2].Kind != ActionHold {
		t.Fatalf("按住不放策略解析错误：%+v", rules[2])
	}
}

func TestParseRejectsInvalidTuning(t *testing.T) {
	cases := map[string]string{
		"延迟超上限":    `{"tg":"t","a":"tap:1","i":100,"d":30001}`,
		"延迟超下限":    `{"tg":"t","a":"tap:1","i":100,"d":-30001}`,
		"最小延迟超上限":  `{"tg":"t","a":"tap:1","i":100,"d":30000,"dn":30001}`,
		"最小延迟为负":   `{"tg":"t","a":"tap:1","i":100,"d":100,"dn":-5}`,
		"最小大于最大":   `{"tg":"t","a":"tap:1","i":100,"d":5,"dn":10}`,
		"只有最小延迟":   `{"tg":"t","a":"tap:1","i":100,"dn":10}`,
		"v5 不再接受 rd": `{"tg":"t","a":"tap:1","i":100,"d":100,"rd":true}`,
		"延迟用于序列":   `{"tg":"t","a":"seq:1,2","i":100,"d":50}`,
		"延迟用于按住":   `{"tg":"t","a":"hold:RMB","d":50}`,
		"策略超范围":    `{"tg":"t","a":"tap:1","i":100,"st":9}`,
		"策略与动作不符":  `{"tg":"t","a":"tap:1","i":100,"st":1}`,
		"已回收字段 rp":  `{"tg":"t","a":"tap:1","i":100,"rp":3}`,
		"已回收字段 ri":  `{"tg":"t","a":"tap:1","i":100,"ri":60}`,
		"已回收字段 pr":  `{"tg":"t","a":"tap:1","i":100,"st":3,"pr":1}`,
	}
	for name, rule := range cases {
		code := codeFromJSON(`{"v":5,"p":[{"r":[` + rule + `]}]}`)
		if _, err := Parse(code); err == nil {
			t.Errorf("%s：应当报错但没有", name)
		}
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	raw := Raw{
		Version: Version,
		Name:    "测试配置",
		Author:  "作者",
		Toggle:  "F8",
		Profiles: []RawProfile{
			{Name: "速刷", HK: "F2", Mode: ModeSequential, Rules: []RawRule{
				{Trigger: "t", Action: "tap:4", IntervalMS: 250},
				{Trigger: "h:X1", Action: "seq:2,3", IntervalMS: 200},
				{Trigger: "t", Action: "hold:RMB"},
				{Trigger: "t", Action: "tap:2", IntervalMS: 300, Strategy: StrategyBuff},
				{Trigger: "t", Action: "tap:1", IntervalMS: 1000, Strategy: StrategySpam, DelayMS: 100, DelayMinMS: 10},
			}},
		},
	}
	code, err := Encode(raw)
	if err != nil {
		t.Fatalf("Encode 失败：%v", err)
	}
	if !strings.HasPrefix(code, Prefix) {
		t.Fatalf("编码结果缺少当前前缀：%s", code)
	}
	p := mustParse(t, code)
	if p.ToggleVK != 0x77 || p.Name != "测试配置" {
		t.Fatalf("回读解析错误：%+v", p)
	}
	if p.Profiles[0].SwitchVK != 0x71 {
		t.Fatalf("方案切换键回读错误：0x%02X", p.Profiles[0].SwitchVK)
	}
	if p.Profiles[0].Mode != ModeSequential {
		t.Fatalf("触发模式回读错误：%q", p.Profiles[0].Mode)
	}
	last := p.Profiles[0].Rules[2]
	if last.Kind != ActionHold || last.Keys[0] != 0x02 {
		t.Fatalf("回读动作解析错误：%+v", last)
	}
	if buff := p.Profiles[0].Rules[3]; !buff.Buff {
		t.Fatalf("回读保持Buff 策略错误：%+v", buff)
	}
	if spam := p.Profiles[0].Rules[4]; spam.DelayMS != 100 || spam.DelayMinMS != 10 {
		t.Fatalf("回读连点延迟错误：%+v", spam)
	}
}

func TestEncodeRejectsInvalid(t *testing.T) {
	bad := Raw{Version: Version, Profiles: []RawProfile{{Rules: []RawRule{
		{Trigger: "t", Action: "tap:1", IntervalMS: 0},
	}}}}
	if _, err := Encode(bad); err == nil {
		t.Fatalf("非法配置应当拒绝编码")
	}
}

func TestParseTwoProfileFixture(t *testing.T) {
	const code = `DLM1:eyJ2IjoxLCJuIjoi5Y-M5pa55qGI5rWL6K-VIiwidGciOiJGMSIsInAiOlt7Im4iOiLpgJ_liLciLCJyIjpbeyJ0ZyI6InQiLCJhIjoidGFwOjQiLCJpIjoyNTB9LHsidGciOiJoOlgxIiwiYSI6InNlcToyLDMiLCJpIjoyMDB9XX0seyJuIjoi5pS75Z2aIiwiciI6W3sidGciOiJ0IiwiYSI6ImhvbGQ6Uk1CIn0seyJ0ZyI6Img6WDIiLCJhIjoib25jZToxIn1dfV19`
	p := mustParse(t, code)
	if len(p.Profiles) != 2 {
		t.Fatalf("期望 2 个方案，实际 %d", len(p.Profiles))
	}
	if p.Profiles[0].Name != "速刷" || p.Profiles[1].Name != "攻坚" {
		t.Fatalf("方案名解析错误：%q / %q", p.Profiles[0].Name, p.Profiles[1].Name)
	}
	if p.Profiles[1].Rules[0].Kind != ActionHold || p.Profiles[1].Rules[0].Keys[0] != 0x02 {
		t.Fatalf("攻坚方案按住右键规则解析错误：%+v", p.Profiles[1].Rules[0])
	}
	if p.Profiles[1].Rules[1].Kind != ActionOnce || p.Profiles[1].Rules[1].HoldVK != 0x06 {
		t.Fatalf("攻坚方案单点规则解析错误：%+v", p.Profiles[1].Rules[1])
	}
}
