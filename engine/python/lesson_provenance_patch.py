"""Versioned provenance and default-language extension of pinned upstream files.

The upstream cache stays immutable. Language instructions are appended to the
official prompts; schemas, extraction, ingestion and replacement rules stay native.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import tempfile

from language_settings import language_settings, knowledge_language_instructions

MODULE = 'cognee/modules/session_distillation/distill.py'
VERSION = 2
PROMPT_ROOT = 'cognee/infrastructure/llm/prompts/'
LANGUAGE_PROMPTS = tuple(PROMPT_ROOT + name for name in (
    'coding/agent_context_extraction_system.txt',
    'coding/session_distillation_curator_system.txt',
    'coding/session_distillation_writer_system.txt',
    'coding/session_distillation_merge_system.txt',
    'session_distillation_curator_system.txt',
    'session_distillation_writer_system.txt',
))


def apply(text):
    old = '''    newest = newest_qa_row(rows) if rows else newest_qa_row(qa_rows)
    return parse_qa_context(newest.get("context") if newest else "")'''
    new = '''    newest = newest_qa_row(rows) if rows else newest_qa_row(qa_rows)
    context = dict(parse_qa_context(newest.get("context") if newest else ""))
    # Preserve latest context for state replacement; provenance uses ONLY
    # explicitly linked QAs, never the session start or fallback newest turn.
    linked_ids = {str(q) for member in members for q in member.source_qa_ids}
    linked = [row for row in qa_rows if str(row.get("qa_id") or "") in linked_ids]
    from datetime import date as _source_date
    dates = []
    for row in linked:
        day = str(parse_qa_context(row.get("context")).get("date") or "")
        try:
            if len(day) == 10 and _source_date.fromisoformat(day).isoformat() == day:
                dates.append(day)
        except ValueError:
            pass
    context["first_source_date"] = min(dates) if dates else ""
    context["source_qa_ids"] = sorted({str(row["qa_id"]) for row in linked})
    context["source_date_status"] = "linked_qa" if dates else "unconfirmed"
    return context'''
    edits = [(old, new),
        ('        "session_date": str(qa_context.get("date") or ""),',
         '''        "session_date": str(qa_context.get("date") or ""),
        "first_source_date": str(qa_context.get("first_source_date") or ""),
        "source_qa_ids": list(qa_context.get("source_qa_ids") or []),
        "source_date_status": str(qa_context.get("source_date_status") or "unconfirmed"),'''),
        ('    return documents\n\n\nasync def _lesson_node_set',
         '''    # Preserve evidenced first dates before the native state-key replacement
    # deletes superseded rows. Read trusted system metadata in the native scope.
    for document in documents:
        if not document.metadata.get("state_key"):
            continue
        rows = await find_lessons_by_state_key(scope.dataset.id, document.metadata["state_key"], node_set)
        candidates = [document.metadata.get("first_source_date")]
        for row in rows:
            facts = getattr(row, "system_metadata", None) or {}
            if facts.get("lesson_type") == document.metadata.get("lesson_type"):
                candidates.append(facts.get("first_source_date"))
        from datetime import date as _first_date
        valid = []
        for day in candidates:
            try:
                if isinstance(day, str) and len(day) == 10 and _first_date.fromisoformat(day).isoformat() == day:
                    valid.append(day)
            except ValueError:
                pass
        if valid:
            document.metadata["first_source_date"] = min(valid)
            document.metadata["source_date_status"] = "linked_qa"
    return documents


async def _lesson_node_set''')]
    for before, after in edits:
        if text.count(before) != 1:
            raise ValueError('pinned_lesson_provenance_anchor_mismatch')
        text = text.replace(before, after, 1)
    return text


def prepare(source, manifest, root):
    """Copy verified packages to a content-addressed local runtime component."""
    source, root = Path(source), Path(root)
    patched = apply((source / MODULE).read_text()).encode()
    # Upstream resolves its local version from this file. Preserve it so a
    # source-location change cannot change migration/version detection.
    metadata = (source / 'pyproject.toml').read_bytes() if (source / 'pyproject.toml').exists() else b''
    prompts = {name: ((source / name).read_text() + knowledge_language_instructions()).encode()
               for name in LANGUAGE_PROMPTS}
    generation = hashlib.sha256(Path(__file__).read_bytes() + patched + metadata
        + json.dumps(language_settings(), sort_keys=True).encode()
        + b''.join(prompts.values()) + manifest['commit'].encode()).hexdigest()
    target = root / ('lesson-provenance-' + generation[:20])
    expected = {}
    if metadata:
        expected['pyproject.toml'] = metadata
    for package in manifest['packages']:
        for path in (source / package).rglob('*'):
            if path.is_file() and '__pycache__' not in path.parts and not any(p.startswith('.') for p in path.relative_to(source).parts):
                relative = str(path.relative_to(source))
                expected[relative] = patched if relative == MODULE else prompts.get(relative, path.read_bytes())
    root.mkdir(parents=True, exist_ok=True)
    if not target.exists():
        temporary = Path(tempfile.mkdtemp(prefix='.provenance-', dir=root))
        try:
            for name, data in expected.items():
                path = temporary / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
            os.rename(temporary, target)
        finally:
            if temporary.exists():
                shutil.rmtree(temporary)
    if target.is_symlink():
        raise ValueError('provenance_component_changed')
    for name, data in expected.items():
        path = target / name
        if path.is_symlink() or not path.is_file() or path.read_bytes() != data:
            raise ValueError('provenance_component_changed')
    extra = {str(p.relative_to(target)) for p in target.rglob('*.py') if '__pycache__' not in p.parts} - expected.keys()
    if extra:
        raise ValueError('unexpected_provenance_module')
    receipt = {'schema_version': VERSION, 'upstream_commit': manifest['commit'],
        'generation': generation, 'module': MODULE, 'module_sha256': hashlib.sha256(patched).hexdigest(),
        'language': language_settings(),
        'prompt_sha256': {name: hashlib.sha256(data).hexdigest() for name, data in prompts.items()}}
    (root / 'lesson-provenance-applied.json').write_text(json.dumps(receipt, sort_keys=True))
    return target
