package desktop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestConversationToolParametersDisplayLiteralStringsAndExactNumbers(t *testing.T) {
	a := New("synthetic", nil)
	_, view := conversationTestState(a)
	view.Tools = true
	command := "printf '<first>\\n'\npython3 - <<'PY'\nprint(\"```literal```\")\nPY"
	raw, err := json.Marshal(map[string]any{"command": command, "count": json.Number("9007199254740993")})
	if err != nil {
		t.Fatal(err)
	}
	view.Detail.Traces[0].Params = raw
	tt := ui.NewTester(a.View, 800, 620)
	if !tt.HasText(command) || !tt.HasText("9007199254740993") {
		t.Fatal("tool values escaped, rounded, or interpreted as markup")
	}
	if err = tt.Click("复制已载入内容"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != conversationCopy(*view.Detail) {
		t.Fatal("display formatting changed original copy")
	}
}

func TestConversationToolHorizontalOffsetSurvivesRefresh(t *testing.T) {
	a := New("synthetic", nil)
	_, view := conversationTestState(a)
	view.Tools = true
	view.Detail.Traces[0].Params, _ = json.Marshal(map[string]string{"command": strings.Repeat("long-command ", 100)})
	tt := ui.NewTester(a.View, 800, 620)
	var selected *ui.ScrollState
	for _, scroll := range view.TraceScroll {
		if scroll.MaxX > 60 {
			selected = scroll
			break
		}
	}
	if selected == nil {
		t.Fatal("long command has no independent horizontal range")
	}
	selected.X = 60
	tt.Frame()
	view.ErrorCode = "timeout"
	tt.Frame()
	if selected.X != 60 {
		t.Fatal("background failure reset horizontal reading position", selected.X)
	}
}
