package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	"time"
)

// The SDK owns focus, dismissal, theme and reduced-motion handling. This shared
// transition expresses opening only; it never advances a request or marks success.
func projectOverlayMotion() ui.ElementTransition {
	return ui.ElementTransition{Duration: 160 * time.Millisecond, Ease: ui.EaseOut, Enter: &ui.Motion{Y: 8, Scale: .98}}
}
