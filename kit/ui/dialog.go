package ui

import (
	mygoui "github.com/egoist/mygo/ui"
)

const (
	DialogSmall float32 = 480
	DialogLarge float32 = 640
)

// DialogOptions configures a V4 modal dialog built on top of mygoui.Modal.
// Confirm/delete dialogs use DialogSmall (480); forms/details use DialogLarge (640).
// Danger dialogs render a solid danger primary button and explicitly consume Enter as a no-op.
type DialogOptions struct {
	Key             string
	Title           string
	Width           float32
	Label           string
	Leading         func()
	Body            func()
	ScrollBody      bool
	Hint            string
	CancelText      string
	ConfirmText     string
	ConfirmLabel    string
	CloseLabel      string
	HideCancel      bool
	HideConfirm     bool
	Danger          bool
	CancelDisabled  bool
	ConfirmDisabled bool
	Closable        *bool
	ExtraFooter     func()
	OnCancel        func()
	OnConfirm       func()
}

// Dialog renders a unified V4 modal dialog backed by mygoui.Modal.
func Dialog(c *mygoui.Context, tok *Tokens, open *bool, opts DialogOptions) mygoui.Element {
	if open == nil || !*open {
		return mygoui.Element{}
	}
	key := opts.Key
	if key == "" {
		key = "v4-dialog-" + opts.Title
	}
	width := opts.Width
	if width <= 0 {
		width = DialogSmall
	}
	closable := true
	if opts.Closable != nil {
		closable = *opts.Closable
	}
	cancelText := opts.CancelText
	if cancelText == "" {
		cancelText = "取消"
	}
	confirmText := opts.ConfirmText
	if confirmText == "" {
		confirmText = "确定"
	}
	label := opts.Label
	if label == "" {
		label = opts.Title
	}

	handleCancel := func() {
		if !closable || opts.CancelDisabled {
			return
		}
		*open = false
		if opts.OnCancel != nil {
			opts.OnCancel()
		}
	}
	handleConfirm := func() {
		if opts.ConfirmDisabled {
			return
		}
		if opts.OnConfirm != nil {
			opts.OnConfirm()
		}
	}

	_, winH := c.Size()
	maxBodyH := winH - 220
	if maxBodyH < 180 {
		maxBodyH = 180
	}

	modal := mygoui.Modal(c.Key(key), open, func() {
		// Header
		if opts.Title != "" || opts.Leading != nil || closable {
			mygoui.Row(c).FillWidth().Padding(Sp4, Sp5, Sp3, Sp5).Gap(Sp2).AlignItems(mygoui.Center).
				BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
				if opts.Leading != nil {
					mygoui.Row(c).Shrink(0).AlignItems(mygoui.Center).Justify(mygoui.Center).Children(opts.Leading)
				}
				if opts.Title != "" {
					mygoui.Text(c, opts.Title).Font("system-ui").FontSize(FSHeading).FontWeight(600).TextColor(tok.Text1).Grow(1).MinWidth(0)
				} else {
					mygoui.Spacer(c)
				}
				if closable {
					closeLbl := opts.CloseLabel
					if closeLbl == "" {
						closeLbl = "关闭" + opts.Title
					}
					closeBtn := mygoui.ButtonBase(c.Key(key+"-close")).Role(mygoui.RoleButton).Label(closeLbl).
						Cursor(mygoui.CursorPointer).Size(26, 26).Radius(Radius).BorderWidth(0).
						AlignItems(mygoui.Center).Justify(mygoui.Center).Shrink(0).Disabled(opts.CancelDisabled)
					closeBtn.Background(HoverColor(closeBtn, "dlg-close-bg", mygoui.Transparent, tok.Hover))
					ButtonPress(closeBtn, 0.92)
					icoCol := tok.Text3
					if closeBtn.Hovered() {
						icoCol = tok.Text1
					}
					closeBtn.Children(func() {
						mygoui.Icon(c, IconClose).FontSize(14).TextColor(icoCol)
					}).OnClick(handleCancel)
				}
			})
		}

		// Body
		if opts.Body != nil {
			if opts.ScrollBody {
				mygoui.Scroll(c.Key(key + "-scroll")).FillWidth().MaxHeight(maxBodyH).Children(func() {
					mygoui.Column(c).FillWidth().Padding(Sp4, Sp5).Gap(Sp3).Children(opts.Body)
				})
			} else {
				mygoui.Column(c).FillWidth().Padding(Sp4, Sp5).Gap(Sp3).Children(opts.Body)
			}
		}

		// Footer
		showFooter := !opts.HideCancel || !opts.HideConfirm || opts.Hint != "" || opts.ExtraFooter != nil
		if showFooter {
			mygoui.Row(c).FillWidth().Padding(Sp3, Sp5).Gap(Sp2).AlignItems(mygoui.Center).
				BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
				if opts.Hint != "" {
					mygoui.Text(c, opts.Hint).Font("system-ui").FontSize(FSCaption).FontWeight(400).TextColor(tok.Text3).Grow(1).MinWidth(0)
				} else {
					mygoui.Spacer(c)
				}
				if !opts.HideCancel {
					OutlineButton(c, tok, cancelText, nil, handleCancel).Disabled(opts.CancelDisabled)
				}
				if opts.ExtraFooter != nil {
					opts.ExtraFooter()
				}
				if !opts.HideConfirm {
					var confirmBtn mygoui.Element
					if opts.Danger {
						confirmBtn = DangerButton(c, tok, confirmText, handleConfirm)
					} else {
						confirmBtn = PrimaryButton(c, tok, confirmText, handleConfirm)
					}
					if opts.ConfirmLabel != "" {
						confirmBtn.Label(opts.ConfirmLabel)
					}
					confirmBtn.Disabled(opts.ConfirmDisabled)
				}
			})
		}
	}).Width(width).Padding(0).Gap(0).Radius(RadiusMenu).Background(tok.BgElevated).
		Border(1, tok.Border).Shadow(0, 12, 32, 0, tok.ShadowLift).Transition(DialogTransition())
	if label != "" {
		modal.Label(label)
	}

	if modal.Dismissed() {
		if !closable || opts.CancelDisabled {
			*open = true
		} else if opts.OnCancel != nil {
			opts.OnCancel()
		}
	} else if opts.Danger {
		_ = modal.Shortcut(0, mygoui.KeyEnter)
	} else if !opts.HideConfirm && !opts.ConfirmDisabled && opts.OnConfirm != nil && modal.Shortcut(0, mygoui.KeyEnter) {
		handleConfirm()
	}

	return modal
}
