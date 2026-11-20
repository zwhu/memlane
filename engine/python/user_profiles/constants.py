"""Shared constants and document validation for user profile storage."""
from pathlib import Path

from project_policy import PolicyError

PROMPT_PATH = Path(__file__).resolve().parents[2] / 'prompts/user-profile-export.md'


def profile_prompt() -> str:
    """Return the fixed prompt used for both automatic and manual profile exports."""
    return PROMPT_PATH.read_text(encoding='utf-8').strip()


PROMPT = profile_prompt()

MANUAL_SOURCES = {
    'manual:chatgpt': 'ChatGPT',
    'manual:claude': 'Claude',
    'manual:gemini': 'Gemini',
    'manual:grok': 'Grok',
    'manual:deepseek': 'DeepSeek',
}
LIMIT = 256 * 1024


def valid_body(body) -> str:
    """Validate and normalize a non-empty UTF-8 profile document within size limits."""
    if not isinstance(body, str) or not body.strip() or '\x00' in body or len(body.encode()) > LIMIT:
        raise PolicyError('invalid_profile_document')
    return body.strip()
