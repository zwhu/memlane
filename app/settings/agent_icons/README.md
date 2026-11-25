Agent marks from Lobe Icons, @lobehub/icons-static-svg 1.102.0.
Source: https://github.com/lobehub/lobe-icons
SVG source: https://unpkg.com/@lobehub/icons-static-svg@1.102.0/icons/
MIT license in LICENSE; brand marks belong to their respective owners.
Vendored locally; no runtime network requests.

2026-11-18: added openai.svg (ChatGPT), gemini.svg, grok.svg and deepseek.svg
from the same pinned package. Manual profile names and registered Agent kinds
share the resolver; Claude and Claude Code reuse claude.svg.

2026-11-18: connection rows use original-color codex-color.svg, trae-color.svg
and claudecode-color.svg from the same pinned 1.102.0 package. Render with
ui.Image to preserve SVG fills; pi/opencode retain original black marks.

2026-11-21: switched traex back to monochrome trae.svg (currentColor, rendered
with tok.Text1) from the same pinned 1.102.0 package because trae-color.svg's
neon green #32F08C had harsh contrast on light surfaces.
