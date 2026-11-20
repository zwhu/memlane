"""Structured CLI failure types, classification, and output redaction."""
import re

from project_policy import PolicyError


class ProfileAgentFailure(PolicyError):
    """Structured failure raised when a CLI agent fails during profile export."""

    def __init__(self, returncode, diagnostic, detail=None):
        super().__init__('profile_agent_failed', 503)
        self.returncode, self.diagnostic = returncode, diagnostic
        self.detail = detail


def failure_detail(stderr: bytes) -> str | None:
    """Expose one actual CLI error line, excluding auth, paths and personal content."""
    text = re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', stderr.decode('utf-8', errors='replace'))
    line = next(
        (
            line.strip()
            for line in reversed(text.splitlines())
            if re.search(
                r'(?i)\b(?:error|fatal|failed|unavailable|invalid|cannot|unsupported|could not|not supported|no complete|unexpected argument)\b',
                line,
            )
        ),
        '',
    )
    if not line:
        return None
    line = re.sub(r'https?://[^\s<>]+', '<url>', line)
    line = re.sub(r'(?i)bearer\s+[^\s,;]+', 'Bearer <redacted>', line)
    line = re.sub(r'(?i)((?:api[_-]?key|token|secret|password|authorization)\s*[=:]\s*)[^\s,;]+', r'\1<redacted>', line)
    line = re.sub(r'\bsk-[A-Za-z0-9_-]+', '<redacted>', line)
    line = re.sub(r'(?i)\b(?:SECRET|PRIVATE|API_KEY|TOKEN)[A-Za-z0-9_-]*\b', '<redacted>', line)
    line = re.sub(r'[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}', '<email>', line)
    line = re.sub(r'(?:/Users/|/private/|/var/|/tmp/)[^\s\"\'<>]+', '<path>', line)
    return line[:400]


def failure_reason(stderr: bytes) -> str:
    """Classify CLI stderr/error output into a bounded failure reason code."""
    # Never publish raw CLI output: it can include tokens or personal memory.
    text = stderr.decode('utf-8', errors='replace').lower()
    if any(value in text for value in ('unexpected argument', 'unrecognized argument', 'unknown option')):
        return 'arguments_rejected'
    if any(value in text for value in ('unauthorized', 'authentication failed', 'authentication required', 'not logged in', 'please log in')):
        return 'authentication_required'
    if any(value in text for value in ('rate limit', 'quota exceeded', 'usage limit')):
        return 'quota_unavailable'
    if any(value in text for value in ('connection refused', 'failed to connect', 'network is unreachable')):
        return 'connection_failed'
    return 'agent_exited'
