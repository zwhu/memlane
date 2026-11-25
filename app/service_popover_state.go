package app

import "time"

type servicePopoverState struct {
	PopoverOpen       bool
	popoverBtnHover   bool
	popoverBtnFocus   bool
	popoverClicked    bool
	popoverPanelHover bool
	popoverShowTimer  *time.Timer
	popoverHideTimer  *time.Timer
}
