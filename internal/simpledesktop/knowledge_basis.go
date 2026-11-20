package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func knowledgeBasisFields(i Item) []ReadingField {
	if i.Kind != "知识" || i.Document == nil || !i.Document.knowledge() || commonCategory(*i.Document) != "" {
		return nil
	}
	if i.Document.directMemory() {
		return []ReadingField{{"保存方式", "直接保存"}}
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

// knowledgeCategoryLabel returns the official category label for filtering.
func knowledgeCategoryLabel(i Item) string {
	fields := knowledgeBasisFields(i)
	if len(fields) == 0 {
		return ""
	}
	return fields[0].Value
}

func knowledgeBasis(c *ui.Context, i Item) {
	fields := knowledgeBasisFields(i)
	if len(fields) <= 1 {
		return
	}
	// V4: hairline divider section instead of filled gray card.
	ui.Column(c.Key("knowledge-basis-"+i.key())).FillWidth().Gap(6).Margin(sp4, 0, 0, 0).Padding(sp4, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
			ui.Icon(c, v4IconInfo).FontSize(14).TextColor(tok.Text3).Shrink(0)
			ui.Text(c, "记忆点").Font("system-ui").FontSize(fsFootnote).FontWeight(600).TextColor(tok.Text1)
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
			homeText(c, text, homeBody).MinWidth(0)
		}
		if len(missing) > 0 {
			homeText(c, strings.Join(missing, "、")+"未记录", homeMeta)
		}
	})
}

func knowledgeBasisTag(c *ui.Context, i Item) {
	if fields := knowledgeBasisFields(i); len(fields) > 0 {
		v4CategoryChip(c, fields[0].Value)
	}
}
