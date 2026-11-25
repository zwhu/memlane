"""LLM HTTP egress language policy for Cognee distillation requests.

Replaces source edits on upstream prompt ``.txt`` files by injecting
``knowledge_language_instructions()`` into matching distillation/extraction
system prompts on the outbound HTTP transport boundary.
"""
import copy
import json

import httpx

from contextvars import ContextVar
from contextlib import contextmanager

LANGUAGE_MARKER = 'MEMLANE DEFAULT LANGUAGE ('
DISTILLATION_PROMPT_SIGNATURES = (
    "You read a batch of a coding agent's tool traces from one session in a software repository",
    "You are reading one slice of a finished coding session in a software repository",
    "You are deciding whether one proposed lesson from a coding session should be persisted",
    "You receive a numbered list of lessons proposed from one coding session.",
    "You are reading one slice of a finished working session and proposing durable lessons",
    "You are deciding whether one proposed session lesson should be persisted",
)


DEFAULT_LANGUAGE = 'zh-CN'
_default_language = DEFAULT_LANGUAGE
_language = ContextVar('memlane_output_language', default=None)


def set_default_language(lang):
    global _default_language
    _default_language = lang if lang else DEFAULT_LANGUAGE


def current_output_language():
    selected = _language.get()
    return _default_language if selected is None else selected


@contextmanager
def scoped_output_language(lang):
    token = _language.set(current_output_language() if lang is None else lang)
    try:
        yield
    finally:
        _language.reset(token)


def knowledge_language_instructions(language=None):
    language = current_output_language() if language is None else language
    if not language or language in ('passthrough', 'none'):
        return ''
    instructions = '''

MEMLANE DEFAULT LANGUAGE (zh-CN):
Write all natural-language candidate lessons, working_statement, statement,
why_learned and explanatory summaries in Simplified Chinese (简体中文), even when
the conversation, tool output, existing lessons or instructions above are English.
Keep JSON field names, enum values, state keys, entity identifiers, file paths,
commands, function names and code exactly as required by the original schema.
Express the explanation in concise Chinese; do not translate technical identifiers.
An existing English lesson stating the same fact is still already known: a language
change alone is not new knowledge and must not bypass the original durability,
evidence, deduplication or acceptance rules. Do not add a translation-only lesson.
'''

    if language == DEFAULT_LANGUAGE:
        return instructions
    return (instructions.replace('(zh-CN)', f'({language})')
            .replace('Simplified Chinese (简体中文)', language)
            .replace('concise Chinese', f'concise {language}'))


def is_distillation_prompt(text):
    """Return True when *text* contains an official distillation/extraction prompt."""
    return isinstance(text, str) and any(sig in text for sig in DISTILLATION_PROMPT_SIGNATURES)


def _append_instruction(text):
    if not is_distillation_prompt(text) or LANGUAGE_MARKER in text:
        return text, False
    instruction = knowledge_language_instructions()
    return text + instruction, bool(instruction)


def _rewrite_blocks(value, *, text_key='text'):
    if isinstance(value, str):
        return _append_instruction(value)
    if isinstance(value, list):
        changed = False
        out = []
        for item in value:
            if isinstance(item, dict) and isinstance(item.get(text_key), str):
                updated, hit = _append_instruction(item[text_key])
                if hit:
                    changed = True
                    out.append({**item, text_key: updated})
                    continue
            out.append(item)
        return (out if changed else value), changed
    return value, False


def inject_language_in_payload(payload):
    """Return ``(payload_or_copy, changed)`` after idempotent language injection."""
    if not isinstance(payload, dict):
        return payload, False
    changed = False
    updated = None

    def ensure_copy():
        nonlocal updated
        if updated is None:
            updated = copy.copy(payload)
        return updated

    for key in ('messages', 'input'):
        msgs = payload.get(key)
        if isinstance(msgs, list):
            new_msgs = []
            list_changed = False
            for msg in msgs:
                if isinstance(msg, dict) and msg.get('role') in ('system', 'developer') and 'content' in msg:
                    new_content, hit = _rewrite_blocks(msg['content'])
                    if hit:
                        list_changed = True
                        new_msgs.append({**msg, 'content': new_content})
                        continue
                new_msgs.append(msg)
            if list_changed:
                changed = True
                ensure_copy()[key] = new_msgs

    if isinstance(payload.get('instructions'), str):
        new_inst, hit = _append_instruction(payload['instructions'])
        if hit:
            changed = True
            ensure_copy()['instructions'] = new_inst

    if 'system' in payload:
        new_sys, hit = _rewrite_blocks(payload['system'])
        if hit:
            changed = True
            ensure_copy()['system'] = new_sys

    for key in ('systemInstruction', 'system_instruction'):
        inst = payload.get(key)
        if isinstance(inst, dict) and isinstance(inst.get('parts'), list):
            new_parts, hit = _rewrite_blocks(inst['parts'], text_key='text')
            if hit:
                changed = True
                ensure_copy()[key] = {**inst, 'parts': new_parts}

    return (updated if changed else payload), changed


def rewrite_llm_request(request):
    """Rewrite an ``httpx.Request`` in-flight if it carries a matching distillation prompt."""
    if request.method.upper() != 'POST':
        return request
    ctype = (request.headers.get('content-type') or '').split(';', 1)[0].strip().lower()
    if ctype != 'application/json':
        return request
    try:
        raw = request.content
    except httpx.RequestNotRead:
        return request
    if not raw:
        return request
    try:
        payload = json.loads(raw)
    except ValueError:
        return request
    rewritten, changed = inject_language_in_payload(payload)
    if not changed:
        return request
    body = json.dumps(rewritten, ensure_ascii=False).encode('utf-8')
    headers = httpx.Headers(request.headers)
    if 'content-length' in headers:
        del headers['content-length']
    return httpx.Request(
        method=request.method,
        url=request.url,
        headers=headers,
        content=body,
        extensions=dict(request.extensions) if request.extensions else None,
    )
