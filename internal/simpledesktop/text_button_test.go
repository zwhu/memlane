package simpledesktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTextButtonKeepsKeyboardActivationAndDisabledState(t *testing.T) {
	clicks, disabled := 0, false
	tt := ui.NewTester(func(c *ui.Context) {
		textButton(c, "查看项目", func() { clicks++ }).Disabled(disabled)
	}, 300, 100)
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("查看项目") {
		t.Fatal("text action is not keyboard focusable")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Key(0, ui.KeySpace)
	if clicks != 2 {
		t.Fatal("keyboard activation was lost", clicks)
	}
	disabled = true
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Click("查看项目")
	if clicks != 2 {
		t.Fatal("disabled text action ran", clicks)
	}
}
