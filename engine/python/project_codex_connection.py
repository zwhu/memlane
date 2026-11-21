"""Reviewed computer-level Codex connection, separate from plugin preparation.

The management service supplies fixed host paths. Requests cannot nominate an
arbitrary executable or user config. A preview never changes that config.
"""
import asyncio
import copy
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import time

import tomlkit

from project_control import private_write
from project_policy import PolicyError


def digest(data):
    return hashlib.sha256(data).hexdigest()


def host(config):
    # Connecting installs host configuration, it does not authenticate the
    # model bridge. A fresh Codex profile need not have been logged in yet.
    home = Path(config['auth_home']).expanduser().resolve()
    executable = Path(config['codex_executable'])
    if not executable.is_absolute() or not executable.is_file():
        raise PolicyError('codex_executable_unavailable', 503)
    return home, executable


def instance_id(config):
    home, _ = host(config)
    return 'codex-' + digest(str(home).encode())[:24]


def read_config(home):
    if home.exists() and not home.is_dir():
        raise PolicyError('codex_config_requires_review', 409)
    path = home / 'config.toml'
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise PolicyError('codex_config_requires_review', 409)
    data = path.read_bytes() if path.exists() else b''
    try:
        parsed = tomlkit.parse(data.decode())
    except (ValueError, UnicodeError):
        raise PolicyError('codex_config_requires_review', 409)
    return data, parsed


def enabled_cognee(parsed):
    return sorted(name for name, value in parsed.get('plugins', {}).items()
                  if name.split('@', 1)[0] == 'cognee' and value.get('enabled', False))


def project_conflicts(parsed, enabled_plugin, codex_home):
    """Trusted project config can override a user-level plugin disable."""
    conflicts = []
    for workspace, settings in parsed.get('projects', {}).items():
        if settings.get('trust_level') != 'trusted':
            continue
        directory = Path(workspace) / '.codex'
        if (directory / 'config.toml').resolve() == (codex_home / 'config.toml').resolve():
            # A trusted $HOME workspace points at the user config itself;
            # that is precisely the file this reviewed preview replaces.
            continue
        if not (directory / 'config.toml').exists():
            continue
        _, project = read_config(directory)
        if any(name != enabled_plugin for name in enabled_cognee(project)):
            conflicts.append(str(directory / 'config.toml'))
    return conflicts


def proposal(data, installation):
    """Edit only the reviewed Cognee entries and the required host hook flag."""
    parsed = tomlkit.parse(data.decode())
    old = enabled_cognee(parsed)
    name = installation['marketplace_name']
    plugin = 'cognee@' + name
    marketplaces = parsed.setdefault('marketplaces', tomlkit.table())
    entry = marketplaces.setdefault(name, tomlkit.table())
    entry['source_type'], entry['source'] = 'local', installation['marketplace_root']
    plugins = parsed.setdefault('plugins', tomlkit.table())
    for key in old:
        plugins[key]['enabled'] = False
    plugins.setdefault(plugin, tomlkit.table())['enabled'] = True
    parsed.setdefault('features', tomlkit.table())['hooks'] = True
    result = tomlkit.dumps(parsed).encode()
    # Parsing the complete result also guards unsupported/ambiguous TOML edits.
    check = tomlkit.parse(result.decode())
    if enabled_cognee(check) != [plugin]:
        raise PolicyError('codex_config_requires_review', 409)
    return result, [key for key in old if key != plugin]


def configured(parsed, installation):
    name = installation['marketplace_name']
    entry = parsed.get('marketplaces', {}).get(name, {})
    return (enabled_cognee(parsed) == ['cognee@' + name]
            and entry.get('source_type') == 'local'
            and entry.get('source') == installation['marketplace_root']
            and bool(parsed.get('features', {}).get('hooks')))


def verify_cache(cache, installation):
    raw = (Path(installation['plugin_dir']) / '.memlane-project-access.json').read_bytes()
    if digest(raw) != installation['manifest_sha256']:
        raise PolicyError('codex_plugin_source_changed', 409)
    manifest = json.loads(raw)
    for relative, expected in manifest['files'].items():
        path = cache / relative
        if path.is_symlink() or not path.is_file() or digest(path.read_bytes()) != expected:
            raise PolicyError('codex_installed_files_changed', 409)
    source_marker = (Path(installation['plugin_dir']) / '.memlane-installation.json').read_bytes()
    marker = cache / '.memlane-installation.json'
    if marker.is_symlink() or marker.read_bytes() != source_marker:
        raise PolicyError('codex_installation_binding_changed', 409)


def legacy_consumers(codex_home, plugin_ids=None):
    # Read process metadata only, never environment values or buffer bodies.
    rows = subprocess.check_output(['ps', '-axo', 'pid=,lstart=,command='], text=True)
    result = []
    for row in rows.splitlines():
        fields = row.strip().split(None, 6)
        if len(fields) < 7:
            continue
        command = fields[6]
        if str(codex_home / 'plugins/cache') + '/' not in command:
            continue
        if plugin_ids is not None:
            prefixes = [str(codex_home / 'plugins/cache' / name.split('@', 1)[1] / name.split('@', 1)[0]) + '/'
                        for name in plugin_ids if '@' in name]
            if not any(prefix in command for prefix in prefixes):
                continue
        for script in ('idle-watcher.py', 'exit-watcher.py', 'sync-session-to-graph.py', 'store-user-prompt.py'):
            if '/scripts/' + script in command and '/cognee/' in command:
                result.append({'pid': int(fields[0]), 'started': ' '.join(fields[1:6]), 'script': script})
                break
    return result


def stage_install(executable, installation, stage):
    """Use the real host installer in a private config, without copying OAuth."""
    stage.mkdir(parents=True, mode=0o700, exist_ok=True)
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(('COGNEE_', 'MEMLANE_'))}
    env.update(CODEX_HOME=str(stage), HOME=installation['home'])
    commands = [['plugin', 'marketplace', 'add', installation['marketplace_root']],
                ['plugin', 'add', 'cognee@' + installation['marketplace_name']]]
    for index, arguments in enumerate(commands):
        with (stage / ('install-' + str(index) + '.log')).open('wb') as log:
            os.chmod(log.name, 0o600)
            try:
                result = subprocess.run([str(executable), *arguments], env=env, stdin=subprocess.DEVNULL,
                                        stdout=log, stderr=subprocess.STDOUT, timeout=45)
            except subprocess.TimeoutExpired:
                raise PolicyError('codex_staging_result_unknown', 503)
        if result.returncode:
            raise PolicyError('codex_staging_failed', 503)
    version = json.loads((Path(installation['plugin_dir']) / '.codex-plugin/plugin.json').read_text())['version']
    cache = stage / 'plugins/cache' / installation['marketplace_name'] / 'cognee' / version
    verify_cache(cache, installation)
    return cache


class CodexConnection:
    def __init__(self, registry, config):
        self.registry, self.config = registry, config
        self.home, self.executable = host(config)
        self.id = instance_id(config)
        self.root = registry.path.parent / 'codex-connections' / self.id

    def view(self):
        state = self.registry.state
        _, parsed = read_config(self.home)
        agent = state['agents'].get(self.id)
        record = state.get('codex_connections', {}).get(self.id, {})
        preview = record.get('preview') or {}
        status = record.get('status', 'not_connected')
        if status == 'configured_pending_new_session' and not configured(parsed, agent['installation']):
            status = 'requires_review'
        if not agent and self.id in state.get('enrollments', {}):
            status = 'agent_creation_unconfirmed'
        return {'instance_id': self.id, 'codex_home': str(self.home), 'detected': True,
                'status': status,
                'agent_registered': bool(agent),
                'legacy_plugins': enabled_cognee(parsed),
                'legacy_consumers': legacy_consumers(self.home, preview.get('disabled_plugins', enabled_cognee(parsed))),
                'project_config_conflicts': project_conflicts(parsed, preview.get('enabled_plugin'), self.home),
                'preview': copy.deepcopy(record.get('preview')),
                'new_sessions': sum(json.loads(key)[0] == self.id and key not in record.get('sessions_before_activation', [])
                                    for key in state['sessions']) if status == 'configured_pending_new_session' else 0}

    async def prepare(self, expected):
        r = self.registry
        read_config(self.home)  # Reject a bad host target before creating an identity.
        # The same stable home always refers to the original creation attempt.
        async with r.mutex:
            r.check_revision(expected)
            old = r.state.get('codex_connections', {}).get(self.id, {})
            if old.get('status') in ('activation_unknown', 'staging_unknown'):
                raise PolicyError('codex_connection_result_requires_review', 409)
        if self.id not in r.state['agents']:
            if self.id in r.state.get('enrollments', {}):
                raise PolicyError('agent_creation_requires_review', 409)
            await r.provision_agent(expected, self.id, '本机 Codex')
        if self.id not in r.state['agents']:
            raise PolicyError('agent_creation_requires_review', 409)
        await r.prepare_installation(r.state['revision'], self.id,
                                    self.config['project_control']['plugin_dir'],
                                    self.config['project_control']['url'],
                                    self.config.get('agent_host_home', str(Path.home())))
        async with r.mutex:
            installation = r.state['agents'][self.id]['installation']
            old = r.state.setdefault('codex_connections', {}).get(self.id, {})
            if old.get('status') in ('activation_unknown', 'staging_unknown'):
                raise PolicyError('codex_connection_result_requires_review', 409)
            baseline, _ = read_config(self.home)
            planned, legacy = proposal(baseline, installation)
            self.root.mkdir(parents=True, mode=0o700, exist_ok=True)
            private_write(self.root / 'config-before.toml', baseline)
            private_write(self.root / 'config-proposed.toml', planned)
            preview = {'baseline_sha256': digest(baseline), 'proposed_sha256': digest(planned),
                       'disabled_plugins': legacy, 'enabled_plugin': 'cognee@' + installation['marketplace_name'],
                       'old_sessions_preserved': True, 'requires_new_session': True}
            record = {'status': 'staging_unknown', 'preview': preview, 'prepared_at': time.time()}
            r.state['codex_connections'][self.id] = record
            r.state['revision'] += 1
            r.save()
            try:
                cache = await asyncio.to_thread(stage_install, self.executable, installation, self.root / 'staged-home')
            except PolicyError as error:
                record['error_code'] = error.code
                r.save()
                raise
            record.update(status='prepared', staged_cache=str(cache))
            r.save()
            return self.view()

    async def activate(self, expected, reviewed_sha):
        r = self.registry
        async with r.mutex:
            r.check_revision(expected)
            record = r.state.get('codex_connections', {}).get(self.id)
            if not record or record['status'] != 'prepared':
                raise PolicyError('codex_activation_requires_prepared_result', 409)
            baseline, parsed = read_config(self.home)
            preview = record['preview']
            if reviewed_sha != preview['proposed_sha256'] or digest(baseline) != preview['baseline_sha256']:
                raise PolicyError('codex_configuration_changed', 409)
            if preview['disabled_plugins'] and await asyncio.to_thread(legacy_consumers, self.home, preview['disabled_plugins']):
                raise PolicyError('codex_legacy_consumers_still_active', 409)
            if project_conflicts(parsed, preview['enabled_plugin'], self.home):
                raise PolicyError('codex_project_config_requires_review', 409)
            planned = (self.root / 'config-proposed.toml').read_bytes()
            if digest(planned) != reviewed_sha:
                raise PolicyError('codex_preview_changed', 409)
            installation = r.state['agents'][self.id]['installation']
            source = Path(record['staged_cache'])
            verify_cache(source, installation)
            # Journal before any writes. Failure never causes an implicit second
            # install, restoration of owner credentials, or replay of old buffers.
            record['status'] = 'activation_unknown'
            record['sessions_before_activation'] = [key for key in r.state['sessions'] if json.loads(key)[0] == self.id]
            r.state['revision'] += 1
            r.save()
            target = self.home / 'plugins/cache' / installation['marketplace_name'] / 'cognee' / source.name
            if target.exists():
                verify_cache(target, installation)
            else:
                target.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
                shutil.copytree(source, target)
                verify_cache(target, installation)
            # Catch edits made while the cache was being copied too.
            current, _ = read_config(self.home)
            if digest(current) != preview['baseline_sha256']:
                raise PolicyError('codex_configuration_changed', 409)
            private_write(self.home / 'config.toml', planned)
            if read_config(self.home)[0] != planned:
                raise PolicyError('codex_activation_result_unknown', 503)
            record['status'] = 'configured_pending_new_session'
            record['activated_at'] = time.time()
            r.save()
            return self.view()

    async def review(self, expected):
        """Inspect the original preview/cache/config, without rerunning commands."""
        r = self.registry
        if self.id not in r.state['agents'] and self.id in r.state.get('enrollments', {}):
            await r.recover_agent(expected, self.id)
            return self.view()
        async with r.mutex:
            r.check_revision(expected)
            record = r.state.get('codex_connections', {}).get(self.id)
            if not record:
                if self.id in r.state.get('enrollments', {}) and self.id not in r.state['agents']:
                    # Recovery of an official creation needs its own original
                    # credential checks, outside this filesystem review.
                    raise PolicyError('agent_creation_requires_review', 409)
                return self.view()
            installation = r.state['agents'][self.id]['installation']
            current, parsed = read_config(self.home)
            preview = record['preview']
            version = json.loads((Path(installation['plugin_dir']) / '.codex-plugin/plugin.json').read_text())['version']
            staged = Path(record.get('staged_cache') or self.root / 'staged-home/plugins/cache' / installation['marketplace_name'] / 'cognee' / version)
            target = self.home / 'plugins/cache' / installation['marketplace_name'] / 'cognee' / version
            if configured(parsed, installation):
                verify_cache(target, installation)
                record['status'] = 'configured_pending_new_session'
            elif digest(current) == preview['baseline_sha256']:
                if staged.exists():
                    verify_cache(staged, installation)
                    record.update(status='prepared', staged_cache=str(staged))
                else:
                    record['status'] = 'staging_not_complete'
            else:
                raise PolicyError('codex_configuration_changed', 409)
            r.state['revision'] += 1
            r.save()
            return self.view()
