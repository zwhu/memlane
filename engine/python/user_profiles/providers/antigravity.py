"""Non-interactive CLI export adapter for Gemini App (Antigravity agy)."""
import json
from pathlib import Path
import shlex
import sys

from project_policy import PolicyError

from ..constants import valid_body
from ..errors import ProfileAgentFailure, failure_detail, failure_reason
from . import CommandSpec, ProfileProvider

_ISOLATION_INSTRUCTIONS = '''# Memlane User Profile Export (Isolated Read-Only Session)

This is a standalone, isolated user-profile export session run by Memlane.
- Do NOT read, inspect, or use memlane-cognee, Memlane, or Cognee plugin skills, scripts, hooks, MCP tools, or files outside this workspace.
- Do NOT execute shell commands, modify/create files, browse the web, or invoke external tools.
- Answer the prompt directly in your final response following the exact requested format, based strictly on your own built-in memories, instructions, and context. If none are stored, explicitly state so in the requested format.
'''

_DENY_TOOLS_SCRIPT = '''#!/usr/bin/env python3
import json
import sys

sys.stdin.read()
print(json.dumps({
    "decision": "deny",
    "reason": "Tools are disabled during Memlane user profile export. Do not read plugin skills or external files; answer directly from your own built-in memories, rules, and context.",
}))
'''


def _write_isolated_workspace(work: Path) -> None:
    """Populate a minimal git root with read-only rules and PreToolUse tool denial."""
    (work / '.git').mkdir(mode=0o700, exist_ok=True)
    instructions = work / 'AGENTS.md'
    instructions.write_text(_ISOLATION_INSTRUCTIONS, encoding='utf-8')
    instructions.chmod(0o600)
    agents_dir = work / '.agents'
    agents_dir.mkdir(mode=0o700, exist_ok=True)
    deny_script = agents_dir / 'deny-tools.py'
    deny_script.write_text(_DENY_TOOLS_SCRIPT, encoding='utf-8')
    deny_script.chmod(0o700)
    hooks = {
        'memlane-profile-export': {
            'PreToolUse': [
                {
                    'matcher': '*',
                    'hooks': [
                        {
                            'type': 'command',
                            'command': shlex.quote(sys.executable) + ' ./deny-tools.py',
                            'timeout': 5,
                        }
                    ],
                }
            ]
        }
    }
    hooks_file = agents_dir / 'hooks.json'
    hooks_file.write_text(json.dumps(hooks, indent=2), encoding='utf-8')
    hooks_file.chmod(0o600)


class AntigravityProvider(ProfileProvider):
    """Profile exporter for Gemini App (`agy`) CLI."""

    kind = 'antigravity'
    display_name = 'Gemini App'

    def build_command(self, executable, row, work, prompt, env):
        from agent_bridge_runtime import read_agent_defaults

        profile_path = Path(row['profile']).expanduser()
        if profile_path.name != 'config' or profile_path.parent.name != '.gemini':
            raise PolicyError('profile_automatic_unavailable', 409)
        defaults = read_agent_defaults(self.kind, profile_path)
        # Keep the native HOME so agy finds Keychain login and default CLI model.
        env['HOME'] = str(profile_path.parent.parent)
        _write_isolated_workspace(work)
        args = [
            executable,
            '--output-format',
            'json',
            '--mode',
            'plan',
            '--model',
            defaults['model'],
            '--disable-slash-commands',
            '--sandbox',
            '--print=' + prompt,
        ]
        return CommandSpec(args=args, env=env)

    def parse_failure(self, returncode, stdout, stderr):
        reason = failure_reason(stderr)
        detail = failure_detail(stderr)
        if reason == 'agent_exited' and detail is None:
            try:
                parsed = json.loads(stdout.decode('utf-8', errors='replace'))
            except ValueError:
                parsed = None
            if isinstance(parsed, dict) and isinstance(parsed.get('error'), str) and parsed['error']:
                err_bytes = parsed['error'].encode('utf-8', errors='replace')
                reason = failure_reason(err_bytes)
                detail = failure_detail(err_bytes)
        return ProfileAgentFailure(returncode, reason, detail)

    def parse_output(self, stdout, output_file=None):
        try:
            result = json.loads(stdout.decode('utf-8'))
        except ValueError:
            raise PolicyError('profile_agent_failed', 503) from None
        if not isinstance(result, dict):
            raise PolicyError('profile_agent_failed', 503)
        if str(result.get('status') or '').upper() != 'SUCCESS' or result.get('error'):
            err_bytes = str(result.get('error') or '').encode('utf-8', errors='replace')
            raise ProfileAgentFailure(0, failure_reason(err_bytes), failure_detail(err_bytes))
        body = result.get('response')
        if not isinstance(body, str) or not body.strip():
            if result.get('denied_actions'):
                raise ProfileAgentFailure(0, 'permission_denied', 'Agent 尝试执行受限操作，未返回画像正文')
            raise ProfileAgentFailure(0, 'empty_response', 'Agent 未返回画像正文')
        return valid_body(body)
