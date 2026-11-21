"""Non-interactive CLI export adapter for pi."""
from . import CommandSpec, ProfileProvider


class PiProvider(ProfileProvider):
    """Profile exporter for pi coding agent CLI."""

    kind = 'pi'
    display_name = 'pi'

    def build_command(self, executable, row, work, prompt, env):
        from agent_bridge_runtime import read_agent_defaults

        profile = row['profile']
        defaults = read_agent_defaults(self.kind, profile)
        env['PI_CODING_AGENT_DIR'] = profile
        args = [
            executable,
            '--print',
            '--no-session',
            '--no-extensions',
            '--no-skills',
            '--provider',
            defaults['provider'],
            '--model',
            defaults['model'],
            '--tools',
            'read,grep,find,ls',
            prompt,
        ]
        return CommandSpec(args=args, env=env)
