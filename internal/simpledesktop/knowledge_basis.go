package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// Explain recorded classification, never reconstruct a model's acceptance decision.
func knowledgeBasisFields(i Item) []ReadingField {
	if i.Kind != "知识" || i.Document == nil || !i.Document.knowledge() || commonCategory(*i.Document) != "" {
		return nil
	}
	meta := i.Document.Metadata
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
	fields := []ReadingField{{"记录分类", label}}
	key := value("state_key")
	if kind == "state" && key == "test-baseline" {
		fields = append(fields, ReadingField{"分类规则", "测试基线：官方规则允许将测试命令、通过数和失败数保存为项目状态。"})
	} else if kind == "state" && strings.HasPrefix(key, "footprint:") && len(key) > len("footprint:") {
		fields = append(fields, ReadingField{"分类规则", "功能现状：记录该功能当前涉及的实现范围。"})
	}
	why := value("why_learned")
	if why == "" {
		why = "未记录"
	}
	return append(fields, ReadingField{"学习情境", why}, ReadingField{"具体接受理由", "未记录"})
}

func knowledgeBasis(c *ui.Context, i Item) {
	fields := knowledgeBasisFields(i)
	if len(fields) == 0 {
		return
	}
	ui.Column(c.Key("knowledge-basis-"+i.key())).FillWidth().Gap(8).Padding(12, 14).Background(ui.Hex("#f6f8fa")).Radius(6).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			homeText(c, "记忆点", homeBody).FontSize(12).FontWeight(500)
			homeText(c, fields[0].Value, homeMeta).FontSize(11).Padding(3, 8).Background(ui.Hex("#e8eef4")).Radius(4)
		})
		missing := []string{}
		for _, field := range fields[1:] {
			if field.Value == "未记录" {
				missing = append(missing, strings.TrimPrefix(field.Name, "具体"))
				continue
			}
			text := field.Value
			if field.Name == "学习情境" {
				text = "学习情境：" + text
			}
			homeText(c, text, homeBody).FontSize(12).MinWidth(0)
		}
		if len(missing) > 0 {
			homeText(c, strings.Join(missing, "、")+"未记录", homeMeta).FontSize(11)
		}
	})
}
