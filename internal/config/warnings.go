package config

import (
	"fmt"

	"github.com/imsxx/diablo-lazy-macro/internal/keys"
)

// Warnings 返回配置中的可疑项（不阻断导入与保存，仅用于提示）：
//
//  1. 同一个键既是触发键（启停键、按住触发键或方案切换键）又是动作键，会形成自我触发叠加
//  2. 同一方案切换键与启停键相同
//  3. 多个方案共用同一个切换键（同一时刻只有其中一个能生效）
func Warnings(p *Parsed) []string {
	triggerRoles := map[uint16]string{}
	if p.ToggleVK != 0 {
		triggerRoles[p.ToggleVK] = "启停键"
	}

	for _, prof := range p.Profiles {
		for _, r := range prof.Rules {
			if r.HoldVK == 0 {
				continue
			}
			if _, exists := triggerRoles[r.HoldVK]; !exists {
				triggerRoles[r.HoldVK] = "触发键"
			}
		}
	}

	var warnings []string

	// 触发键与动作键重叠
	seen := map[uint16]bool{}
	for _, prof := range p.Profiles {
		for _, r := range prof.Rules {
			for _, vk := range r.Keys {
				role, isTrigger := triggerRoles[vk]
				if !isTrigger || seen[vk] {
					continue
				}
				seen[vk] = true
				warnings = append(warnings, fmt.Sprintf("键 %s 同时用作%s与动作键，会形成自我触发叠加，建议分开配置", keys.Label(vk), role))
			}
		}
	}
	return warnings
}
