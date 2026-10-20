//go:build windows

package main

import (
	"strings"

	"github.com/lxn/walk"
	decl "github.com/lxn/walk/declarative"
)

// showLogWindow 打开日志窗口（单实例）；已打开时只刷新内容。
func (a *app) showLogWindow() {
	if a.logDlg != nil {
		a.refreshLogWindow()
		return
	}
	var dlg *walk.Dialog
	var te *walk.TextEdit
	err := decl.Dialog{
		AssignTo: &dlg,
		Title:    appName + " v" + version + " · 日志",
		Icon:     a.icon,
		MinSize:  decl.Size{Width: 860, Height: 480},
		Layout:   decl.VBox{},
		Children: []decl.Widget{
			decl.Label{Text: "日志文件：" + a.log.FilePath()},
			decl.TextEdit{AssignTo: &te, ReadOnly: true, VScroll: true, MinSize: decl.Size{Width: 820, Height: 380}},
			decl.Composite{
				Layout: decl.HBox{},
				Children: []decl.Widget{
					decl.PushButton{Text: "复制全部", OnClicked: func() {
						text := strings.Join(a.log.Lines(), "\r\n")
						if err := walk.Clipboard().SetText(text); err != nil {
							walk.MsgBox(dlg, appName, "复制失败：\n"+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
							return
						}
						a.log.Printf("[日志窗口] 已复制 %d 行到剪贴板", len(a.log.Lines()))
					}},
					decl.PushButton{Text: "刷新", OnClicked: func() { a.refreshLogWindow() }},
					decl.PushButton{Text: "打开数据目录", OnClicked: func() { a.openDataDir() }},
					decl.PushButton{Text: "清空显示", OnClicked: func() {
						a.log.Clear()
						a.refreshLogWindow()
					}},
					decl.HSpacer{},
					decl.PushButton{Text: "关闭", OnClicked: func() { dlg.Accept() }},
				},
			},
		},
	}.Create(a.mw)
	if err != nil {
		a.log.Printf("[日志窗口] 打开失败：%v", err)
		walk.MsgBox(a.mw, appName, "打开日志窗口失败：\n"+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	a.logDlg = dlg
	a.logTe = te
	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		*canceled = false
		a.logDlg = nil
		a.logTe = nil
	})
	dlg.Show()
	a.refreshLogWindow()
}

func (a *app) refreshLogWindow() {
	if a.logTe == nil {
		return
	}
	lines := a.log.Lines()
	text := strings.Join(lines, "\r\n")
	if err := a.logTe.SetText(text); err != nil {
		a.log.Printf("[日志窗口] 刷新失败：%v", err)
		return
	}
	a.logTe.SetTextSelection(len(text), len(text))
}
