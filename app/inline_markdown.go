package app

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func previewMarkdownSource(s string) string {
	return kitui.PreviewMarkdownSource(s)
}

func parseInlineMarkdown(src string, baseSize float32, textColor ui.Color) []ui.Span {
	return kitui.ParseInlineMarkdown(src, baseSize, textColor, tok.Fill2, tok.Accent)
}

func v4InlineMarkdown(c *ui.Context, raw string, size float32, weight int, lineHeight float32, color ui.Color) ui.Element {
	return kitui.InlineMarkdown(c, &tok.Tokens, raw, size, weight, lineHeight, color)
}
