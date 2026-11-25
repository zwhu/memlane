package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/reading"
)

func knowledgeBasisFields(i Item) []ReadingField {
	if i.Kind != "知识" || i.Document == nil || !i.Document.knowledge() || commonCategory(*i.Document) != "" {
		return nil
	}
	basis := reading.BasisFields(i.Document.directMemory(), i.Document.Metadata)
	fields := make([]ReadingField, len(basis))
	for n, field := range basis {
		fields[n] = ReadingField(field)
	}
	return fields
}

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
