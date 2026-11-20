"""Non-interactive CLI export adapter for pi."""
from . import CommandSpec, ProfileProvider


class PiProvider(ProfileProvider):
    """Profile exporter for pi coding agent CLI."""

    kind = 'pi'
    display_name = 'pi'

    def build_command(self, executable, row, work, prompt, env):
        env['PI_CODING_AGENT_DIR'] = row['profile']
        args = [
            executable,
            '--print',
            '--no-session',
            '--no-extensions',
            '--no-skills',
            '--tools',
            'read,grep,find,ls',
            prompt,
        ]
        return CommandSpec(args=args, env=env)
