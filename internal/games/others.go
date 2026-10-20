package games

// standardForceStandKey 是暗黑3/4 共有的「强制站立 / 原地站立」默认键。
const standardForceStandKey = "SHIFT"

// 暗黑破坏神4 的适配器。
// 暂未启用「左键自动强制站立」——机制与暗黑3 相同，但尚未在实机验证，不擅自猜测行为。
// 后续需要接入屏幕识别或助手功能时在此实现，不污染通用层。

type d4Adapter struct{}

func (d4Adapter) ID() string                                { return "d4" }
func (d4Adapter) Name() string                              { return "暗黑破坏神4" }
func (d4Adapter) DefaultProcess() string                    { return "Diablo IV.exe" }
func (d4Adapter) Slots() []SlotDef                          { return standardSlots() }
func (d4Adapter) DefaultForceStandKey() string              { return standardForceStandKey }
func (d4Adapter) ForceStandKeys() []string                  { return nil } // 待实测后再启用
func (d4Adapter) BuffActive(uintptr, int, PixelSource) bool { return false }

// standardSlots 是暗黑3/4 通用的 6 技能槽默认定义。
func standardSlots() []SlotDef {
	return []SlotDef{
		{Name: "技能一：", DefaultKey: "1"},
		{Name: "技能二：", DefaultKey: "2"},
		{Name: "技能三：", DefaultKey: "3"},
		{Name: "技能四：", DefaultKey: "4"},
		{Name: "左键技能：", DefaultKey: "LMB"},
		{Name: "右键技能：", DefaultKey: "RMB"},
	}
}
