package app

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type servicePopoverState struct {
	PopoverOpen       bool
	popoverBtnHover   bool
	popoverPanelHover bool
	panelBounds       ui.Rect
	intent            *kitui.HoverIntent
}

func (s *servicePopoverState) hoverIntent() *kitui.HoverIntent {
	if s.intent == nil {
		s.intent = kitui.DefaultHoverIntent()
	}
	return s.intent
}
