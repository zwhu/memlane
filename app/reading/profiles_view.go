package reading

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	kitui "github.com/zwhu/memlane/kit/ui"
)

const (
	profileMarkdownVirtualizeThreshold = 4096
	profileMarkdownChunkTargetBytes    = 1400
	profileMarkdownChunkHardLimitBytes = 3600
)

// SplitProfileMarkdownChunks splits large Markdown profiles into fence-safe
// chunks for virtualized ui.List rendering, never breaking fenced code blocks.
func SplitProfileMarkdownChunks(md string) []string {
	if md == "" {
		return nil
	}
	lines := strings.Split(md, "\n")
	var chunks []string
	var buf strings.Builder
	fenceChar := byte(0)
	fenceLen := 0

	flush := func() {
		chunk := strings.TrimRight(buf.String(), "\n")
		if strings.TrimSpace(chunk) != "" {
			chunks = append(chunks, chunk)
		}
		buf.Reset()
	}

	for idx, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if fenceChar == 0 {
			if len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
				ch := trimmed[0]
				n := 0
				for n < len(trimmed) && trimmed[n] == ch {
					n++
				}
				if n >= 3 {
					fenceChar = ch
					fenceLen = n
				}
			}
		} else if len(trimmed) >= fenceLen && trimmed[0] == fenceChar {
			n := 0
			for n < len(trimmed) && trimmed[n] == fenceChar {
				n++
			}
			if n >= fenceLen && strings.TrimSpace(trimmed[n:]) == "" {
				fenceChar = 0
				fenceLen = 0
			}
		}

		isHeading := fenceChar == 0 && (strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### "))
		if isHeading && buf.Len() >= profileMarkdownChunkTargetBytes/2 {
			flush()
		}

		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)

		if fenceChar == 0 {
			isBlank := strings.TrimSpace(line) == ""
			if (isBlank && buf.Len() >= profileMarkdownChunkTargetBytes) || buf.Len() >= profileMarkdownChunkHardLimitBytes {
				if idx < len(lines)-1 {
					flush()
				}
			}
		}
	}
	flush()
	if len(chunks) == 0 {
		return []string{md}
	}
	return chunks
}

type ProfileMarkdown struct {
	original, normalized string
	key                  string
	chunks               []string
	cache                kitui.ChunkCache
}

func (m *ProfileMarkdown) Clear() {
	m.original, m.normalized, m.key, m.chunks = "", "", "", nil
	m.cache.Invalidate("")
}

func (m *ProfileMarkdown) Resolve(key, body string) (string, []string) {
	if key == m.key && body == m.original {
		return m.normalized, m.chunks
	}
	m.Clear()
	m.key, m.original, m.normalized = key, body, UnwrapExportEnvelope(body)
	if len(m.normalized) > profileMarkdownVirtualizeThreshold {
		m.chunks = m.cache.GetOrSplit("profiles", key, "body", kitui.MarkdownContentKey(m.normalized), func() []string {
			return SplitProfileMarkdownChunks(m.normalized)
		})
	}
	return m.normalized, m.chunks
}

func (m *ProfileMarkdown) Render(c *ui.Context, key, body string, list *ui.ListState, scroll *ui.ScrollState, opts kitui.VirtualizedMarkdownOptions) ui.Element {
	opts.Cache = &m.cache
	return kitui.RenderVirtualizedMarkdownList(c, key, list, scroll, UnwrapExportEnvelope(body), opts)
}

func RenderProfileWithHeader(c *ui.Context, id, key string, chunks []string, list *ui.ListState, scroll *ui.ScrollState, width float32, md *markdown.Options, header func()) ui.Element {
	list.KeepOffset = true
	list.Selected = nil
	list.Key = func(row int) any {
		if row == 0 {
			return "header/" + id + "/" + key
		}
		return fmt.Sprintf("chunk/%s/%s/%d", id, key, row-1)
	}
	return ui.List(c.Key("user-profile-list-"+id+"-"+key), list, 1+len(chunks), func(row int) {
		ui.Column(c.Key(fmt.Sprintf("profile-vrow-%s-%d", key, row))).FillWidth().MaxWidth(width).Padding(0, kitui.Sp10).Gap(kitui.Sp4).Children(func() {
			if row == 0 {
				ui.Column(c).FillWidth().Padding(kitui.Sp6, 0, kitui.Sp2, 0).Gap(kitui.Sp4).Children(header)
				return
			}
			idx := row - 1
			padBottom := float32(kitui.Sp2)
			if idx == len(chunks)-1 {
				padBottom = kitui.Sp8
			}
			kitui.MarkdownChunkRow(c, fmt.Sprintf("user-profile-markdown-%s-%d", key, idx), chunks[idx], md, width, padBottom)
		})
	}).Label("用户画像内容").Grow(1).MinHeight(0).TrackScroll(scroll)
}
