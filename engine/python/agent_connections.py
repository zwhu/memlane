"""Managed computer-level connections. Host paths come only from runtime config."""
import asyncio
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tomlkit

from project_control import private_write
from project_policy import PolicyError


def sha(data):
    return hashlib.sha256(data).hexdigest()


def safe_file(path):
    if any(parent.is_symlink() for parent in (path, *path.parents)):
        raise PolicyError('host_configuration_requires_review', 409)
    if path.exists() and not path.is_file():
        raise PolicyError('host_configuration_requires_review', 409)
    return path.read_bytes() if path.exists() else b''


class NativeConnection:
    def __init__(self, registry, config, kind, profile, executable):
        self.registry, self.config, self.kind = registry, config, kind
        self.profile = Path(profile).expanduser().absolute()
        self.executable = executable
        self.id = kind + '-' + sha(str(self.profile).encode())[:24]
        self.root = registry.path.parent / 'connections' / self.id
        self.target = self.profile / {'pi': 'extensions/memlane-cognee/index.ts', 'traex': 'traecli.toml',
                                     'claude_code': 'settings.json',
                                     'cursor': 'plugins/local/memlane-cognee/.memlane-installation.json'}[kind]

    def view(self):
        record = self.registry.state.get('connections', {}).get(self.id, {})
        result = {'instance_id': self.id, 'kind': self.kind, 'profile': str(self.profile),
                  'detected': bool(self.executable and Path(self.executable).is_file()),
                  'status': record.get('status', 'not_connected'),
                  'requires_reload': True, 'entrypoints': ['ide'] if self.kind == 'cursor' else ['cli'],
                  'capabilities': {key: {'available': True, 'verified': False}
                                   for key in ('explicit_memory', 'auto_capture', 'auto_recall', 'project_policy')},
                  'preview': copy.deepcopy(record.get('preview'))}
        if self.kind == 'cursor':
            result['capabilities']['auto_recall']['condition'] = 'session_start_or_first_non_read_tool; no_tool_turn_not_supported'
        if self.kind == 'claude_code':
            result['capabilities']['auto_capture']['condition'] = 'bound_main_session; sidechains_require_independent_binding'
        result['new_sessions'] = sum(json.loads(key)[0] == self.id and key not in record.get('sessions_before_activation', [])
                                     for key in self.registry.state['sessions']) if record.get('activated') else 0
        if result['status'] == 'configured_pending_new_session' and self.kind == 'pi':
            if sha(safe_file(self.target)) != record.get('proposed_sha256'):
                result['status'] = 'requires_review'
        if result['status'] == 'configured_pending_new_session' and self.kind == 'cursor':
            installation = self.registry.state['agents'][self.id]['installation']
            verify_artifact({**installation, 'plugin_dir': str(self.target.parent)})
        if result['status'] == 'configured_pending_new_session' and self.kind == 'traex':
            self.traex_installed(self.registry.state['agents'][self.id]['installation'])
        return result

    def traex_installed(self, installation):
        """Review the native direct-install receipt and cache without replay."""
        try:
            marketplace = installation['marketplace_name']
            state = json.loads(safe_file(self.profile / '.tmp/marketplaces' / marketplace / '.agents/plugins/trae-direct-install.json'))
            if (state['type'] != 'trae-direct-plugin-install' or state['marketplace'] != marketplace
                    or state['plugins']['memlane-cognee'] != {
                        'source_path': installation['plugin_dir'], 'source_kind': 'native'}):
                raise ValueError()
            settings = tomlkit.parse(safe_file(self.target).decode())
            enabled = 'memlane-cognee@' + marketplace
            plugins = settings.get('plugins', {})
            if plugins.get(enabled, {}).get('enabled') is not True:
                raise ValueError()
            if any(name != enabled and 'cognee' in name.lower() and value.get('enabled', False)
                   for name, value in plugins.items()):
                raise ValueError()
            manifest = json.loads(safe_file(Path(installation['plugin_dir']) / '.codex-plugin/plugin.json'))
            version = manifest['version']
            if manifest['name'] != 'memlane-cognee' or not re.fullmatch(r'[a-zA-Z0-9_+.-]+', version) or version in ('.', '..'):
                raise ValueError()
        except (ValueError, KeyError, TypeError):
            raise PolicyError('traex_installation_requires_review', 409) from None
        path = self.profile / 'plugins/cache' / marketplace / 'memlane-cognee' / version
        verify_artifact({**installation, 'plugin_dir': str(path)})
        return path

    def claude_installed(self, installation):
        raw = safe_file(self.profile / 'plugins/installed_plugins.json')
        try:
            state = json.loads(raw)
            rows = state['plugins']['cognee@' + installation['marketplace_name']]
            matches = [row for row in rows if row.get('scope') == 'user']
            if len(matches) != 1:
                raise ValueError()
            path = Path(matches[0]['installPath'])
            if not path.is_absolute() or self.profile / 'plugins/cache' not in path.parents:
                raise ValueError()
        except (ValueError, KeyError, TypeError):
            raise PolicyError('claude_installation_requires_review', 409) from None
        verify_artifact({**installation, 'plugin_dir': str(path)})
        return path

    def preview(self):
        baseline = safe_file(self.target)
        record = self.registry.state.get('connections', {}).get(self.id, {})
        if self.kind == 'pi' and baseline and sha(baseline) != record.get('proposed_sha256'):
            raise PolicyError('existing_pi_extension_requires_review', 409)
        if self.kind == 'pi':
            settings_raw = safe_file(self.profile / 'settings.json')
            try:
                settings = json.loads(settings_raw) if settings_raw else {}
            except ValueError:
                raise PolicyError('pi_configuration_requires_review', 409) from None
            configured = settings.get('packages', []) + settings.get('extensions', [])
            if any('cognee' in str(item).lower() for item in configured):
                raise PolicyError('duplicate_pi_memory_extension_requires_review', 409)
            # Auto-discovered peers also count. Do not inspect their code or
            # remove them; installation remains blocked until disambiguated.
            directory = self.profile / 'extensions'
            if directory.exists() and any('cognee' in path.name.lower() and path.name != 'memlane-cognee'
                                          for path in directory.iterdir()):
                raise PolicyError('duplicate_pi_memory_extension_requires_review', 409)
        elif self.kind == 'traex':
            try:
                plugins = tomlkit.parse(baseline.decode()).get('plugins', {})
            except (ValueError, UnicodeError):
                raise PolicyError('traex_configuration_requires_review', 409) from None
            if any('cognee' in name.lower() and value.get('enabled', False)
                   and not (record.get('activated') and name == record.get('enabled_plugin', 'memlane-cognee@local'))
                   for name, value in plugins.items()):
                raise PolicyError('duplicate_traex_memory_plugin_requires_review', 409)
        elif self.kind == 'claude_code':
            try:
                settings = json.loads(baseline) if baseline else {}
            except ValueError:
                raise PolicyError('claude_configuration_requires_review', 409) from None
            if any('cognee' in name.lower() and enabled
                   and not (record.get('activated') and name == record.get('enabled_plugin'))
                   for name, enabled in settings.get('enabledPlugins', {}).items()):
                raise PolicyError('duplicate_claude_memory_plugin_requires_review', 409)
        else:
            plugin = self.target.parent
            if plugin.exists():
                marker = json.loads(baseline) if baseline else {}
                if marker.get('instance_id') != self.id:
                    raise PolicyError('cursor_local_plugin_requires_review', 409)
            plugins = self.profile / 'plugins'
            if plugins.exists() and any('cognee' in path.name.lower() and path != plugin and plugin not in path.parents
                                         for path in plugins.rglob('*') if path.is_dir()):
                raise PolicyError('duplicate_cursor_memory_plugin_requires_review', 409)
            # Cursor may import enabled Claude plugins as a second hook path.
            claude_profile = self.config.get('agent_hosts', {}).get('claude_code', {}).get('profile', str(Path.home() / '.claude'))
            raw = safe_file(Path(claude_profile).expanduser() / 'settings.json')
            try:
                imported = json.loads(raw) if raw else {}
            except ValueError:
                raise PolicyError('cursor_third_party_import_requires_review', 409) from None
            if any('cognee' in name.lower() and enabled for name, enabled in imported.get('enabledPlugins', {}).items()):
                raise PolicyError('cursor_third_party_import_requires_review', 409)
        return {'instance_id': self.id, 'kind': self.kind, 'target': str(self.target),
                'baseline_sha256': sha(baseline), 'preserve_other_plugins': True,
                'requires_reload': True, 'requires_host_trust': True}

    async def prepare(self, expected):
        r = self.registry
        r.check_revision(expected)
        if not self.executable or not Path(self.executable).is_file():
            raise PolicyError('host_executable_unavailable', 503)
        preview = self.preview()
        record = r.state.setdefault('connections', {}).get(self.id, {})
        if record.get('status') in ('activation_unknown', 'preparation_unknown'):
            raise PolicyError('connection_result_requires_review', 409)
        if self.id not in r.state['agents']:
            await r.provision_agent(expected, self.id, '本机 ' + {'pi': 'pi', 'traex': 'TraeX', 'claude_code': 'Claude Code', 'cursor': 'Cursor'}[self.kind])
        async with r.mutex:
            previous_plugin = record.get('enabled_plugin') if record.get('activated') else record.get('previous_plugin')
            if self.kind == 'traex' and record.get('activated') and not previous_plugin:
                previous_plugin = 'memlane-cognee@local'
            record = {'status': 'preparation_unknown', 'preview': preview, 'previous_plugin': previous_plugin}
            r.state['connections'][self.id] = record
            r.state['revision'] += 1
            r.save()
            from project_installation import prepare
            agent = r.state['agents'][self.id]
            control = self.config.get('project_control', {})
            installation = prepare(control.get('plugin_dir') or self.config['plugin_dir'], r.path.parent,
                                   self.id, agent, r.official.url, control.get('url') or self.config['project_control_url'],
                                   self.config.get('agent_host_home', str(Path.home())),
                                   host_kind=self.kind, python=self.config['python'])
            agent.update(kind=self.kind, host_profile_fingerprint=sha(str(self.profile).encode()),
                         entrypoints=['ide'] if self.kind == 'cursor' else ['cli'], capabilities=self.view()['capabilities'], installation=installation)
            if self.kind == 'pi':
                stub = 'export { default } from ' + json.dumps(str(Path(installation['plugin_dir']) / 'extension/index.ts')) + ';\n'
                private_write(self.root / 'extension-proposed.ts', stub.encode())
                record['proposed_sha256'] = sha(stub.encode())
            else:
                record['proposed_sha256'] = installation['manifest_sha256']
            record['preview']['proposed_sha256'] = record['proposed_sha256']
            record['status'] = 'prepared'
            r.save()
            return self.view()

    async def activate(self, expected, reviewed_sha):
        r = self.registry
        async with r.mutex:
            r.check_revision(expected)
            record = r.state.get('connections', {}).get(self.id, {})
            if record.get('status') != 'prepared' or reviewed_sha != record.get('proposed_sha256'):
                raise PolicyError('reviewed_prepared_connection_required', 409)
            if sha(safe_file(self.target)) != record['preview']['baseline_sha256']:
                raise PolicyError('host_configuration_changed', 409)
            installation = r.state['agents'][self.id]['installation']
            verify_artifact(installation)
            record.update(status='activation_unknown', sessions_before_activation=[
                key for key in r.state['sessions'] if json.loads(key)[0] == self.id])
            r.state['revision'] += 1
            r.save()
            if self.kind == 'pi':
                stub = safe_file(self.root / 'extension-proposed.ts')
                if sha(stub) != reviewed_sha:
                    raise PolicyError('connection_preview_changed', 409)
                # Existing conflicting files and symlink parents were rejected
                # before journaling. Recheck immediately before atomic replace.
                if sha(safe_file(self.target)) != record['preview']['baseline_sha256']:
                    raise PolicyError('host_configuration_changed', 409)
                private_write(self.target, stub)
            elif self.kind == 'cursor':
                target = self.target.parent
                if target.exists():
                    # Keep every active generation immutable under its host.
                    verify_artifact({**installation, 'plugin_dir': str(target)})
                else:
                    target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                    shutil.copytree(installation['plugin_dir'], target)
                    verify_artifact({**installation, 'plugin_dir': str(target)})
            else:
                env = {key: value for key, value in os.environ.items()
                       if not key.startswith(('COGNEE_', 'MEMLANE_', 'TRAE_', 'TRAECLI_'))}
                if self.kind == 'claude_code':
                    env['CLAUDE_CONFIG_DIR'] = str(self.profile)
                    commands = [['plugin', 'marketplace', 'add', installation['marketplace_root']],
                                ['plugin', 'install', 'cognee@' + installation['marketplace_name'], '--scope', 'user']]
                    record['enabled_plugin'] = 'cognee@' + installation['marketplace_name']
                else:
                    env.update(TRAE_HOME=str(self.profile), TRAECLI_HOME=str(self.profile))
                    record['enabled_plugin'] = 'memlane-cognee@' + installation['marketplace_name']
                    commands = [['plugin', 'install', installation['plugin_dir'],
                                 '--marketplace-name', installation['marketplace_name']]]
                    if record.get('previous_plugin') and record['previous_plugin'] != record['enabled_plugin']:
                        commands.append(['plugin', 'disable', record['previous_plugin']])
                r.save()
                try:
                    for arguments in commands:
                        result = await asyncio.to_thread(subprocess.run, [self.executable, *arguments],
                            env=env, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=45)
                        if result.returncode:
                            raise PolicyError('host_installation_requires_review', 409)
                except subprocess.TimeoutExpired:
                    raise PolicyError('host_installation_result_unknown', 503) from None
                if result.returncode:
                    raise PolicyError('host_installation_requires_review', 409)
                if self.kind == 'claude_code':
                    self.claude_installed(installation)
                else:
                    self.traex_installed(installation)
            record.update(status='configured_pending_new_session', activated=True)
            r.save()
            return self.view()

    async def review(self, expected):
        r = self.registry
        async with r.mutex:
            r.check_revision(expected)
            record = r.state.get('connections', {}).get(self.id, {})
            if record.get('status') == 'activation_unknown' and self.kind == 'pi':
                if sha(safe_file(self.target)) == record.get('proposed_sha256'):
                    verify_artifact(r.state['agents'][self.id]['installation'])
                    record.update(status='configured_pending_new_session', activated=True)
                    r.state['revision'] += 1
                    r.save()
            elif record.get('status') == 'activation_unknown' and self.kind in ('claude_code', 'cursor', 'traex'):
                installation = r.state['agents'][self.id]['installation']
                if self.kind == 'claude_code':
                    self.claude_installed(installation)
                    settings = json.loads(safe_file(self.target))
                    if not settings.get('enabledPlugins', {}).get('cognee@' + installation['marketplace_name']):
                        raise PolicyError('claude_configuration_requires_review', 409)
                elif self.kind == 'traex':
                    self.traex_installed(installation)
                else:
                    verify_artifact({**installation, 'plugin_dir': str(self.target.parent)})
                record.update(status='configured_pending_new_session', activated=True)
                r.state['revision'] += 1
                r.save()
            # Review only the original cache and enabled binding, never install.
            return self.view()


def verify_artifact(installation):
    root = Path(installation['plugin_dir'])
    raw = safe_file(root / '.memlane-project-access.json')
    if sha(raw) != installation['manifest_sha256']:
        raise PolicyError('connection_artifact_changed', 409)
    for name, digest in json.loads(raw)['files'].items():
        if Path(name).is_absolute() or '..' in Path(name).parts or sha(safe_file(root / name)) != digest:
            raise PolicyError('connection_artifact_changed', 409)
    marker_path = root / '.memlane-installation.json'
    marker = json.loads(safe_file(marker_path))
    if (marker_path.stat().st_mode & 0o077 or marker.get('env_file') != installation['env_file']
            or (installation.get('host_kind') and marker.get('host_kind') != installation['host_kind'])
            or (installation.get('instance_id') and marker.get('instance_id') != installation['instance_id'])):
        raise PolicyError('connection_binding_changed', 409)


class Connections:
    def __init__(self, registry, config):
        self.registry, self.adapters = registry, {}
        self.mutex = asyncio.Lock()
        configured = config.get('agent_hosts', {})
        codex = configured.get('codex', {})
        executable = codex.get('executable') or config.get('codex_executable') or shutil.which('codex')
        self.missing_codex = None
        if executable and Path(executable).is_file():
            from project_codex_connection import CodexConnection
            # Separate host discovery from generation runtime/auth fields.
            # Legacy configs keep exactly the same canonical home/instance ID.
            host_config = {**config, 'auth_home': codex.get('profile') or config.get('auth_home') or str(Path.home() / '.codex'),
                           'codex_executable': executable}
            adapter = CodexConnection(registry, host_config)
            self.adapters[adapter.id] = adapter
        else:
            self.missing_codex = {'instance_id': 'codex-unavailable', 'kind': 'codex', 'detected': False,
                                  'status': 'not_connected'}
        for kind, directory, command in [('pi', Path.home() / '.pi/agent', 'pi'), ('traex', Path.home() / '.trae', 'traecli'),
                                          ('claude_code', Path.home() / '.claude', 'claude'), ('cursor', Path.home() / '.cursor', 'cursor')]:
            settings = configured.get(kind, {})
            executable = settings.get('executable') or shutil.which(command)
            if kind == 'cursor' and not executable:
                candidate = Path('/Applications/Cursor.app/Contents/Resources/app/bin/cursor')
                executable = str(candidate) if candidate.is_file() else None
            adapter = NativeConnection(registry, config, kind, settings.get('profile', directory),
                                       executable)
            self.adapters[adapter.id] = adapter

    def view(self):
        rows = [self.missing_codex] if self.missing_codex else []
        for adapter in self.adapters.values():
            try:
                row = adapter.view()
                row.setdefault('kind', 'codex')
            except PolicyError as error:
                row = {'instance_id': adapter.id, 'kind': getattr(adapter, 'kind', 'codex'),
                       'status': 'requires_review', 'error_code': error.code}
            rows.append(row)
        return {'revision': self.registry.state['revision'], 'connections': rows}

    async def perform(self, identifier, action, expected, operation_id, reviewed_sha=None):
        if not isinstance(operation_id, str) or not re.fullmatch(r'[a-zA-Z0-9_-]{1,64}', operation_id):
            raise PolicyError('invalid_operation_id')
        if action not in ('preview', 'prepare', 'activate', 'review') or identifier not in self.adapters:
            raise PolicyError('connection_not_found', 404)
        r, adapter = self.registry, self.adapters[identifier]
        signature = sha(json.dumps([identifier, action, reviewed_sha]).encode())
        async with self.mutex:
            operations = r.state.setdefault('connection_operations', {})
            if operation_id in operations:
                old = operations[operation_id]
                if old['signature'] != signature:
                    raise PolicyError('operation_id_conflict', 409)
                if old['status'] != 'complete':
                    raise PolicyError('connection_operation_requires_review', 409)
                return copy.deepcopy(old['result'])
            async with r.mutex:
                r.check_revision(expected)
                operations[operation_id] = {'signature': signature, 'status': 'unknown'}
                r.state['revision'] += 1
                r.save()
            try:
                if action == 'preview':
                    result = adapter.preview() if isinstance(adapter, NativeConnection) else adapter.view()
                elif action == 'activate':
                    result = await adapter.activate(r.state['revision'], reviewed_sha)
                else:
                    result = await getattr(adapter, action)(r.state['revision'])
            except PolicyError as error:
                async with r.mutex:
                    operations[operation_id]['error_code'] = error.code
                    r.save()
                raise
            async with r.mutex:
                result = {'revision': r.state['revision'], 'connection': result}
                operations[operation_id].update(status='complete', result=copy.deepcopy(result))
                r.save()
            return result
