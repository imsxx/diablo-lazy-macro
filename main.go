//go:build windows

// Diablo Lazy Macro 桌面执行器：主窗口 + 托盘常驻 + 全局热键 + 分享码导入/导出 + 日志。
// 支持暗黑破坏神4 / 暗黑破坏神3，各自独立配置。
// 唯一底线：不做无人值守的挂机与全自动打怪。
// 技术约束：不读游戏内存、不注入进程、不加载驱动；只合成键鼠输入，屏幕识别仅用公开的截图/取色；仅在目标游戏位于前台时发送。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imsxx/diablo-lazy-macro/internal/config"
	"github.com/imsxx/diablo-lazy-macro/internal/engine"
	"github.com/imsxx/diablo-lazy-macro/internal/games"
	"github.com/imsxx/diablo-lazy-macro/internal/helper"
	"github.com/imsxx/diablo-lazy-macro/internal/keys"
	"github.com/imsxx/diablo-lazy-macro/internal/logx"
	"github.com/imsxx/diablo-lazy-macro/internal/store"
	"github.com/imsxx/diablo-lazy-macro/internal/win32"

	"github.com/lxn/walk"
)

const (
	appName = "Diablo Lazy Macro · 暗黑懒人宏"
	version = "0.2.0-dev"
	// idlePoll 是「没有待发按键时」的检测周期：启停键、方案切换键、快速暂停都靠它轮询。
	// 10ms 对手按按键完全够（双击间隔都在 100ms 以上），而且不影响发送精度——
	// 真正要按键时引擎会给出到期时刻，循环睡到那一刻再醒。
	idlePoll = 10 * time.Millisecond
)

// winSender 把 internal/win32 的输入合成接入规则引擎。
type winSender struct{}

func (winSender) Tap(vk uint16) error     { return win32.Tap(vk) }
func (winSender) KeyDown(vk uint16) error { return win32.KeyDown(vk) }
func (winSender) KeyUp(vk uint16) error   { return win32.KeyUp(vk) }

// winKeyState 把 GetAsyncKeyState 轮询接入规则引擎。
type winKeyState struct{}

func (winKeyState) Pressed(vk uint16) bool { return win32.KeyPressed(vk) }

// runtimeSnapshot 是 UI 线程与 tick 线程共享的一次性快照。
type runtimeSnapshot struct {
	Engine        *engine.Engine
	Raw           *config.Raw
	ToggleVK      uint16
	GameID        string
	TargetProcess string
	StartMode  int // 0: 懒人模式 (开关切换), 1: 仅按下时 (按住生效)
	SmartPause bool
	Helpers       store.HelperSettings
	// SlotVKs 是每个方案 6 个槽位的虚拟键码（0 表示空），由 UI 线程在提交配置时写入。
	// Buff 守卫在 tick 线程按它定位槽位：只读快照，不再读界面控件状态（避免数据竞争）。
	SlotVKs [maxProfiles][numSlots]uint16
}

// runtimeState 保存会被 UI 线程与 tick 线程并发访问的状态。
type runtimeState struct {
	mu sync.Mutex
	s  runtimeSnapshot
}

func (s *runtimeState) snapshot() runtimeSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.s
}

func (s *runtimeState) apply(snap runtimeSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.s = snap
}

type app struct {
	mw   *walk.MainWindow // 隐藏的基础窗口：承载托盘与消息循环
	ni   *walk.NotifyIcon
	icon *walk.Icon // 应用图标（取自 exe 资源，窗口与托盘共用）
	dir  string
	log  *logx.Logger
	st   *runtimeState

	win *mainWindow  // 程序主窗口（可见）
	hud *floatingHUD // 游戏悬浮状态窗（置顶 Mini HUD）

	settings *store.Settings // 仅 UI 线程访问

	// 智能暂停的轮询状态（仅 tick 线程访问；spPrev 的长度与 smartPauseStopKeys 一致）
	spTab  bool
	spPrev [3]bool

	// 助手运行状态：helperHotkeyDown 只在 tick 线程读写；
	// 另两个会被助手 goroutine 触碰，因此用原子量。
	helperHotkeyDown bool
	helperRunning    atomic.Bool
	helperAbort      atomic.Bool

	notifyMu   sync.Mutex
	lastNotify time.Time
}

func main() {
	runtime.LockOSThread()
	a, err := newApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动失败：", err)
		os.Exit(1)
	}
	if ms := os.Getenv("DLM_SMOKE_EXIT_MS"); ms != "" {
		if n, convErr := strconv.Atoi(ms); convErr == nil && n > 0 {
			go func() {
				time.Sleep(time.Duration(n) * time.Millisecond)
				a.sync(func() { a.quitApp() })
			}()
		}
	}
	os.Exit(a.run())
}

func newApp() (*app, error) {
	mw, err := walk.NewMainWindow()
	if err != nil {
		return nil, fmt.Errorf("创建基础窗口失败：%v", err)
	}
	dir, err := store.DefaultDir()
	if err != nil {
		return nil, err
	}
	logger, err := logx.New(dir)
	if err != nil {
		return nil, err
	}
	// GUI 程序没有控制台：Go runtime 的致命错误（并发读写 map、nil 解引用、栈溢出）
	// 默认只写 stderr，进程会直接消失且我们的日志里什么都看不到。
	// 重定向到文件，崩溃后能拿到完整堆栈。
	if f, ferr := os.OpenFile(filepath.Join(dir, "logs", "stderr.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); ferr == nil {
		os.Stderr = f
		logger.Printf("标准错误已重定向到 logs/stderr.log")
	}
	// 栈溢出这类崩溃不走 Go 的 panic，需要 Windows 层的异常过滤器才抓得到。
	win32.InstallCrashHandler(filepath.Join(dir, "logs", "crash.log"))
	var ni *walk.NotifyIcon
	if iconObj, err := walk.NewNotifyIcon(mw); err == nil {
		ni = iconObj
	} else {
		logger.Printf("[托盘] 创建托盘图标失败（将仅使用主窗口模式）：%v", err)
	}
	a := &app{mw: mw, ni: ni, dir: dir, log: logger, st: &runtimeState{}}
	a.log.Printf("%s v%s 启动：%s/%s，数据目录 %s", appName, version, runtime.GOOS, runtime.GOARCH, dir)

	// 图标取自 exe 资源（winres.json 的 RT_GROUP_ICON #1）；加载失败退回系统默认图标，不影响功能。
	if icon, err := walk.NewIconFromResourceId(1); err == nil {
		a.icon = icon
		if err := a.mw.SetIcon(icon); err != nil {
			a.log.Printf("[图标] 主窗口图标设置失败：%v", err)
		}
		if ni != nil {
			if err := ni.SetIcon(icon); err != nil {
				a.log.Printf("[图标] 托盘图标设置失败：%v", err)
			}
		}
	} else {
		a.icon = walk.IconApplication()
		a.log.Printf("[图标] 资源图标加载失败，使用系统默认图标：%v", err)
	}

	settings, err := store.LoadSettings(dir)
	if err != nil {
		a.confirmQuarantine(store.SettingsFile, err)
		settings = store.DefaultSettings()
	}
	a.settings = settings
	a.hud = newFloatingHUD(a)
	a.log.Printf("当前游戏：%s，目标进程 %s", gameDisplayName(settings.CurrentGame), a.targetProcessOf(settings.CurrentGame))

	if err := a.loadGame(settings.CurrentGame); err != nil {
		a.confirmQuarantine(store.ConfigFile(settings.CurrentGame), err)
		if retryErr := a.loadGame(settings.CurrentGame); retryErr != nil {
			return nil, retryErr
		}
	}

	if err := a.initTrayMenu(); err != nil {
		return nil, err
	}
	a.refreshUI()
	if ni != nil {
		if err := ni.SetVisible(true); err != nil {
			a.log.Printf("[托盘] 显示图标失败：%v", err)
		}
		a.wireTrayClicks()
	}
	a.startTicker()

	if err := a.createMainWindow(); err != nil {
		a.log.Printf("[窗口] 创建失败，已退回托盘模式：%v", err)
		walk.MsgBox(a.mw, appName, "打开主窗口失败（仍可通过托盘使用）：\n"+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
	}
	return a, nil
}

func (a *app) run() int {
	defer func() {
		// 主循环返回 = 进程即将结束。这条日志是「这次到底怎么退出的」的唯一线索：
		// 正常退出会先打「退出主程序」，只有这条说明窗口是被别的路径关掉的。
		a.log.Printf("[退出] 消息循环结束，进程即将结束")
		if a.hud != nil {
			a.hud.destroy()
		}
		if eng := a.st.snapshot().Engine; eng != nil {
			eng.ReleaseAll()
		}
		// 退出时托盘的消息窗口往往已随主窗口销毁，隐藏失败属预期，故只记一行、不打印 walk 的堆栈。
		if a.ni != nil {
			if err := a.ni.SetVisible(false); err != nil {
				fmt.Fprintln(os.Stderr, "[托盘] 退出时隐藏图标失败（可忽略）")
			}
		}
		if err := a.log.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "[日志] 关闭失败：", err)
		}
	}()
	if a.win != nil && a.win.wv != nil {
		a.win.wv.Run()
		return 0
	}
	return a.mw.Run()
}

func (a *app) quitApp() {
	a.log.Printf("退出主程序")
	if a.hud != nil {
		a.hud.destroy()
	}
	if a.win != nil {
		a.win.quitting = true
		if a.win.wv != nil {
			a.win.wv.Terminate()
			a.win.wv.Destroy()
		}
	}
	_ = a.mw.Close()
}

func (a *app) sync(f func()) {
	// 这些回调跑在 WebView2 / walk 的消息线程上，panic 会直接带走整个进程，
	// 且 GUI 没有控制台可看：统一兜住并写日志。
	safe := func() {
		defer func() {
			if r := recover(); r != nil {
				a.log.Printf("[崩溃] 界面回调异常：%v\n%s", r, debug.Stack())
			}
		}()
		f()
	}
	if a.win != nil && a.win.wv != nil {
		a.win.wv.Dispatch(safe)
		return
	}
	a.mw.Synchronize(safe)
}

// owner 返回对话框应使用的宿主窗口。
func (a *app) owner() walk.Form {
	return a.mw
}

// wireTrayClicks 让双击托盘图标打开主窗口。
func (a *app) wireTrayClicks() {
	var lastClick time.Time
	a.ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button != walk.LeftButton {
			return
		}
		now := time.Now()
		if now.Sub(lastClick) < 400*time.Millisecond {
			lastClick = time.Time{}
			a.showMainWindow()
			return
		}
		lastClick = now
	})
}

// confirmQuarantine 询问后隔离无法读取的文件；失败只记录，不影响本次会话继续运行。
func (a *app) confirmQuarantine(name string, loadErr error) {
	if walk.MsgBox(a.owner(), appName, name+" 无法读取：\n"+loadErr.Error()+"\n\n要把它重命名保留（.bad-时间戳）并以默认状态继续吗？",
		walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
		a.log.Printf("%s 无法读取，用户选择保留原文件（本次会话以默认状态继续）：%v", name, loadErr)
		return
	}
	moved, err := store.Quarantine(a.dir, name)
	if err != nil {
		a.log.Printf("[错误] 隔离 %s 失败：%v", name, err)
		return
	}
	a.log.Printf("损坏文件已隔离：%s", moved)
}

func (a *app) loadGame(gameID string) error {
	// 加载链路上每一步都留一行日志：之前「没有已加载日志」却看不出卡在哪，
	// 只能靠猜；现在任何一步失败都能直接从日志定位。
	a.log.Printf("[配置] 开始加载 %s", gameID)
	target, err := a.settings.TargetProcess(gameID)
	if err != nil {
		a.log.Printf("[配置] 目标进程解析失败：%v", err)
		return err
	}
	next := runtimeSnapshot{
		GameID: gameID, TargetProcess: target,
		SmartPause: a.settings.SmartPause,
		Helpers:    a.settings.Helpers,
	}

	snap, err := store.Load(a.dir, store.ConfigFile(gameID))
	if err != nil {
		a.log.Printf("[配置] 读取存档失败：%v", err)
		return err
	}
	if snap != nil {
		raw := snap.Config
		a.log.Printf("[配置] 读到存档：协议 v%d，%d 个方案", raw.Version, len(raw.Profiles))
		parsed, err := config.Resolve(raw)
		if err != nil {
			a.log.Printf("[配置] 校验失败：%v", err)
			return err
		}
		eng, err := engine.New(winSender{}, winKeyState{}, parsed, a.notifyEngineError)
		if err != nil {
			a.log.Printf("[配置] 创建引擎失败：%v", err)
			return err
		}
		a.setupBuffChecker(eng)
		a.setupForceStand(eng)
		profIdx := snap.ProfileIdx
		if profIdx < 0 || profIdx >= len(parsed.Profiles) {
			profIdx = 0
		}
		if err := eng.SetProfile(profIdx); err != nil {
			return err
		}
		// 启动时保持「停止」态：宏不该在玩家没按启停键之前就自己跑起来。
		// 之前默认开启，导致玩家按启停键实际是「关掉」，悬浮窗随之显示待命，看起来像没生效。
		eng.SetEnabled(false)
		next.Engine = eng
		next.Raw = &raw
		next.ToggleVK = parsed.ToggleVK
		next.StartMode = snap.StartMode
	} else {
		a.log.Printf("[配置] 没有存档，使用默认配置")
		defRaw := defaultConfig()
		defRaw.Game = gameID
		parsed, err := config.Resolve(defRaw)
		if err == nil {
			if eng, err := engine.New(winSender{}, winKeyState{}, parsed, a.notifyEngineError); err == nil {
				a.setupBuffChecker(eng)
				a.setupForceStand(eng)
				_ = eng.SetProfile(0)
				eng.SetEnabled(false) // 同上：等玩家按启停键再开
				next.Engine = eng
				next.Raw = &defRaw
				next.ToggleVK = parsed.ToggleVK
				next.StartMode = 0
			}
		}
	}

	if old := a.st.snapshot(); old.Engine != nil {
		old.Engine.SetEnabled(false)
		old.Engine.ReleaseAll()
	}
	a.st.apply(next)
	if next.Engine != nil {
		a.log.Printf("已加载 %s 配置：%s（方案：%s）", gameDisplayName(gameID), next.Raw.Name, profileDisplayName(next.Engine, next.Engine.ProfileIndex()))
	} else {
		a.log.Printf("%s 暂无配置：等待导入或新建", gameDisplayName(gameID))
	}
	return nil
}

func (a *app) switchGame(gameID string) {
	if a.settings.CurrentGame == gameID {
		return
	}
	if err := a.loadGame(gameID); err != nil {
		a.confirmQuarantine(store.ConfigFile(gameID), err)
		if retryErr := a.loadGame(gameID); retryErr != nil {
			walk.MsgBox(a.owner(), appName, "切换游戏失败：\n"+retryErr.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
			return
		}
	}
	a.settings.CurrentGame = gameID
	if err := store.SaveSettings(a.dir, *a.settings); err != nil {
		walk.MsgBox(a.owner(), appName, "已切换游戏，但设置保存失败（下次启动会回到旧游戏）：\n"+err.Error(),
			walk.MsgBoxOK|walk.MsgBoxIconWarning)
	}
	a.afterStateChange()
	a.log.Printf("切换到 %s（目标进程 %s）", gameDisplayName(gameID), a.targetProcessOf(gameID))
}

// initTrayMenu 只建两个入口：打开界面、退出。
// 所有操作都在界面里做——托盘菜单不该再养一份「切换游戏 / 切换方案 / 导入导出」的影子界面。
func (a *app) initTrayMenu() error {
	if a.ni == nil {
		return nil
	}
	menu := a.ni.ContextMenu()
	if err := menu.Actions().Clear(); err != nil {
		return err
	}
	openAct := walk.NewAction()
	if err := openAct.SetText("打开界面"); err != nil {
		return err
	}
	openAct.Triggered().Attach(a.showMainWindow)
	if err := menu.Actions().Add(openAct); err != nil {
		return err
	}
	quitAct := walk.NewAction()
	if err := quitAct.SetText("退出"); err != nil {
		return err
	}
	quitAct.Triggered().Attach(a.quitApp)
	return menu.Actions().Add(quitAct)
}

// refreshUI 刷新托盘与主窗口的运行状态；必须在 UI 线程调用。
func (a *app) refreshUI() {
	snap := a.st.snapshot()

	tip := fmt.Sprintf("%s v%s · %s", appName, version, gameDisplayName(a.settings.CurrentGame))
	if snap.Engine == nil {
		tip += " · 未配置"
	} else {
		stateText := "已关闭"
		if snap.Engine.Enabled() {
			stateText = "已开启"
		}
		tip += fmt.Sprintf(" · %s · 方案：%s", stateText, profileDisplayName(snap.Engine, snap.Engine.ProfileIndex()))
	}
	if a.ni != nil {
		if err := a.ni.SetToolTip(tip); err != nil {
			a.log.Printf("[托盘] 设置提示失败：%v", err)
		}
	}

	if a.win != nil {
		a.win.refreshStatus()
	}
	if a.hud != nil {
		// 用带锁的访问器读前台态：该字段由 tick 线程写入，直接读会形成数据竞争。
		a.hud.update(snap, a.hud.isForeground())
	}
}

func (a *app) refreshUIAsync() {
	a.sync(a.refreshUI)
}

// afterStateChange 在任何改变运行状态的动作后统一刷新界面。
func (a *app) afterStateChange() {
	a.refreshUI()
	if a.win != nil {
		a.win.loadFromState()
	}
}

// previewImportFromClipboard 读取剪贴板里的分享码并校验，返回给人看的预览文本。
// 不产生任何副作用：确认与应用交给调用方（界面用两步弹层，托盘用原生确认框）。
func (a *app) previewImportFromClipboard() (config.Raw, *config.Parsed, string, error) {
	text, err := walk.Clipboard().Text()
	if err != nil {
		return config.Raw{}, nil, "", fmt.Errorf("读取剪贴板失败：%v", err)
	}
	raw, parsed, err := config.Decode(text)
	if err != nil {
		a.log.Printf("[导入] 分享码无效：%v", err)
		return config.Raw{}, nil, "", fmt.Errorf("分享码无效：%v", err)
	}

	var sb strings.Builder
	sb.WriteString("确认导入这份配置？\n\n")
	sb.WriteString(a.describeConfig(raw, parsed))
	if parsed.Game != "" && parsed.Game != a.settings.CurrentGame {
		sb.WriteString(fmt.Sprintf("\n注意：这份配置标注为「%s」，当前游戏是「%s」，导入后会用于当前游戏。\n",
			gameDisplayName(parsed.Game), gameDisplayName(a.settings.CurrentGame)))
	}
	if warns := config.Warnings(parsed); len(warns) > 0 {
		sb.WriteString("\n注意：\n")
		for _, w := range warns {
			sb.WriteString("· " + w + "\n")
		}
	}
	return *raw, parsed, sb.String(), nil
}

// applyImport 应用一份已确认的导入配置：按分享码自带的方案数量校验、按「启用」重建引擎。
// 不再补齐到 4 套——码里有几个方案就导入几个（协议保证 1–4），需要更多由玩家在界面上加。
func (a *app) applyImport(raw config.Raw, parsed *config.Parsed) error {
	resolved, err := config.Resolve(raw)
	if err != nil {
		return fmt.Errorf("配置校验失败：%v", err)
	}
	startMode := a.st.snapshot().StartMode
	// 导入是玩家的显式动作：与旧行为一致，导入后宏处于启用状态。
	if err := a.applyConfig(&raw, resolved, applyOptions{
		ProfileIdx: 0,
		StartMode:  startMode,
		Inherit:    &engine.RuntimeState{Enabled: true},
	}); err != nil {
		a.log.Printf("[导入] 应用失败：%v", err)
		return fmt.Errorf("应用配置失败：%v", err)
	}
	if a.ni != nil {
		if err := a.ni.ShowInfo(appName, "配置已应用。\n进入游戏后按 "+keyName(resolved.ToggleVK)+" 启用宏。"); err != nil {
			a.log.Printf("[托盘] 气泡提示失败：%v", err)
		}
	}
	return nil
}



// applyOptions 是应用配置时的可选项。
type applyOptions struct {
	ProfileIdx int                            // 激活的方案序号
	StartMode  int                            // 启动方式：0 懒人（开关切换）/ 1 仅按下时（按住生效）
	Inherit    *engine.RuntimeState           // 新引擎继承的运行态；nil 表示继承当前引擎
	SlotVKs    *[maxProfiles][numSlots]uint16 // 槽位键快照；nil 表示按存档规则推导
	// SkipUIReload 跳过「从存档反推界面槽位表」：界面自己发起的提交不该被反推覆盖（会打乱槽位顺序）。
	SkipUIReload bool
}

func (a *app) applyConfig(raw *config.Raw, parsed *config.Parsed, opts applyOptions) error {
	gameID := a.settings.CurrentGame
	if raw.Game == "" {
		raw.Game = gameID
	}
	target, err := a.settings.TargetProcess(gameID)
	if err != nil {
		return err
	}
	old := a.st.snapshot()

	eng, err := engine.New(winSender{}, winKeyState{}, parsed, a.notifyEngineError)
	if err != nil {
		return err
	}
	a.setupBuffChecker(eng)
	a.setupForceStand(eng)
	if err := eng.SetProfile(opts.ProfileIdx); err != nil {
		return err
	}
	// 继承运行态：编辑规则不该顺手把宏打开或关掉，也不该丢掉暂停状态。
	runtime := engine.RuntimeState{Enabled: true}
	if old.Engine != nil {
		runtime = old.Engine.RuntimeState()
	}
	if opts.Inherit != nil {
		runtime = *opts.Inherit
	}
	eng.ApplyRuntimeState(runtime)

	if err := store.Save(a.dir, store.ConfigFile(gameID), store.Snapshot{
		Config:     *raw,
		ProfileIdx: opts.ProfileIdx,
		StartMode:  opts.StartMode,
	}); err != nil {
		return err
	}

	slotVKs := slotVKsFromRaw(*raw)
	if opts.SlotVKs != nil {
		slotVKs = *opts.SlotVKs
	}

	if old.Engine != nil {
		old.Engine.SetEnabled(false)
		old.Engine.ReleaseAll()
	}
	a.st.apply(runtimeSnapshot{
		Engine:        eng,
		Raw:           raw,
		ToggleVK:      parsed.ToggleVK,
		GameID:        gameID,
		TargetProcess: target,
		StartMode:     opts.StartMode,
		SmartPause:    a.settings.SmartPause,
		Helpers:       a.settings.Helpers,
		SlotVKs:       slotVKs,
	})
	a.refreshUI()
	if !opts.SkipUIReload && a.win != nil {
		a.win.loadFromState()
	}
	a.log.Printf("已应用 %s 配置：%s（%d 个方案，启停键 %s）", gameDisplayName(gameID), raw.Name, len(parsed.Profiles), keyName(parsed.ToggleVK))
	return nil
}

func (a *app) switchProfile(i int) {
	snap := a.st.snapshot()
	if snap.Engine == nil || snap.Raw == nil {
		return
	}
	if snap.Engine.ProfileIndex() == i {
		return
	}
	if err := snap.Engine.SetProfile(i); err != nil {
		walk.MsgBox(a.owner(), appName, "切换方案失败：\n"+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	if err := store.Save(a.dir, store.ConfigFile(snap.GameID), store.Snapshot{
		Config:     *snap.Raw,
		ProfileIdx: i,
		StartMode:  snap.StartMode,
	}); err != nil {
		walk.MsgBox(a.owner(), appName, "方案已切换，但保存失败（下次启动会回到旧方案）：\n"+err.Error(),
			walk.MsgBoxOK|walk.MsgBoxIconWarning)
	}
	a.refreshUI()
	a.log.Printf("[切换] 方案 → %s", profileDisplayName(snap.Engine, i))
}

// openDataDir 打开配置与日志所在目录（入口在界面的日志浮层里，托盘不再承担操作）。
func (a *app) openDataDir() error {
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return fmt.Errorf("创建目录失败：%v", err)
	}
	if err := exec.Command("explorer", a.dir).Start(); err != nil {
		return fmt.Errorf("打开目录失败：%v", err)
	}
	return nil
}

func (a *app) startTicker() {
	go func() {
		// GUI 程序没有控制台，主循环里一旦 panic，进程会静默消失、只留一条"已开启"的日志。
		// 这里兜住并把堆栈写进日志，下次出问题能直接看到崩在哪一行。
		defer func() {
			if r := recover(); r != nil {
				a.log.Printf("[崩溃] 主循环异常退出：%v\n%s", r, debug.Stack())
			}
		}()
		timer := time.NewTimer(time.Millisecond)
		defer timer.Stop()
		var (
			prevToggle  bool
			lastEnabled bool
			lastProfile = -1
		)
		for {
			<-timer.C
			now := time.Now()
			snap := a.st.snapshot()

			if snap.StartMode == 1 { // 仅按下时 (按住生效)
				down := snap.ToggleVK != 0 && win32.KeyPressed(snap.ToggleVK)
				if down != prevToggle && snap.Engine != nil {
					snap.Engine.SetEnabled(down)
					if down {
						a.log.Printf("[按键] %s 按下 → 宏启动", keyName(snap.ToggleVK))
					} else {
						a.log.Printf("[按键] %s 松开 → 宏停止", keyName(snap.ToggleVK))
					}
				}
				prevToggle = down
			} else { // 懒人模式 (开关切换)
				pressed := snap.ToggleVK != 0 && win32.KeyPressed(snap.ToggleVK)
				if pressed && !prevToggle && snap.Engine != nil {
					a.log.Printf("[热键] %s → %s", keyName(snap.ToggleVK), onOffText(snap.Engine.Toggle()))
				}
				prevToggle = pressed
			}

			// 默认按检测周期醒来看按键；引擎有待发按键时改睡到它的到期时刻。
			next := now.Add(idlePoll)

			if snap.Engine == nil {
				if a.hud != nil {
					a.hud.update(snap, false)
				}
			} else {
				// 智能暂停：聊天 / 开地图 / 回城时自动让路
				a.tickSmartPause(snap)
				// 助手功能（暗黑3）：按快捷键触发，运行中再按一次打断
				a.tickHelper(snap)

				name, ok := win32.ForegroundProcessName()
				foreground := ok && win32.MatchProcess(name, snap.TargetProcess)
				snap.Engine.Tick(now, foreground)

				if a.hud != nil {
					a.hud.update(snap, foreground)
				}

				if enabled := snap.Engine.Enabled(); enabled != lastEnabled || snap.Engine.ProfileIndex() != lastProfile {
					lastEnabled = enabled
					lastProfile = snap.Engine.ProfileIndex()
					a.refreshUIAsync()
				}

				// Tick 之后再问：这样拿到的是最新的到期时刻。
				if due := snap.Engine.NextDue(now); !due.IsZero() && due.Before(next) {
					next = due
				}
			}

			d := next.Sub(now)
			if d < time.Millisecond {
				d = time.Millisecond
			}
			timer.Reset(d)
		}
	}()
}

// smartPauseStopKeys 是智能暂停里「按下即停止宏」的键：回车（聊天）、T（回城）、M（世界地图）。
// 这三个键会让游戏接管输入，此时继续发技能只会误操作。
var smartPauseStopKeys = []uint16{keys.VKEnter, uint16('T'), uint16('M')}

// tickSmartPause 在轮询循环里检测智能暂停的触发键（语义对齐 D3keyHelper）：
// 按 Tab 暂停 / 恢复宏；按回车、T、M 停止宏。
func (a *app) tickSmartPause(snap runtimeSnapshot) {
	if !snap.SmartPause || snap.Engine == nil {
		return
	}
	eng := snap.Engine
	now := time.Now()

	// Tab：切换暂停 / 恢复
	tabDown := win32.KeyPressed(keys.VKTab)
	if tabDown && !a.spTab {
		if eng.Paused() {
			eng.Resume()
			a.log.Printf("[智能暂停] Tab → 恢复宏")
		} else {
			eng.QuickPause(now, 0)
			a.log.Printf("[智能暂停] Tab → 暂停宏")
		}
	}
	a.spTab = tabDown

	// 回车 / T / M：停止宏
	for i, vk := range smartPauseStopKeys {
		down := win32.KeyPressed(vk)
		if down && !a.spPrev[i] && eng.Enabled() {
			eng.SetEnabled(false)
			a.log.Printf("[智能暂停] %s → 停止宏", keyName(vk))
		}
		a.spPrev[i] = down
	}
}

// updateSmartPause 由界面在设置变更后调用：更新运行期快照，无需重建引擎。
func (a *app) updateSmartPause(on bool) {
	snap := a.st.snapshot()
	snap.SmartPause = on
	a.st.apply(snap)
}

// updateHelpers 由界面在助手设置变更后调用：更新运行期快照，无需重建引擎。
func (a *app) updateHelpers(h store.HelperSettings) {
	snap := a.st.snapshot()
	snap.Helpers = h
	a.st.apply(snap)
}

// helperMouse 把 win32 的鼠标能力接入助手运行时。
type helperMouse struct{}

func (helperMouse) Position() (int32, int32, error) { return win32.MousePosition() }
func (helperMouse) Move(x, y int32) error           { return win32.SetMousePos(x, y) }
func (helperMouse) Tap(vk uint16) error             { return win32.Tap(vk) }

// clientLang 依窗口标题判断客户端语言：标题恰为 "Diablo III" 视为英文原版；
// 其余语言（含中文）的魔盒界面元素整体上移，坐标需要相应补偿。
func clientLang(title string) games.D3ClientLang {
	if title == "Diablo III" {
		return games.D3ClientEnglish
	}
	return games.D3ClientOther
}

// tickHelper 检测助手快捷键：按一次触发，运行中再按一次打断。
func (a *app) tickHelper(snap runtimeSnapshot) {
	vk, err := keys.Parse(snap.Helpers.Hotkey)
	if err != nil || vk == 0 {
		a.helperHotkeyDown = false
		return
	}
	if down := win32.KeyPressed(vk); !down {
		a.helperHotkeyDown = false
		return
	}
	if a.helperHotkeyDown {
		return // 只认按下沿，等松开再算下一次
	}
	a.helperHotkeyDown = true

	if a.helperRunning.Load() {
		a.helperAbort.Store(true)
		a.log.Printf("[助手] 收到打断请求，正在停止…")
		return
	}
	a.startHelper(snap)
}

// startHelper 依据当前界面状态挑选并启动一个助手。
func (a *app) startHelper(snap runtimeSnapshot) {
	// 助手目前是暗黑3 专属能力：其它游戏没有对应的界面识别数据。
	if snap.GameID != "d3" {
		a.log.Printf("[助手] 助手目前仅支持暗黑3")
		return
	}
	if snap.Engine == nil {
		return
	}
	// 与技能宏互斥（D3keyHelper 也是这样：助手入口先检查宏是否在跑）。
	if snap.Engine.Enabled() {
		a.log.Printf("[助手] 技能宏正在运行，已忽略（助手与技能宏互斥，请先停宏）")
		return
	}
	fg, err := win32.GetForegroundWindow()
	if err != nil || !win32.MatchProcess(fg.ProcessName, snap.TargetProcess) {
		a.log.Printf("[助手] 游戏不在前台，已忽略")
		return
	}
	w, h, ox, oy, ok := win32.Screen{}.ClientRect(uintptr(fg.Handle))
	if !ok || w <= 0 || h <= 0 {
		a.log.Printf("[助手] 无法读取游戏窗口尺寸（请确认游戏以窗口化运行）")
		return
	}

	env := &helper.Env{
		WinW: w, WinH: h, OriginX: ox, OriginY: oy,
		Lang:    clientLang(fg.Title),
		Delay:   time.Duration(snap.Helpers.DelayMS) * time.Millisecond,
		Dev:     snap.Helpers.DevMode,
		Pixel:   win32.Screen{},
		Mouse:   helperMouse{},
		Aborted: func() bool { return a.helperAbort.Load() },
		Logf:    func(format string, args ...any) { a.log.Printf("[助手] "+format, args...) },
	}

	task, err := pickHelper(env, snap.Helpers)
	if err != nil {
		a.log.Printf("[助手] %v", err)
		return
	}

	a.helperRunning.Store(true)
	a.helperAbort.Store(false)
	a.log.Printf("[助手] 启动：%s", task.Name())
	if snap.Helpers.DevMode {
		a.log.Printf("[助手] 开发者模式开启：客户区 %dx%d @ (%d,%d)，动作间隔 %dms",
			w, h, ox, oy, snap.Helpers.DelayMS)
	}
	go func() {
		defer a.helperRunning.Store(false)
		runErr := task.Run(env)
		if runErr == nil {
			a.log.Printf("[助手] %s 完成", task.Name())
			return
		}
		if _, aborted := runErr.(helper.AbortError); aborted {
			a.log.Printf("[助手] 已中断：%v", runErr)
			return
		}
		a.log.Printf("[助手] %s 失败：%v", task.Name(), runErr)
	}()
}

// pickHelper 按当前界面状态挑选助手（顺序对应 D3keyHelper 的调度器）。
func pickHelper(env *helper.Env, settings store.HelperSettings) (helper.Helper, error) {
	at := env.Sampler()
	if settings.IsEnabled("d3.gamble") && games.D3GambleOpen(at) {
		return helper.D3Gamble{Times: settings.GambleTimes}, nil
	}
	kanai := games.D3KanaiCubeOpen(env.Lang, at)
	if settings.IsEnabled("d3.kanai.reforge") && kanai == games.D3KanaiReforge {
		return helper.D3Reforge{Target: settings.ReforgeTarget}, nil
	}
	if settings.IsEnabled("d3.kanai.upgrade") && kanai == games.D3KanaiUpgrade {
		return helper.D3KanaiUpgrade{}, nil
	}
	if settings.IsEnabled("d3.kanai.convert") && kanai == games.D3KanaiConvert {
		return helper.D3KanaiConvert{}, nil
	}
	if settings.IsEnabled("d3.salvage.quick") {
		if state, _ := games.D3SalvagePageOpen(at); state == games.D3SalvageReady {
			return helper.D3QuickSalvage{}, nil
		}
	}
	// 拾取没有界面条件，放在最后兜底。
	if settings.IsEnabled("d3.loot") {
		return helper.D3Loot{Times: settings.LootTimes}, nil
	}
	return nil, fmt.Errorf("当前界面没有可用的助手：请打开赌博界面、铁匠铺分解页或卡奈魔盒重铸页；" +
		"若只想拾取，请启用「一键拾取」")
}



func (a *app) notifyEngineError(err error) {
	a.log.Printf("[发送错误] %v", err)
	a.notifyMu.Lock()
	skip := time.Since(a.lastNotify) < 10*time.Second
	if !skip {
		a.lastNotify = time.Now()
	}
	a.notifyMu.Unlock()
	if skip {
		return
	}
	a.sync(func() {
		if a.ni != nil {
			if showErr := a.ni.ShowError(appName, "按键发送失败：\n"+err.Error()); showErr != nil {
				a.log.Printf("[托盘] 错误提示失败：%v", showErr)
			}
		}
	})
}

// setupBuffChecker 为引擎注入屏幕 Buff 状态检测器：入参为技能键，返回该键的 Buff 是否仍在生效。
// 引擎只在声明了「保持Buff」策略的规则上调用它（并据此做优先级仲裁）；
// 具体识别算法由游戏适配器提供（暗黑3 为发光环取色，其余游戏暂无守卫恒返回 false），
// 通用层不再出现游戏标识判断。
func (a *app) setupBuffChecker(eng *engine.Engine) {
	if eng == nil {
		return
	}
	eng.SetBuffChecker(func(vk uint16) bool {
		snap := a.st.snapshot()
		adapter, ok := games.For(snap.GameID)
		if !ok {
			return false
		}
		fg, err := win32.GetForegroundWindow()
		if err != nil {
			return false
		}
		if !win32.MatchProcess(fg.ProcessName, snap.TargetProcess) {
			return false
		}
		profIdx := eng.ProfileIndex()
		if profIdx < 0 || profIdx >= maxProfiles {
			return false
		}
		// 只读运行期快照里的槽位键：不再读界面控件状态（那是 UI 线程的数据，会在 tick 线程形成竞争）。
		slotIdx := -1
		for s := 0; s < numSlots; s++ {
			if snap.SlotVKs[profIdx][s] != 0 && snap.SlotVKs[profIdx][s] == vk {
				slotIdx = s
				break
			}
		}
		if slotIdx < 0 {
			return false
		}
		// 由游戏适配器判定该槽位技能的 Buff 是否仍在生效（无守卫的游戏恒为 false）。
		return adapter.BuffActive(uintptr(fg.Handle), slotIdx, win32.Screen{})
	})
}

// setupForceStand 依据本机按键映射设置与游戏适配器，为引擎注入「点击前先按住强制站立键」。
// 设置来自每游戏按键映射（不随分享码分发）：玩家可自定义键位，留空则用该游戏默认值。
func (a *app) setupForceStand(eng *engine.Engine) {
	if eng == nil {
		return
	}
	gameID := a.settings.CurrentGame
	gk := a.settings.GameKeyMap(gameID)
	adapter, ok := games.For(gameID)
	if !ok || !gk.ForceStandEnabled {
		eng.SetForceStand(0, 0)
		return
	}
	targets := adapter.ForceStandKeys()
	if len(targets) == 0 {
		// 该游戏尚未启用这一机制（未实测），保持关闭。
		eng.SetForceStand(0, 0)
		return
	}
	keyName := gk.ForceStandKey
	if keyName == "" {
		keyName = adapter.DefaultForceStandKey()
	}
	modVK, err := keys.Parse(keyName)
	if err != nil || modVK == 0 {
		a.log.Printf("[设置] 强制站立键 %q 无效，已跳过", keyName)
		eng.SetForceStand(0, 0)
		return
	}
	applyVK, err := keys.Parse(targets[0])
	if err != nil || applyVK == 0 {
		eng.SetForceStand(0, 0)
		return
	}
	eng.SetForceStand(applyVK, modVK)
}

// setTargetProcess 更新某游戏的目标进程名（空字符串表示恢复默认），并同步运行期状态。
func (a *app) setTargetProcess(gameID, proc string) error {
	if a.settings.Processes == nil {
		a.settings.Processes = map[string]string{}
	}
	if proc == "" {
		delete(a.settings.Processes, gameID)
	} else {
		a.settings.Processes[gameID] = proc
	}
	if err := store.SaveSettings(a.dir, *a.settings); err != nil {
		return err
	}
	snap := a.st.snapshot()
	if snap.GameID == gameID {
		resolved, err := a.settings.TargetProcess(gameID)
		if err != nil {
			return err
		}
		snap.TargetProcess = resolved
		a.st.apply(snap)
	}
	return nil
}

func (a *app) targetProcessOf(gameID string) string {
	p, err := a.settings.TargetProcess(gameID)
	if err != nil {
		return ""
	}
	return p
}

// gameInForeground 判断目标游戏当前是否在前台（供界面状态展示使用）。
func (a *app) gameInForeground(snap runtimeSnapshot) bool {
	fg, err := win32.GetForegroundWindow()
	return err == nil && win32.MatchProcess(fg.ProcessName, snap.TargetProcess)
}

func (a *app) describeConfig(raw *config.Raw, parsed *config.Parsed) string {
	var sb strings.Builder
	if parsed.Game != "" {
		sb.WriteString("游戏：" + gameDisplayName(parsed.Game) + "\n")
	}
	if raw.Name != "" {
		sb.WriteString("配置：" + raw.Name + "\n")
	}
	if raw.Author != "" {
		sb.WriteString("作者：" + raw.Author + "\n")
	}
	sb.WriteString("启停键：" + keyName(parsed.ToggleVK) + "\n")
	for i, prof := range parsed.Profiles {
		name := prof.Name
		if name == "" {
			name = fmt.Sprintf("方案 %d", i+1)
		}
		mark := ""
		if i == 0 {
			mark = "（默认启用）"
		}
		sb.WriteString(fmt.Sprintf("\n%s%s\n", name, mark))
		for _, r := range prof.Rules {
			sb.WriteString("  · " + describeRule(r) + "\n")
		}
	}
	sb.WriteString("\n提示：只有目标游戏位于前台时才会发送按键。")
	return sb.String()
}

func describeRule(r config.ParsedRule) string {
	trigger := "宏开启期间"
	if r.HoldVK != 0 {
		trigger = "按住 " + keyName(r.HoldVK) + " 时"
	}
	switch r.Kind {
	case config.ActionTap:
		return fmt.Sprintf("%s，每 %s 连点一次 %s", trigger, durationText(r.IntervalMS), keyName(r.Keys[0]))
	case config.ActionSeq:
		return fmt.Sprintf("%s，循环按 %s（每 %s 一次）", trigger, joinKeyLabels(r.Keys, " → "), durationText(r.IntervalMS))
	case config.ActionOnce:
		return fmt.Sprintf("%s，点击一次 %s", trigger, keyName(r.Keys[0]))
	case config.ActionHold:
		return fmt.Sprintf("%s，保持按住 %s 不放", trigger, keyName(r.Keys[0]))
	}
	panic("未知动作类型：" + string(r.Kind))
}

func durationText(ms int) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms%1000 == 0:
		return fmt.Sprintf("%d 秒", ms/1000)
	default:
		return fmt.Sprintf("%.1f 秒", float64(ms)/1000)
	}
}

func joinKeyLabels(vks []uint16, sep string) string {
	labels := make([]string, 0, len(vks))
	for _, vk := range vks {
		labels = append(labels, keyName(vk))
	}
	return strings.Join(labels, sep)
}

func profileDisplayName(eng *engine.Engine, idx int) string {
	if name := eng.ProfileName(idx); name != "" {
		return name
	}
	return fmt.Sprintf("方案 %d", idx+1)
}

func gameDisplayName(id string) string {
	if g, ok := games.Lookup(id); ok {
		return g.Name
	}
	return id
}

func onOffText(on bool) string {
	if on {
		return "已开启"
	}
	return "已关闭"
}

// defaultConfig 是新建配置的起手模板：固定 4 个方案，开箱即用。
// defaultConfig 是新游戏的起始配置：只有一个空方案，其余方案由玩家在界面 tab 尾部点「+」新增。
// 刻意不做「内置方案预设」：每套 BD 用哪些技能、技能放在哪个键位都不确定，
// 预设只会逼玩家先删掉一堆用不上的配置；空方案在玩家填好技能前不按任何键。
func defaultConfig() config.Raw {
	return config.Raw{
		Version: config.Version,
		Profiles: []config.RawProfile{{
			Name:  "配置 1",
			Rules: []config.RawRule{},
		}},
	}
}
