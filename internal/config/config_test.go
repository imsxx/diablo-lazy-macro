package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docExampleV6 是 docs/share-code.md 中的示例码（当前唯一支持的版本）：必须始终可解析。
// 第三个规则展示延迟区间（d=40 / dn=2），第二个方案是空方案。
const docExampleV6 = `DLM6:eyJ2Ijo2LCJnIjoiZDMiLCJuIjoi5pqX6buRMyDpgJ_liLciLCJhIjoi5p-QQkTkvZzogIUiLCJ0ZyI6IlgxIiwicCI6W3sibiI6IumAn-WItyIsIm0iOiJzZXEiLCJyIjpbeyJ0ZyI6InQiLCJhIjoidGFwOjIiLCJpIjozMDAsInN0IjozfSx7InRnIjoidCIsImEiOiJob2xkOlJNQiJ9LHsidGciOiJ0IiwiYSI6InRhcDoxIiwiaSI6MTUwLCJzdCI6MiwiZCI6NDAsImRuIjoyfV19LHsibiI6IuaCrOi1jyIsInIiOltdfV19`

func mustParse(t *testing.T, code string) *Parsed {
	t.Helper()
	p, err := Parse(code)
	if err != nil {
		t.Fatalf("Parse 失败：%v", err)
	}
	return p
}

// 旧版本码没有兼容义务：直接拒绝，并提示用新版重新导出。
func TestParseRejectsOldVersion(t *testing.T) {
	payload := strings.TrimPrefix(docExampleV6, Prefix)
	for _, prefix := range []string{"DLM1:", "DLM3:", "DLM5:"} {
		if _, err := Parse(prefix + payload); err == nil {
			t.Errorf("%s 前缀的旧码应当被拒绝", prefix)
		}
	}
}

func TestToggleKeyDefaultsToMouseSideButton(t *testing.T) {
	// 缺省启停键为鼠标侧键 1：暗黑3/4 的默认键位都不占用鼠标额外按键
	// （暗黑3 的 F1 是「收藏」，Shift 两款都是「强制站立」）。
	p := mustParse(t, codeFromJSON(`{"v":6,"p":[{"r":[{"tg":"t","a":"tap:1","i":100}]}]}`))
	if p.ToggleVK != 0x05 {
		t.Fatalf("缺省启停键应为 X1(0x05)，实际 0x%02X", p.ToggleVK)
	}
	// 显式使用鼠标键作为启停键应当被接受。
	p2 := mustParse(t, codeFromJSON(`{"v":6,"tg":"X2","p":[{"r":[{"tg":"t","a":"tap:1","i":100}]}]}`))
	if p2.ToggleVK != 0x06 {
		t.Fatalf("鼠标键作为启停键应被接受，实际 0x%02X", p2.ToggleVK)
	}
}

func TestParseRejectsInvalidMode(t *testing.T) {
	valid := `{"tg":"t","a":"tap:1","i":100}`
	code := codeFromJSON(`{"v":6,"p":[{"m":"round","r":[` + valid + `]}]}`)
	if _, err := Parse(code); err == nil {
		t.Fatal("非法触发模式应当报错")
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
	if found != docExampleV6 {
		t.Fatalf("文档示例码与测试常量不一致（文档 %d 字符 / 常量 %d 字符）", len(found), len(docExampleV6))
	}
	if _, err := Parse(found); err != nil {
		t.Fatalf("文档示例码不可解析：%v", err)
	}
}

// TestParseDocExampleV6 守护 v6 示例码：延迟区间与空方案。
func TestParseDocExampleV6(t *testing.T) {
	p := mustParse(t, docExampleV6)
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

// 空方案（0 条规则）在 v4 起合法：玩家新增的配置在填好技能前不该按任何键。
func TestParseAllowsEmptyProfile(t *testing.T) {
	p := mustParse(t, codeFromJSON(`{"v":6,"p":[{"r":[]},{"n":"有规则","r":[{"tg":"t","a":"tap:1","i":100}]}]}`))
	if len(p.Profiles) != 2 || len(p.Profiles[0].Rules) != 0 || len(p.Profiles[1].Rules) != 1 {
		t.Fatalf("空方案解析错误：%+v", p.Profiles)
	}
}

func TestParseToleratesWhitespacePrefixCaseAndPadding(t *testing.T) {
	payload := strings.TrimPrefix(docExampleV6, Prefix)
	messy := "dlm6:" + payload[:20] + "\n\t " + payload[20:] + "=="
	p := mustParse(t, messy)
	if len(p.Profiles) != 2 || len(p.Profiles[0].Rules) != 3 {
		t.Fatalf("容错解析结果错误：%+v", p)
	}
}

func codeFromJSON(js string) string {
	return Prefix + base64.RawURLEncoding.EncodeToString([]byte(js))
}

func TestParseRejectsInvalid(t *testing.T) {
	valid := `{"tg":"t","a":"tap:1","i":100}`
	nine := strings.TrimSuffix(strings.Repeat(valid+",", 9), ",")
	cases := map[string]string{
		"空码":        "",
		"前缀错误":      "D4LM6:" + strings.TrimPrefix(docExampleV6, Prefix),
		"前缀版本非数字":   "DLMX:" + strings.TrimPrefix(docExampleV6, Prefix),
		"版本超出支持":    "DLM9:" + base64.RawURLEncoding.EncodeToString([]byte(`{"v":9,"p":[{"r":[`+valid+`]}]}`)),
		"前缀与正文版本不一致": "DLM6:" + base64.RawURLEncoding.EncodeToString([]byte(`{"v":5,"p":[{"r":[`+valid+`]}]}`)),
		"非 Base64": Prefix + "不是base64!!",
		"游戏标识非法":   codeFromJSON(`{"v":6,"g":"d5","p":[{"r":[` + valid + `]}]}`),
		"未知字段":     codeFromJSON(`{"v":6,"x":1,"p":[{"r":[` + valid + `]}]}`),
		"没有方案":     codeFromJSON(`{"v":6,"p":[]}`),
		"规则过多":     codeFromJSON(`{"v":6,"p":[{"r":[` + nine + `]}]}`),
		"间隔过小":     codeFromJSON(`{"v":6,"p":[{"r":[{"tg":"t","a":"tap:1","i":0}]}]}`),
		"间隔缺失":     codeFromJSON(`{"v":6,"p":[{"r":[{"tg":"t","a":"tap:1"}]}]}`),
		"单点带间隔":    codeFromJSON(`{"v":6,"p":[{"r":[{"tg":"t","a":"once:1","i":100}]}]}`),
		"按键名非法":    codeFromJSON(`{"v":6,"p":[{"r":[{"tg":"t","a":"tap:ZZ","i":100}]}]}`),
		"序列过长":     codeFromJSON(`{"v":6,"p":[{"r":[{"tg":"t","a":"seq:1,2,3,4,5,6,7","i":100}]}]}`),
		"触发方式非法":   codeFromJSON(`{"v":6,"p":[{"r":[{"tg":"x","a":"tap:1","i":100}]}]}`),
		"触发键非法":    codeFromJSON(`{"v":6,"p":[{"r":[{"tg":"h:??","a":"tap:1","i":100}]}]}`),

		"名称过长":     codeFromJSON(`{"v":6,"n":"` + strings.Repeat("长", 25) + `","p":[{"r":[` + valid + `]}]}`),
		"JSON 有尾巴":  codeFromJSON(`{"v":6,"p":[{"r":[` + valid + `]}]}{}`),
		"JSON 未闭合":  codeFromJSON(`{"v":6,"p":[{"r":[` + valid),
	}
	for name, code := range cases {
		if _, err := Parse(code); err == nil {
			t.Errorf("%s：应当报错但没有", name)
		}
	}
}

func TestParseV2TuningFields(t *testing.T) {
	js := `{"v":6,"p":[{"r":[` +
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
		code := codeFromJSON(`{"v":6,"p":[{"r":[` + rule + `]}]}`)
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
			{Name: "速刷", Mode: ModeSequential, Rules: []RawRule{
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
	code := codeFromJSON(`{"v":6,"n":"多方案测试","tg":"F1","p":[` +
		`{"n":"速刷","r":[{"tg":"t","a":"tap:4","i":250},{"tg":"h:X1","a":"seq:2,3","i":200}]},` +
		`{"n":"攻坚","r":[{"tg":"t","a":"hold:RMB"},{"tg":"h:X2","a":"once:1"}]}]}`)
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
