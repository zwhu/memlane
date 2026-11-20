"""Non-interactive CLI export adapter for Claude Code."""
import json

from project_policy import PolicyError

from ..constants import valid_body
from . import CommandSpec, ProfileProvider


class ClaudeCodeProvider(ProfileProvider):
    """Profile exporter for Claude Code CLI."""

    kind = 'claude_code'
    display_name = 'Claude Code'

    def build_command(self, executable, row, work, prompt, env):
        env['CLAUDE_CONFIG_DIR'] = row['profile']
        env.pop('CLAUDECODE', None)
        args = [
            executable,
            '--print',
            '--no-session-persistence',
            '--output-format',
            'json',
            '--permission-mode',
            'dontAsk',
            '--tools',
            'Read,Glob,Grep',
            '--strict-mcp-config',
            '--mcp-config',
            '{"mcpServers":{}}',
            '--settings',
            '{"disableAllHooks":true}',
            prompt,
        ]
        return CommandSpec(args=args, env=env)

    def parse_output(self, stdout, output_file=None):
        result = json.loads(stdout.decode('utf-8'))
        if result.get('is_error') or result.get('subtype') != 'success':
            raise PolicyError('profile_agent_failed', 503)
        return valid_body(result.get('result'))
