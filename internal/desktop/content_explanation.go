package desktop

import (
	_ "embed"
	"sync"

	"github.com/egoist/mygo/ui"
)

// Original prototype SVGs are rendered at 2x because MyGo omits SVG text.
// Source digests are in mvp-content-diagram-sources-20261111.json.
//
//go:embed assets/content-relationship-art.png
var contentRelationshipArt []byte

//go:embed assets/content-relationship-story.png
var contentRelationshipStory []byte

var contentDiagramOnce sync.Once
var contentDiagramImages [2]*ui.Bitmap

type ContentExplanationUI struct {
	Open, Example, CogneeOpen, ReturnFocus bool
	Project                                string
}

func contentDiagram(example bool) *ui.Bitmap {
	contentDiagramOnce.Do(func() {
		for i, data := range [][]byte{contentRelationshipArt, contentRelationshipStory} {
			image, err := ui.DecodeBitmap(data)
			if err != nil {
				panic(err)
			}
			contentDiagramImages[i] = image
		}
	})
	if example {
		return contentDiagramImages[1]
	}
	return contentDiagramImages[0]
}

func (a *App) contentExplanationEntry(c *ui.Context) {
	s := &a.contentExplanation
	button := quietButton(c, "记忆是怎么来的？").Key("content-explanation-open").OnClick(func() {
		s.Open, s.CogneeOpen, s.Project = true, false, a.scope
	})
	if s.ReturnFocus && s.Project == a.scope {
		button.Focus()
		s.ReturnFocus = false
	}
}

func (a *App) closeContentExplanation() {
	a.contentExplanation.Open = false
	a.contentExplanation.ReturnFocus = true
}

func (a *App) contentExplanationView(c *ui.Context) {
	s := &a.contentExplanation
	if !s.Open {
		return
	}
	if a.page != "项目" || a.scope != s.Project {
		s.Open, s.ReturnFocus = false, false
		return
	}
	w, h := c.Size()
	width := min(float32(820), w-48)
	dialog := ui.DialogBase(c, &s.Open, func(back, panel ui.Element) {
		back.Background(ui.RGBA(36, 55, 77, .22))
		panel.Column().Key("content-explanation-dialog").Label("记忆说明").Width(width).Height(min(float32(680), h-48)).Radius(22).Clip().Border(1, ui.Hex("#dce5ef")).Background(ui.Hex("#ffffff"))
		ui.Row(c).Padding(28, 30, 10, 30).Gap(20).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).Gap(8).Children(func() {
				ui.Text(c, "关于项目记忆").FontSize(11).TextColor(design.Muted)
				ui.Text(c, "留下过程，积累知识。").FontSize(25).FontWeight(600)
				ui.Text(c, "原始记录与知识，共同组成这个项目的记忆。").FontSize(12).TextColor(design.Muted)
			})
			quietButton(c, "×").Label("关闭记忆说明").Size(30, 30).Radius(15).OnClick(a.closeContentExplanation)
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, 18, 18, 18).Gap(14).Children(func() {
			ui.Image(c, contentDiagram(s.Example)).Label("原始记录可整理为知识，知识保留出处，也可以直接保存知识").FillWidth().Height((width - 36) * 480 / 900).Fit(ui.Contain)
			ui.Row(c).Wrap().Gap(12).Children(func() {
				quietButton(c, "关系示意").OnClick(func() { s.Example = false })
				quietButton(c, "举个例子").OnClick(func() { s.Example = true })
				ui.Spacer(c)
				quietButton(c, "与 Cognee 的关系").OnClick(func() { s.CogneeOpen = !s.CogneeOpen })
			})
			if s.CogneeOpen {
				ui.Text(c, "Cognee 帮助组织材料、提炼和检索。这里的知识是保存下来的可复用结论；图谱中的实体与关系帮助关联内容。原始记录经过索引后也能被检索，仍然保留为原始记录。").FontSize(12).LineHeight(1.8).TextColor(design.Muted)
			}
		})
		ui.Row(c).Shrink(0).Padding(16, 30).Gap(15).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, "整理会新增知识，原始记录仍然保留。").FontSize(11).TextColor(design.Muted).Grow(1).MinWidth(0)
			actionButton(c, "知道了", nil).Background(ui.Hex("#3b526b")).TextColor(ui.Hex("#ffffff")).OnClick(a.closeContentExplanation)
		})
	})
	if dialog.Dismissed() {
		a.closeContentExplanation()
	}
}
