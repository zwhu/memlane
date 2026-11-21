"""Non-interactive CLI export adapters for Codex and TraeX."""
import json
from pathlib import Path

from . import CommandSpec, ProfileProvider


class _CodexFamilyProvider(ProfileProvider):
    """Shared read-only exporter for Codex-compatible CLIs."""

    home_env_var: str = ''
    config_filename: str = ''

    def build_command(self, executable, row, work, prompt, env):
        from agent_bridge_runtime import read_agent_defaults
        import tomlkit

        profile = row['profile']
        defaults = read_agent_defaults(self.kind, profile)
        env[self.home_env_var] = profile
        if self.kind == 'traex':
            env.pop('CODEX_HOME', None)
            env['TRAECLI_HOME'] = str(Path(profile) / 'cli')
        output = work / 'answer.md'
        args = [
            executable,
            'exec',
            '--ephemeral',
            '--ignore-user-config',
            '--skip-git-repo-check',
            '-s',
            'read-only',
            '-c',
            'features.hooks=false',
            '-c',
            'features.plugins=false',
            '-c',
            'model=' + json.dumps(defaults['model']),
            '-c',
            'model_provider=' + json.dumps(defaults['provider']),
            '-c',
            'memories.extract_model=' + json.dumps(defaults['model']),
            '-c',
            'memories.consolidation_model=' + json.dumps(defaults['model']),
            '-c',
            'memories.generate_memories=false',
            '-o',
            str(output),
            prompt,
        ]
        # Auth and stored native memories stay in the original Agent home.
        # Ignore tool configuration at the official CLI boundary: disabling
        # individual MCP entries with quoted dotted keys can create phantom
        # servers that fail native transport deserialization before generation.
        config = Path(profile) / self.config_filename
        parsed = dict(tomlkit.parse(config.read_text())) if config.is_file() else {}
        selected_profile = None
        if parsed.get('profile') and isinstance(parsed.get('profiles'), dict):
            candidate = parsed['profiles'].get(parsed['profile'])
            if isinstance(candidate, dict):
                selected_profile = candidate
        if 'reasoning_effort' in defaults:
            args[-1:-1] = ['-c', 'model_reasoning_effort=' + json.dumps(defaults['reasoning_effort'])]
        if 'service_tier' in defaults:
            args[-1:-1] = ['-c', 'service_tier=' + json.dumps(defaults['service_tier'])]
        features = dict(parsed.get('features', {})) if isinstance(parsed.get('features'), dict) else {}
        memories = dict(parsed.get('memories', {})) if isinstance(parsed.get('memories'), dict) else {}
        if selected_profile is not None:
            if isinstance(selected_profile.get('features'), dict):
                features.update(selected_profile['features'])
            if isinstance(selected_profile.get('memories'), dict):
                memories.update(selected_profile['memories'])
        if isinstance(features.get('memories'), bool):
            args[-1:-1] = ['-c', 'features.memories=' + str(features['memories']).lower()]
        for name, value in memories.items():
            if name == 'use_memories' and isinstance(value, bool):
                args[-1:-1] = ['-c', 'memories.' + name + '=' + str(value).lower()]
        return CommandSpec(args=args, env=env, output_file=output)


class CodexProvider(_CodexFamilyProvider):
    """Profile exporter for OpenAI Codex CLI."""

    kind = 'codex'
    display_name = 'Codex'
    home_env_var = 'CODEX_HOME'
    config_filename = 'config.toml'


class TraeXProvider(_CodexFamilyProvider):
    """Profile exporter for TraeX CLI."""

    kind = 'traex'
    display_name = 'TraeX'
    home_env_var = 'TRAE_HOME'
    config_filename = 'traecli.toml'
