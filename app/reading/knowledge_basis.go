package reading

import "strings"

type BasisField struct{ Name, Value string }

func BasisFields(direct bool, meta map[string]any) []BasisField {
	if direct {
		return []BasisField{{"保存方式", "直接保存"}}
	}
	value := func(key string) string {
		s, _ := meta[key].(string)
		return strings.TrimSpace(s)
	}
	kind := value("lesson_type")
	label := map[string]string{
		"state": "项目状态", "pitfall": "踩坑经验", "convention": "项目约定",
		"location": "代码位置与关联", "change": "代码变更", "constraint": "约束条件", "other": "其他",
	}[kind]
	if label == "" {
		label = "未记录"
		if kind != "" {
			label = "未识别分类（" + kind + "）"
		}
	}
	fields := []BasisField{{"记录分类", label}}
	key := value("state_key")
	if kind == "state" && key == "test-baseline" {
		fields = append(fields, BasisField{"分类规则", "测试基线：官方规则允许将测试命令、通过数和失败数保存为项目状态。"})
	} else if kind == "state" && strings.HasPrefix(key, "footprint:") && len(key) > len("footprint:") {
		fields = append(fields, BasisField{"分类规则", "功能现状：记录该功能当前涉及的实现范围。"})
	}
	why := value("why_learned")
	if why == "" {
		why = "未记录"
	}
	return append(fields, BasisField{"学习情境", why}, BasisField{"具体接受理由", "未记录"})
}

// CategoryLabel returns the official category label for filtering.
func CategoryLabel(fields []BasisField) string {
	if len(fields) == 0 {
		return ""
	}
	return fields[0].Value
}
