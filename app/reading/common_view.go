package reading

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func ConfirmDiscard(c *ui.Context, tokens *kitui.Tokens, open *bool, confirm func()) {
	kitui.Dialog(c, tokens, open, kitui.DialogOptions{
		Key: "common-discard-draft", Title: "放弃未保存的草稿？", Width: kitui.DialogSmall,
		Danger: true, CancelText: "继续编辑", ConfirmText: "放弃并返回", OnConfirm: confirm,
		Body: func() {
			ui.Text(c, "草稿尚未保存，放弃后将清除本次编辑。").Font(kitui.FontUI).FontSize(kitui.FSBody).TextColor(tokens.Text2).LineHeight(1.6)
		},
	})
}

func ConfirmDelete(c *ui.Context, tokens *kitui.Tokens, open *bool, id string, busy bool, cancel, confirm func()) {
	kitui.Dialog(c, tokens, open, kitui.DialogOptions{
		Key: "common-delete-confirm-" + id, Title: "删除这条通用记忆？", Width: kitui.DialogSmall,
		Danger: true, CancelText: "保留记忆", ConfirmText: "确认删除此条", ConfirmDisabled: busy,
		OnCancel: cancel, OnConfirm: confirm,
		Body: func() {
			ui.Text(c, "官方确认后将移除记忆及关联索引。此操作无法在客户端撤销。").Font(kitui.FontUI).FontSize(kitui.FSBody).TextColor(tokens.Text2).LineHeight(1.6)
		},
	})
}
