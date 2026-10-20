//go:build windows

package main

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 前端（web/app.js）没有编译期类型检查：字段名一旦对不上，界面只会静默显示空白。
// 这个用例把 JS 实际读取的 JSON 字段钉在测试里，改名时必然失败。
func TestFrontendStateJSONContract(t *testing.T) {
	st := &frontendState{Version: version, CurrentGame: "d3", CurrentProfileIdx: 1}
	st.ToggleKey = "X1"
	st.ToggleKeyLabel = "侧键1"
	st.ProfileNames = []string{"配置 1"}
	st.Profiles = []frontendProfile{{
		Name: "配置 1", HK: "F9", HKLabel: "F9", Mode: "con",
		Slots: []frontendSlot{{
			Name: "技能一：", Key: "1", KeyLabel: "1", Strategy: 2,
			IntervalMS: 250, DelayMaxMS: 0, DelayMinMS: 0, Explanation: "每 250ms 持续连点「1」",
		}},
	}}
	st.Helpers.Hotkey = "F5"
	st.Helpers.HotkeyLabel = "F5"
	st.Helpers.Enabled = map[string]bool{"d3.loot": true}
	st.AllHelpers = []helperMeta{{ID: "d3.loot", Name: "快速拾取"}}
	st.ForceStandKey = "LSHIFT"
	st.ForceStandKeyLabel = "LSHIFT"
	st.Engine = frontendEngineState{
		Configured: true, Status: "ready", StatusText: "○ 宏已停止（按 侧键1 启动）",
		HoldMode: false, Enabled: false, Paused: false, Foreground: true,
	}

	b, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("状态序列化失败：%v", err)
	}
	js := string(b)

	// web/app.js 里读取过的字段（含嵌套层）
	wanted := []string{
		`"version"`,
		`"currentGame"`, `"currentProfileIdx"`, `"profileNames"`, `"profiles"`,
		`"toggleKeyLabel"`, `"startMode"`, `"quickPause"`,
		`"helpers"`, `"hotkey"`, `"hotkeyLabel"`, `"enabled"`,
		`"allHelpers"`, `"id"`, `"name"`,
		`"smartPause"`, `"soundCue"`, `"forceStandEnabled"`, `"forceStandKey"`, `"forceStandKeyLabel"`,
		`"targetProcess"`,
		`"engine"`, `"configured"`, `"status"`, `"statusText"`, `"holdMode"`, `"foreground"`,
		`"hud"`,
		`"name"`, `"hk"`, `"hkLabel"`, `"mode"`, `"slots"`,
		`"key"`, `"keyLabel"`, `"strategy"`, `"intervalMS"`, `"delayMaxMS"`, `"delayMinMS"`, `"explanation"`,
	}
	for _, key := range wanted {
		if !strings.Contains(js, key) {
			t.Errorf("前端读取的字段 %s 未出现在下发的状态 JSON 里", key)
		}
	}
}

// Go 与前端之间没有编译期约束，接口名/元素 id/事件处理器写错都只会「点了没反应」。
// 这个用例把两侧的契约钉死：曾经因为漏写 window.onStateUpdate 导致所有后端推送被静默丢弃。
func TestFrontendInterfaceContract(t *testing.T) {
	appJS := readRepoFile(t, "web/app.js")
	indexHTML := readRepoFile(t, "web/index.html")
	winGo := readRepoFile(t, "window.go")

	// 1) 状态推送契约：两侧必须同时存在
	if !strings.Contains(winGo, "window.onStateUpdate") {
		t.Error("window.go 不再通过 window.onStateUpdate 推送状态：请同步更新本用例与前端入口")
	}
	if !strings.Contains(appJS, "window.onStateUpdate") {
		t.Error("web/app.js 缺少 window.onStateUpdate：后端推送会被静默丢弃，托盘与热键引起的变更不会显示")
	}

	// 2) Go 注册的接口与前端调用的接口必须一一对应
	bound := map[string]bool{}
	for _, m := range regexp.MustCompile(`bind\("(api_[A-Za-z0-9_]+)"`).FindAllStringSubmatch(winGo, -1) {
		bound[m[1]] = true
	}
	if len(bound) == 0 {
		t.Fatal("没有解析到任何 Go 侧接口绑定，测试自身可能已失效")
	}
	used := map[string]bool{}
	for _, name := range regexp.MustCompile(`api_[A-Za-z0-9_]+`).FindAllString(appJS, -1) {
		used[name] = true
	}
	for name := range used {
		if !bound[name] {
			t.Errorf("前端使用了未注册的接口 %s", name)
		}
	}
	var unused []string
	for name := range bound {
		if !used[name] {
			unused = append(unused, name)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("Go 注册但前端未使用的接口：%s", strings.Join(unused, ", "))
	}

	// 3) 键帽一类「纯展示按钮」曾被「有焦点就跳过写入」的助手漏掉更新：点过它之后改键位会一直显示旧值。
	//    该助手已删除，这里挡住同类写法再被引入。
	if strings.Contains(appJS, "setTextIfIdle") {
		t.Error("web/app.js 又出现了 setTextIfIdle：展示型元素（键帽按钮）必须直接写 textContent")
	}

	// 3.5) 方案标签必须按后端下发的数量动态渲染：硬编码的 4 个按钮在「默认只有 1 个配置」时
	//      会露出 3 个空标签，点进去还会撞上后端越界校验。
	if !strings.Contains(indexHTML, `id="profileTabs"`) {
		t.Error("web/index.html 缺少方案标签容器 id=profileTabs")
	}
	if regexp.MustCompile(`class="tab-btn[^"]*"\s+data-tab=`).MatchString(indexHTML) {
		t.Error("web/index.html 又硬编码了方案标签：标签应由 renderTabs 按 profileNames 渲染")
	}

	// 4) 前端取用的元素 id 必须在 HTML 里存在
	for _, m := range regexp.MustCompile(`\$id\('([A-Za-z0-9_]+)'\)`).FindAllStringSubmatch(appJS, -1) {
		if !strings.Contains(indexHTML, `id="`+m[1]+`"`) {
			t.Errorf("web/index.html 缺少元素 id=%s", m[1])
		}
	}

	// 4) HTML 里内联调用的函数必须在 JS 中定义
	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`function\s+([A-Za-z0-9_]+)`).FindAllStringSubmatch(appJS, -1) {
		defined[m[1]] = true
	}
	keywords := map[string]bool{"if": true, "return": true, "typeof": true}
	for _, attr := range regexp.MustCompile(`on[a-z]+="([^"]+)"`).FindAllStringSubmatch(indexHTML, -1) {
		for _, call := range regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*\(`).FindAllStringSubmatch(attr[1], -1) {
			name := call[1]
			if keywords[name] || defined[name] {
				continue
			}
			t.Errorf("web/index.html 内联调用了未定义的函数 %s", name)
		}
	}
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", name, err)
	}
	return string(b)
}
