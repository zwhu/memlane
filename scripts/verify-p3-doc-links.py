#!/usr/bin/env python3
"""检查本轮新增/已有 P3 文档和实施清单中的本地链接与章节锚点。"""
from pathlib import Path
import re
import unicodedata
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parents[1]
files = [ROOT / 'docs' / name for name in (
    'p3-validation.md', 'implementation-plan.md', 'p3-product-design.md',
    'p3-interaction-design.md', 'p3-technical-analysis.md', 'p3-architecture.md', 'research-p3-memory.md','p3-goal-prompt.md','p3-experiment-goal-prompt.md')]

def headings(path):
    seen = {}
    result = set()
    for title in re.findall(r'^#{1,6}\s+(.+)$', path.read_text(), re.M):
        slug = ''.join(c for c in title.lower() if c in '-_ ' or unicodedata.category(c)[0] in 'LN').replace(' ', '-')
        number = seen.get(slug, 0)
        seen[slug] = number + 1
        result.add(slug + ('-' + str(number) if number else ''))
    return result

errors = []
checked = 0
for file in files:
    for target in re.findall(r'!?\[[^\]]*\]\(([^\s)]+)(?:\s+"[^"]*")?\)', file.read_text()):
        if re.match(r'^[a-zA-Z][a-zA-Z0-9+.-]*:', target):
            continue
        target = unquote(target.strip('<>'))
        path, _, anchor = target.partition('#')
        dest = (file.parent / path).resolve() if path else file
        checked += 1
        if not dest.exists():
            errors.append(f'{file.relative_to(ROOT)}: missing {target}')
        elif anchor and dest.suffix == '.md' and anchor not in headings(dest):
            errors.append(f'{file.relative_to(ROOT)}: missing anchor {target}')
if errors:
    raise SystemExit('\n'.join(errors))
print(f'PASS: {len(files)} documents, {checked} local links and anchors')
