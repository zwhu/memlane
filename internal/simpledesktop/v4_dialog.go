package simpledesktop

import "github.com/egoist/mygo/ui"

const (
	v4DialogSmall float32 = 480
	v4DialogLarge float32 = 640
)

// v4DialogOptions configures a V4 modal dialog built on top of ui.Modal.
// Confirm/delete dialogs use v4DialogSmall (480); forms/details use v4DialogLarge (640).
// Danger dialogs render a solid danger primary button and intentionally do NOT bind Enter.
type v4DialogOptions struct {
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

// v4Dialog renders a unified V4 modal dialog backed by ui.Modal.
func v4Dialog(c *ui.Context, open *bool, opts v4DialogOptions) ui.Element {
	if open == nil || !*open {
		return ui.Element{}
	}
	key := opts.Key
	if key == "" {
		key = "v4-dialog-" + opts.Title
	}
	width := opts.Width
	if width <= 0 {
		width = v4DialogSmall
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

	modal := ui.Modal(c.Key(key), open, func() {
		// Header
		if opts.Title != "" || opts.Leading != nil || closable {
			ui.Row(c).FillWidth().Padding(sp4, sp5, sp3, sp5).Gap(sp2).AlignItems(ui.Center).
				BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
				if opts.Leading != nil {
					ui.Row(c).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(opts.Leading)
				}
				if opts.Title != "" {
					ui.Text(c, opts.Title).Font("system-ui").FontSize(fsHeading).FontWeight(600).TextColor(tok.Text1).Grow(1).MinWidth(0)
				} else {
					ui.Spacer(c)
				}
				if closable {
					closeLbl := opts.CloseLabel
					if closeLbl == "" {
						closeLbl = "关闭" + opts.Title
					}
					closeBtn := ui.ButtonBase(c.Key(key + "-close")).Role(ui.RoleButton).Label(closeLbl).
						Cursor(ui.CursorPointer).Size(26, 26).Radius(radius).BorderWidth(0).
						AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Disabled(opts.CancelDisabled)
					closeBtn.Background(homeHoverColor(closeBtn, "dlg-close-bg", ui.Transparent, tok.Hover))
					v4ButtonPress(closeBtn, 0.92)
					icoCol := tok.Text3
					if closeBtn.Hovered() {
						icoCol = tok.Text1
					}
					closeBtn.Children(func() {
						ui.Icon(c, v4IconClose).FontSize(14).TextColor(icoCol)
					}).OnClick(handleCancel)
				}
			})
		}

		// Body
		if opts.Body != nil {
			if opts.ScrollBody {
				ui.Scroll(c.Key(key + "-scroll")).FillWidth().MaxHeight(maxBodyH).Children(func() {
					ui.Column(c).FillWidth().Padding(sp4, sp5).Gap(sp3).Children(opts.Body)
				})
			} else {
				ui.Column(c).FillWidth().Padding(sp4, sp5).Gap(sp3).Children(opts.Body)
			}
		}

		// Footer
		showFooter := !opts.HideCancel || !opts.HideConfirm || opts.Hint != "" || opts.ExtraFooter != nil
		if showFooter {
			ui.Row(c).FillWidth().Padding(sp3, sp5).Gap(sp2).AlignItems(ui.Center).
				BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
				if opts.Hint != "" {
					ui.Text(c, opts.Hint).Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).Grow(1).MinWidth(0)
				} else {
					ui.Spacer(c)
				}
				if !opts.HideCancel {
					v4OutlineButton(c, cancelText, nil, handleCancel).Disabled(opts.CancelDisabled)
				}
				if opts.ExtraFooter != nil {
					opts.ExtraFooter()
				}
				if !opts.HideConfirm {
					var confirmBtn ui.Element
					if opts.Danger {
						confirmBtn = v4DangerButton(c, confirmText, handleConfirm)
					} else {
						confirmBtn = v4PrimaryButton(c, confirmText, handleConfirm)
					}
					if opts.ConfirmLabel != "" {
						confirmBtn.Label(opts.ConfirmLabel)
					}
					confirmBtn.Disabled(opts.ConfirmDisabled)
				}
			})
		}
	}).Width(width).Padding(0).Gap(0).Radius(radiusMenu).Background(tok.BgElevated).
		Border(1, tok.Border).Shadow(0, 12, 32, 0, tok.ShadowLift).Transition(v4DialogTransition())
	if label != "" {
		modal.Label(label)
	}

	if modal.Dismissed() {
		if !closable || opts.CancelDisabled {
			*open = true
		} else if opts.OnCancel != nil {
			opts.OnCancel()
		}
	} else if !opts.Danger && !opts.HideConfirm && !opts.ConfirmDisabled && opts.OnConfirm != nil && modal.Shortcut(0, ui.KeyEnter) {
		handleConfirm()
	}

	return modal
}
