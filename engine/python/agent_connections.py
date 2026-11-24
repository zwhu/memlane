"""Managed computer-level connections. Host paths come only from runtime config."""
import asyncio
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import time
import tomlkit

from project_control import loopback_origin, private_write
from project_policy import PolicyError


UPGRADE_FIELDS = ('prepared_installation', 'active_installation', 'active_record')


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
                                     'cursor': 'hooks.json', 'trae_ide': 'hooks.json'}[kind]

    def component_source_available(self):
        source = self.config.get('project_control', {}).get('plugin_dir') or self.config.get('plugin_dir')
        if self.kind not in ('pi', 'traex', 'trae_ide'):
            from project_host_plugin import archive_available
            return archive_available(source)
        if not isinstance(source, (str, Path)) or not source:
            return False
        source = Path(source)
        marker = source / '.memlane-project-access.json'
        return source.is_absolute() and not source.is_symlink() and source.is_dir() and not marker.is_symlink() and marker.is_file()

    def view(self):
        record = self.registry.state.get('connections', {}).get(self.id, {})
        status = record.get('status', 'not_connected' if self.component_source_available() else 'component_unavailable')
        result = {'instance_id': self.id, 'kind': self.kind, 'profile': str(self.profile),
                  'detected': bool(self.executable and Path(self.executable).is_file()),
                  'status': status,
                  'requires_reload': True, 'entrypoints': ['ide'] if self.kind in ('cursor', 'trae_ide') else ['cli'],
                  'capabilities': {key: {'available': True, 'verified': False}
                                   for key in ('explicit_memory', 'auto_capture', 'auto_recall', 'project_policy')},
                  'preview': copy.deepcopy(record.get('preview'))}
        if self.kind == 'cursor':
            result['capabilities']['auto_recall']['condition'] = 'session_start_or_first_non_read_tool; no_tool_turn_not_supported'
        if self.kind == 'trae_ide':
            result['capabilities']['explicit_memory']['available'] = False
            result['capabilities']['auto_capture']['condition'] = 'single_workspace_live_turns'
        if self.kind == 'claude_code':
            result['capabilities']['auto_capture']['condition'] = 'bound_main_session; sidechains_require_independent_binding'
        result['new_sessions'] = sum(json.loads(key)[0] == self.id and key not in record.get('sessions_before_activation', [])
                                     for key in self.registry.state['sessions']) if record.get('activated') else 0
        if result['status'] == 'configured_pending_new_session' and self.kind == 'pi':
            if sha(safe_file(self.target)) != record.get('proposed_sha256'):
                result['status'] = 'requires_review'
        if result['status'] == 'configured_pending_new_session' and self.kind == 'cursor':
            installation = self.registry.state['agents'][self.id]['installation']
            self.cursor_installed(installation, record)
        if result['status'] == 'configured_pending_new_session' and self.kind == 'trae_ide':
            self.trae_ide_installed(self.registry.state['agents'][self.id]['installation'], record)
        if result['status'] == 'configured_pending_new_session' and self.kind == 'traex':
            self.traex_installed(self.registry.state['agents'][self.id]['installation'])
        if result['status'] == 'configured_pending_new_session' and self.kind == 'claude_code':
            self.claude_installed(self.registry.state['agents'][self.id]['installation'])
        return result

    def cursor_installed(self, installation, record):
        verify_artifact(installation)
        from cursor_connection import proposal
        raw = safe_file(self.target)
        hook = record.get('cursor_next_hook')
        if not isinstance(hook, dict) or sha(raw) != record.get('cursor_proposal_sha256'):
            raise PolicyError('cursor_loader_changed_requires_review', 409)
        proposal(raw, hook, None)

    def trae_ide_installed(self, installation, record, *, hooks=None):
        from trae_ide_connection import EVENTS, proposal
        expected_hooks = record.get('trae_ide_next_hooks') if hooks is None else hooks
        script = str(Path(installation['plugin_dir']) / 'scripts/trae_ide_hook.py')
        try:
            if not isinstance(expected_hooks, dict) or set(expected_hooks) != set(EVENTS):
                raise ValueError()
            for event, group in expected_hooks.items():
                commands = group['hooks']
                if (len(commands) != 1 or commands[0]['type'] != 'command'
                        or shlex.split(commands[0]['command'])[1:] != ['-B', script, event]):
                    raise ValueError()
        except (KeyError, TypeError, ValueError, AttributeError):
            raise PolicyError('trae_ide_hook_changed_requires_review', 409) from None
        verify_artifact(installation)
        proposal(safe_file(self.target), expected_hooks, None)

    def _verify_active_installation(self, installation, record):
        if (not isinstance(installation, dict) or not isinstance(installation.get('plugin_dir'), (str, Path))
                or not installation['plugin_dir'] or not isinstance(record, dict)
                or record.get('status') != 'configured_pending_new_session' or not record.get('activated')):
            raise PolicyError('connection_artifact_changed', 409)
        installation = {**installation, 'host_kind': self.kind, 'instance_id': self.id}
        if self.kind == 'pi':
            verify_artifact(installation)
            if sha(safe_file(self.target)) != record.get('proposed_sha256'):
                raise PolicyError('existing_pi_extension_requires_review', 409)
        elif self.kind == 'cursor':
            from cursor_connection import loader
            hook = record.get('cursor_hook') or record.get('cursor_next_hook')
            try:
                # The path is escaped inside the loader's Python string literal.
                python = shlex.split(hook['command'])[0]
                if hook != loader(installation, python) or hook != record.get('cursor_next_hook'):
                    raise ValueError()
            except (KeyError, TypeError, ValueError, IndexError, AttributeError):
                raise PolicyError('cursor_loader_changed_requires_review', 409) from None
            self.cursor_installed(installation, record)
        elif self.kind == 'trae_ide':
            hooks = record.get('trae_ide_hooks')
            if hooks is None or hooks != record.get('trae_ide_next_hooks'):
                raise PolicyError('trae_ide_hook_changed_requires_review', 409)
            self.trae_ide_installed(installation, record, hooks=hooks)
        elif self.kind == 'traex':
            verify_artifact(installation)
            self.traex_installed(installation)
        elif self.kind == 'claude_code':
            verify_artifact(installation)
            self.claude_installed(installation)

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
            enabled = 'cognee@' + installation['marketplace_name']
            plugins = json.loads(safe_file(self.target)).get('enabledPlugins', {})
            if plugins.get(enabled) is not True or any(
                    name != enabled and 'cognee' in name.lower() and active
                    for name, active in plugins.items()):
                raise ValueError()
            state = json.loads(raw)
            rows = state['plugins'][enabled]
            matches = [row for row in rows if row.get('scope') == 'user']
            if len(matches) != 1:
                raise ValueError()
            path = Path(matches[0]['installPath'])
            if not path.is_absolute() or self.profile / 'plugins/cache' not in path.parents:
                raise ValueError()
        except (ValueError, KeyError, TypeError, AttributeError):
            raise PolicyError('claude_installation_requires_review', 409) from None
        verify_artifact({**installation, 'plugin_dir': str(path)})
        return path

    def preview(self):
        baseline = safe_file(self.target)
        record = self.registry.state.get('connections', {}).get(self.id, {})
        if self.kind == 'pi':
            active = record.get('active_record')
            pi_record = active if record.get('activated') and isinstance(active, dict) else record
            if baseline and sha(baseline) != pi_record.get('proposed_sha256'):
                raise PolicyError('existing_pi_extension_requires_review', 409)
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
        elif self.kind == 'trae_ide':
            from trae_ide_connection import proposal
            proposal(baseline, record.get('trae_ide_hooks'), None)
        else:
            from cursor_connection import proposal
            proposal(baseline, record.get('cursor_hook'), None)
            plugins = self.profile / 'plugins'
            if plugins.exists() and any('cognee' in path.name.lower()
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
        if not self.component_source_available():
            raise PolicyError('managed_plugin_input_mismatch', 503)
        previous = r.state.get('connections', {}).get(self.id, {})
        if previous.get('status') in ('activation_unknown', 'preparation_unknown'):
            raise PolicyError('connection_result_requires_review', 409)
        if self.id not in r.state['agents'] and not previous.get('activated'):
            self.preview()
            directory = await r.provision_agent(expected, self.id, '本机 ' + {'pi': 'pi', 'traex': 'TRAE CLI', 'trae_ide': 'TRAE IDE', 'claude_code': 'Claude Code', 'cursor': 'Cursor', 'antigravity': 'Gemini App'}[self.kind])
            expected = directory['revision']
        async with r.mutex:
            previous = copy.deepcopy(r.state.get('connections', {}).get(self.id, {}))
            if previous.get('status') in ('activation_unknown', 'preparation_unknown'):
                raise PolicyError('connection_result_requires_review', 409)
            r.check_revision(expected)
            agent = r.state['agents'].get(self.id)
            if agent is None:
                raise PolicyError('agent_creation_requires_review', 409)
            active_installation = active_record = None
            if self.kind != 'antigravity' and previous.get('activated'):
                active_installation = previous.get('active_installation')
                if active_installation is None and self.kind == 'cursor':
                    active_installation = previous.get('cursor_active_installation')
                if active_installation is None:
                    active_installation = agent.get('installation')
                active_record = previous.get('active_record')
                if active_record is None and previous.get('status') == 'configured_pending_new_session':
                    active_record = {key: value for key, value in previous.items() if key not in UPGRADE_FIELDS}
                self._verify_active_installation(active_installation, active_record)
            preview = self.preview()
            current = active_record if active_record is not None else previous
            previous_plugin = current.get('enabled_plugin') if current.get('activated') else current.get('previous_plugin')
            if self.kind == 'traex' and current.get('activated') and not previous_plugin:
                previous_plugin = 'memlane-cognee@local'
            record = {'status': 'preparation_unknown', 'preview': preview, 'previous_plugin': previous_plugin}
            if active_installation is not None:
                record.update(activated=True, active_installation=copy.deepcopy(active_installation),
                              active_record=copy.deepcopy(active_record))
                for key in ('activated_at', 'upgraded_at', 'environment_receipt_required',
                            'executable_fingerprint', 'sessions_before_activation', 'enabled_plugin'):
                    if key in active_record:
                        record[key] = copy.deepcopy(active_record[key])
            if self.kind == 'cursor':
                record['cursor_hook'] = current.get('cursor_hook') or current.get('cursor_next_hook')
                record['cursor_active_installation'] = copy.deepcopy(
                    active_installation if active_installation is not None else current.get('cursor_active_installation'))
            if self.kind == 'trae_ide':
                record['trae_ide_hooks'] = current.get('trae_ide_hooks')
            r.state.setdefault('connections', {})[self.id] = record
            r.state['revision'] += 1
            r.save()
            from project_installation import prepare
            control = self.config.get('project_control', {})
            control_url = control.get('url') or self.config['project_control_url']
            gateway_url = loopback_origin(control_url) + '/cognee'
            # Rebinding a service must not move pending buffers to a new HOME.
            installation = prepare(control.get('plugin_dir') or self.config.get('plugin_dir'), r.path.parent,
                                   self.id, agent, gateway_url, control_url,
                                   self.config.get('agent_host_home', agent.get('installation', {}).get('home') or str(Path.home())),
                                   host_kind=self.kind, python=self.config['python'])
            if self.kind in ('cursor', 'trae_ide'):
                marker = Path(installation['plugin_dir']) / '.memlane-installation.json'
                marker_data = json.loads(marker.read_text())
                marker_data['runtime_profile'] = str(self.profile)
                if self.kind == 'trae_ide':
                    marker_data.update(hook_origin=loopback_origin(control_url),
                                       hook_token_file=agent['control_token_file'])
                private_write(marker, json.dumps(marker_data).encode())
            if active_installation is not None:
                record['prepared_installation'] = copy.deepcopy(installation)
            agent.update(kind=self.kind, host_profile=str(self.profile), host_profile_fingerprint=sha(str(self.profile).encode()),
                         entrypoints=['ide'] if self.kind in ('cursor', 'trae_ide') else ['cli'], capabilities=self.view()['capabilities'],
                         installation=copy.deepcopy(active_installation) if active_installation is not None else installation)
            if self.kind == 'pi':
                stub = pi_stub(installation)
                private_write(self.root / 'extension-proposed.ts', stub)
                record['proposed_sha256'] = sha(stub)
            elif self.kind == 'cursor':
                from cursor_connection import loader, proposal
                record['cursor_next_hook'] = loader(installation, self.config['python'])
                proposed = proposal(safe_file(self.target), record['cursor_hook'], record['cursor_next_hook'])
                private_write(self.root / 'cursor-hooks-proposed.json', proposed)
                record['cursor_proposal_sha256'] = sha(proposed)
                record['proposed_sha256'] = sha((installation['manifest_sha256'] + sha(proposed)).encode())
            elif self.kind == 'trae_ide':
                from trae_ide_connection import loader, proposal
                next_hooks = loader(installation, self.config['python'])
                proposed = proposal(safe_file(self.target), record['trae_ide_hooks'], next_hooks)
                record['trae_ide_next_hooks'] = next_hooks
                record['proposed_sha256'] = sha((installation['manifest_sha256'] + sha(proposed)).encode())
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
            agent = r.state['agents'][self.id]
            installation = record.get('prepared_installation') or agent['installation']
            verify_artifact(installation)
            if self.kind == 'pi':
                stub = safe_file(self.root / 'extension-proposed.ts')
                if sha(stub) != reviewed_sha:
                    raise PolicyError('connection_preview_changed', 409)
            if self.kind == 'cursor':
                proposed = safe_file(self.root / 'cursor-hooks-proposed.json')
                if (sha(proposed) != record.get('cursor_proposal_sha256')
                        or sha((installation['manifest_sha256'] + sha(proposed)).encode()) != reviewed_sha):
                    raise PolicyError('connection_preview_changed', 409)
                # Recheck duplicate imports before any host mutation.
                self.preview()
            if self.kind == 'trae_ide':
                from trae_ide_connection import proposal
                proposed = proposal(safe_file(self.target), record['trae_ide_hooks'], record['trae_ide_next_hooks'])
                if sha((installation['manifest_sha256'] + sha(proposed)).encode()) != reviewed_sha:
                    raise PolicyError('connection_preview_changed', 409)
            record.update(status='activation_unknown', sessions_before_activation=[
                key for key in r.state['sessions'] if json.loads(key)[0] == self.id])
            if record.get('prepared_installation'):
                agent['installation'] = copy.deepcopy(installation)
            r.state['revision'] += 1
            r.save()
            if self.kind == 'pi':
                # Existing conflicting files and symlink parents were rejected
                # before journaling. Recheck immediately before atomic replace.
                if sha(safe_file(self.target)) != record['preview']['baseline_sha256']:
                    raise PolicyError('host_configuration_changed', 409)
                private_write(self.target, stub)
            elif self.kind == 'cursor':
                if sha(safe_file(self.target)) != record['preview']['baseline_sha256']:
                    raise PolicyError('host_configuration_changed', 409)
                private_write(self.target, proposed)
                self.cursor_installed(installation, record)
                record['cursor_hook'] = record['cursor_next_hook']
                record['cursor_active_installation'] = copy.deepcopy(installation)
            elif self.kind == 'trae_ide':
                if sha(safe_file(self.target)) != record['preview']['baseline_sha256']:
                    raise PolicyError('host_configuration_changed', 409)
                private_write(self.target, proposed)
                self.trae_ide_installed(installation, record)
                record['trae_ide_hooks'] = record['trae_ide_next_hooks']
            else:
                env = {key: value for key, value in os.environ.items()
                       if not key.startswith(('COGNEE_', 'MEMLANE_', 'TRAE_', 'TRAECLI_'))}
                env['HOME'] = installation['home']
                if self.kind == 'claude_code':
                    env['CLAUDE_CONFIG_DIR'] = str(self.profile)
                    commands = [['plugin', 'marketplace', 'add', installation['marketplace_root']],
                                ['plugin', 'install', 'cognee@' + installation['marketplace_name'], '--scope', 'user']]
                    record['enabled_plugin'] = 'cognee@' + installation['marketplace_name']
                    if record.get('previous_plugin') and record['previous_plugin'] != record['enabled_plugin']:
                        commands.append(['plugin', 'disable', record['previous_plugin'], '--scope', 'user'])
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
            for key in UPGRADE_FIELDS:
                record.pop(key, None)
            r.save()
            return self.view()

    async def review(self, expected):
        r = self.registry
        async with r.mutex:
            r.check_revision(expected)
            record = r.state.get('connections', {}).get(self.id, {})
            status = record.get('status')
            has_active_snapshot = (record.get('activated') and isinstance(record.get('active_record'), dict)
                                   and isinstance(record.get('active_installation'), dict))
            if status == 'preparation_unknown' or (status == 'prepared' and has_active_snapshot):
                baseline = safe_file(self.target)
                baseline_sha = (record.get('preview') or {}).get('baseline_sha256')
                if baseline_sha is not None and sha(baseline) != baseline_sha:
                    raise PolicyError('host_configuration_changed', 409)
                agent = r.state['agents'].get(self.id)
                active = (record['active_installation'], record['active_record']) if has_active_snapshot else None
                if active is None:
                    if record.get('activated') or any(record.get(key) is not None for key in UPGRADE_FIELDS):
                        raise PolicyError('connection_artifact_changed', 409)
                    active = self._legacy_active_preparation(record, (agent or {}).get('installation'), baseline)
                if active is not None:
                    if agent is None:
                        raise PolicyError('connection_artifact_changed', 409)
                    installation, restored_record = active
                    self._verify_active_installation(installation, restored_record)
                    r.state['connections'][self.id] = copy.deepcopy(restored_record)
                    agent['installation'] = copy.deepcopy(installation)
                else:
                    self._verify_staged_artifacts()
                    self.preview()
                    del r.state['connections'][self.id]
                    if agent is not None:
                        agent.pop('installation', None)
                r.state['revision'] += 1
                result = {'revision': r.state['revision'], 'connection': self.view()}
                if status == 'preparation_unknown':
                    for operation in r.state.get('connection_operations', {}).values():
                        if (operation.get('instance_id') == self.id and operation.get('action') == 'prepare'
                                and operation.get('status') != 'complete'):
                            operation.update(status='complete', outcome='rejected',
                                             error_code=f'{self.kind}_preparation_did_not_install', result=result)
                r.save()
                return result['connection']
            if status == 'activation_unknown':
                installation = r.state['agents'][self.id]['installation']
                if self.kind == 'pi':
                    if sha(safe_file(self.target)) != record.get('proposed_sha256'):
                        return self.view()
                    verify_artifact(installation)
                elif self.kind == 'claude_code':
                    self.claude_installed(installation)
                    settings = json.loads(safe_file(self.target))
                    if not settings.get('enabledPlugins', {}).get('cognee@' + installation['marketplace_name']):
                        raise PolicyError('claude_configuration_requires_review', 409)
                elif self.kind == 'traex':
                    self.traex_installed(installation)
                elif self.kind == 'trae_ide':
                    self.trae_ide_installed(installation, record)
                    record['trae_ide_hooks'] = record['trae_ide_next_hooks']
                else:
                    self.cursor_installed(installation, record)
                    record['cursor_hook'] = record['cursor_next_hook']
                    record['cursor_active_installation'] = copy.deepcopy(installation)
                record.update(status='configured_pending_new_session', activated=True)
                for key in UPGRADE_FIELDS:
                    record.pop(key, None)
                r.state['revision'] += 1
                r.save()
            # Review only the original cache and enabled binding, never install.
            return self.view()

    def _legacy_active_preparation(self, record, installation, baseline):
        """Recover the active binding from fields written by older prepare journals."""
        active = {'status': 'configured_pending_new_session', 'activated': True}
        if self.kind == 'trae_ide' and record.get('trae_ide_hooks') is not None:
            hooks = record['trae_ide_hooks']
            active.update(trae_ide_hooks=hooks, trae_ide_next_hooks=hooks)
            return installation, active
        if self.kind == 'cursor' and record.get('cursor_hook') is not None:
            hook = record['cursor_hook']
            installation = record.get('cursor_active_installation')
            active.update(cursor_hook=hook, cursor_next_hook=hook, cursor_proposal_sha256=sha(baseline),
                          cursor_active_installation=installation)
            return installation, active
        if self.kind in ('claude_code', 'traex') and record.get('previous_plugin'):
            marketplace = installation.get('marketplace_name') if isinstance(installation, dict) else None
            if not isinstance(marketplace, str) or not marketplace:
                raise PolicyError('connection_artifact_changed', 409)
            prefix = 'cognee@' if self.kind == 'claude_code' else 'memlane-cognee@'
            active['enabled_plugin'] = prefix + marketplace
            return installation, active
        if self.kind == 'pi' and isinstance(installation, dict) and baseline:
            plugin_dir = installation.get('plugin_dir')
            if not isinstance(plugin_dir, str) or not plugin_dir or sha(pi_stub(installation)) != sha(baseline):
                raise PolicyError('existing_pi_extension_requires_review', 409)
            active['proposed_sha256'] = sha(baseline)
            return installation, active
        if any(record.get(key) is not None for key in
               ('trae_ide_hooks', 'cursor_hook', 'cursor_active_installation', 'previous_plugin')):
            raise PolicyError('connection_artifact_changed', 409)
        return None

    def _verify_staged_artifacts(self):
        artifacts = self.registry.path.parent / 'managed-marketplaces' / self.id
        try:
            if any(path.is_symlink() for path in (artifacts, *artifacts.parents)):
                raise ValueError()
            if not artifacts.exists():
                return
            for generation in artifacts.iterdir():
                plugins = generation / 'plugins'
                plugin_dir = plugins / 'cognee'
                if any(path.is_symlink() or not path.is_dir() for path in (generation, plugins, plugin_dir)):
                    raise ValueError()
                verify_manifest_files(plugin_dir, safe_file(plugin_dir / '.memlane-project-access.json'))
                marker_path = plugin_dir / '.memlane-installation.json'
                marker = json.loads(safe_file(marker_path))
                if (not isinstance(marker, dict) or marker_path.stat().st_mode & 0o077
                        or marker.get('instance_id') != self.id or marker.get('host_kind') != self.kind):
                    raise ValueError()
        except (PolicyError, OSError, ValueError, KeyError, TypeError):
            raise PolicyError('connection_artifact_changed', 409) from None


def pi_stub(installation):
    entrypoint = str(Path(installation['plugin_dir']) / 'extension/index.ts')
    return ('export { default } from ' + json.dumps(entrypoint) + ';\n').encode()


def verify_artifact(installation):
    try:
        root = Path(installation['plugin_dir'])
        raw = safe_file(root / '.memlane-project-access.json')
        if sha(raw) != installation['manifest_sha256']:
            raise PolicyError('connection_artifact_changed', 409)
        verify_manifest_files(root, raw)
        marker_path = root / '.memlane-installation.json'
        marker = json.loads(safe_file(marker_path))
        if (not isinstance(marker, dict) or marker_path.stat().st_mode & 0o077
                or marker.get('env_file') != installation['env_file']
                or (installation.get('host_kind') and marker.get('host_kind') != installation['host_kind'])
                or (installation.get('instance_id') and marker.get('instance_id') != installation['instance_id'])):
            raise PolicyError('connection_binding_changed', 409)
    except (OSError, ValueError, KeyError, TypeError):
        raise PolicyError('connection_artifact_changed', 409) from None


def verify_manifest_files(root, raw):
    files = json.loads(raw)['files']
    if not isinstance(files, dict) or not files:
        raise PolicyError('connection_artifact_changed', 409)
    for name, digest in files.items():
        path = root / name
        if Path(name).is_absolute() or '..' in Path(name).parts or not path.is_file() or sha(safe_file(path)) != digest:
            raise PolicyError('connection_artifact_changed', 409)


def detect_executable(kind, settings, legacy=None):
    """Read-only discovery; explicit host paths are authoritative."""
    configured = settings.get('executable') or legacy
    if configured:
        path = Path(configured).expanduser()
        return str(path) if path.is_absolute() and path.is_file() and os.access(path, os.X_OK) else None
    commands = {'codex': ('codex',), 'pi': ('pi',), 'traex': ('traecli', 'traex'), 'trae_ide': (),
                           'claude_code': ('claude',), 'cursor': ('cursor',), 'opencode': ('opencode',), 'antigravity': ('agy',)}[kind]
    for command in commands:
        found = shutil.which(command)
        if found:
            return found
    home = Path.home()
    candidates = [base / command for base in (home / '.local/bin', home / '.bun/bin',
                  Path('/opt/homebrew/bin'), Path('/usr/local/bin')) for command in commands]
    if kind == 'opencode':
        candidates.append(home / '.opencode/bin/opencode')
    if kind == 'codex':
        candidates.extend(base / app / 'Contents/Resources/codex-cli/bin/codex'
                          for base in (Path('/Applications'), home / 'Applications')
                          for app in ('Codex.app', 'ChatGPT.app'))
    if kind == 'antigravity':
        candidates.extend(base / 'Gemini.app/Contents/Helpers/agy'
                          for base in (Path('/Applications'), home / 'Applications'))
    if kind == 'trae_ide':
        candidates.extend(base / 'Trae CN.app/Contents/MacOS/Electron'
                          for base in (Path('/Applications'), home / 'Applications'))
    if kind == 'cursor':
        candidates.extend(base / 'Cursor.app/Contents/Resources/app/bin/cursor'
                          for base in (Path('/Applications'), home / 'Applications'))
    return next((str(path) for path in candidates if path.is_file() and os.access(path, os.X_OK)), None)


class _StateSnapshotRegistry:
    def __init__(self, registry):
        self.path = registry.path
        self.state = copy.deepcopy({key: registry.state[key] for key in (
            'revision', 'host_targets', 'agents', 'enrollments', 'connections',
            'codex_connections', 'connection_receipts', 'sessions') if key in registry.state})


class Connections:
    def __init__(self, registry, config):
        self.registry, self.adapters = registry, {}
        self.mutex = asyncio.Lock()
        self.config = config
        self._discover_generation = 0
        from connection_capture import ConnectionCapture
        self.capture = ConnectionCapture(registry, config)
        self.discover()

    def discover(self):
        self.environments, self.adapters, self.missing_codex = self._discover(self.registry, self.config)
        self._discover_generation += 1

    @staticmethod
    def _discover(registry, config):
        from agent_environments import DEFAULTS, environment
        environments = {}
        for kind in DEFAULTS:
            try:
                environments[kind] = environment(config, registry.state, kind)
            except (PolicyError, OSError, ValueError, TypeError):
                environments[kind] = environment({}, {}, kind, environ={})
                environments[kind]['environment_issue'] = 'environment_unavailable'
        configured = {kind: {**config.get('agent_hosts', {}).get(kind, {}), 'profile': row['profile']}
                      for kind, row in environments.items()}
        adapters = {}
        codex = configured.get('codex', {})
        executable = detect_executable('codex', codex, config.get('codex_executable'))
        missing_codex = None
        if executable and Path(executable).is_file():
            from project_codex_connection import CodexConnection
            # Separate host discovery from generation runtime/auth fields.
            # Legacy configs keep exactly the same canonical home/instance ID.
            host_config = {**config, 'auth_home': codex.get('profile') or config.get('auth_home') or str(Path.home() / '.codex'),
                           'codex_executable': executable}
            adapter = CodexConnection(registry, host_config)
            adapters[adapter.id] = adapter
        else:
            missing_codex = {'instance_id': 'codex-unavailable', 'kind': 'codex', 'detected': False,
                             'status': 'not_connected'}
        for kind, directory in [('pi', Path.home() / '.pi/agent'), ('traex', Path.home() / '.trae'),
                                ('trae_ide', Path.home() / '.trae-cn'),
                                ('claude_code', Path.home() / '.claude'), ('cursor', Path.home() / '.cursor')]:
            settings = configured.get(kind, {})
            executable = detect_executable(kind, settings)
            adapter = NativeConnection(registry, config, kind, settings.get('profile', directory),
                                       executable)
            adapters[adapter.id] = adapter

        from opencode_connection import OpenCodeConnection
        settings = configured.get('opencode', {})
        directory = Path(os.environ.get('XDG_CONFIG_HOME') or Path.home() / '.config') / 'opencode'
        adapter = OpenCodeConnection(registry, config, settings.get('profile', directory),
                                     detect_executable('opencode', settings))
        adapters[adapter.id] = adapter

        from antigravity_connection import AntigravityConnection
        settings = configured.get('antigravity', {})
        adapter = AntigravityConnection(registry, config, settings.get('profile', Path.home() / '.gemini/config'),
                                        detect_executable('antigravity', settings))
        adapters[adapter.id] = adapter
        return environments, adapters, missing_codex

    @staticmethod
    def _connection_rows(registry, environments, adapters, missing_codex):
        known_codex = any(getattr(a, 'kind', 'codex') == 'codex' for a in adapters.values())
        rows = [missing_codex] if missing_codex and not known_codex else []
        for adapter in adapters.values():
            try:
                row = adapter.view()
                row.setdefault('kind', 'codex')
                if row['kind'] == 'codex' and missing_codex:
                    row['detected'] = False
            except PolicyError as error:
                row = {'instance_id': adapter.id, 'kind': getattr(adapter, 'kind', 'codex'),
                       'status': 'requires_review', 'error_code': error.code}
            details = environments[row['kind']]
            row.update(details)
            if details['environment_issue']:
                row['status'] = details['environment_issue']
            records = 'codex_connections' if row['kind'] == 'codex' else 'connections'
            record = registry.state.get(records, {}).get(adapter.id, {})
            receipt = registry.state.get('connection_receipts', {}).get(adapter.id)
            row['last_receipt_at'] = receipt.get('received_at', 0) if receipt else 0
            receipt_profile_verified = bool(
                receipt and receipt.get('environment_verified')
                and (not receipt.get('profile') or receipt.get('profile') == row.get('profile'))
            )
            row['environment_verified'] = bool(
                receipt_profile_verified
                and receipt.get('received_at', 0) >= record.get('activated_at', 0)
            )
            row['awaiting_post_upgrade_receipt'] = bool(
                receipt_profile_verified
                and not row['environment_verified']
                and record.get('upgraded_at')
            )
            row['plugin_version'] = receipt.get('plugin_version', '') if receipt else ''
            if (row['status'] == 'configured_pending_new_session' and row.get('new_sessions')
                    and record.get('environment_receipt_required') and not row['environment_verified']):
                row['status'] = 'environment_receipt_unconfirmed'
            if row['status'] == 'configured_pending_new_session' and not row.get('new_sessions'):
                activated_at = record.get('activated_at')
                row['receipt_overdue'] = not activated_at or time.time() - activated_at > 120
            try:
                binary = Path(getattr(adapter, 'executable', '')).stat()
                row['executable_changed'] = bool(record.get('executable_fingerprint') and
                    record['executable_fingerprint'] != [binary.st_ino, binary.st_size, binary.st_mtime_ns])
                if row['executable_changed']:
                    row['status'] = 'requires_review'
                    row['error_code'] = 'host_executable_changed'
            except (OSError, TypeError):
                pass
            rows.append(row)
        return rows

    @classmethod
    def _discover_rows(cls, registry, config):
        environments, adapters, missing_codex = cls._discover(registry, config)
        rows = cls._connection_rows(registry, environments, adapters, missing_codex)
        return environments, adapters, missing_codex, rows

    def _attach_capture(self, rows, adapters):
        for row in rows:
            if (adapter := adapters.get(row['instance_id'])) is None:
                continue
            capture = self.capture.view(row, adapter)
            if capture is not None:
                row['capture'] = capture
                row.setdefault('capabilities', {}).setdefault('auto_capture', {'available': True})['verified'] = capture['state'] == 'healthy'

    def view(self):
        self.discover()
        rows = self._connection_rows(self.registry, self.environments, self.adapters, self.missing_codex)
        self._attach_capture(rows, self.adapters)
        return {'revision': self.registry.state['revision'], 'connections': rows}

    async def checked_view(self, *, force=False):
        from project_codex_connection import CodexConnection

        snapshot = _StateSnapshotRegistry(self.registry)
        config = copy.deepcopy(self.config)
        host_targets_before = snapshot.state.get('host_targets', {})
        generation_before = self._discover_generation
        environments, adapters, missing_codex, rows = await asyncio.to_thread(self._discover_rows, snapshot, config)
        for adapter in adapters.values():
            adapter.registry = self.registry
            if not isinstance(adapter, CodexConnection):
                adapter.config = self.config
        if (self._discover_generation == generation_before
                and self.registry.state.get('host_targets', {}) == host_targets_before):
            self.environments, self.adapters, self.missing_codex = environments, adapters, missing_codex
            self._discover_generation += 1

        await self.capture.refresh(rows, adapters, force=force)
        self._attach_capture(rows, adapters)
        return {'revision': snapshot.state['revision'], 'connections': rows}

    async def select_environment(self, kind, profile, expected):
        from agent_environments import DEFAULTS, profile_path, verification_profile
        if kind not in DEFAULTS:
            raise PolicyError('unknown_host_kind')
        profile = profile_path(profile)
        if verification_profile(profile) and not self.config.get('allow_verification_agent_profiles', False):
            raise PolicyError('verification_environment_not_allowed', 409)
        if not Path(profile).is_dir():
            raise PolicyError('host_environment_not_directory', 409)
        async with self.mutex:
            async with self.registry.mutex:
                self.registry.check_revision(expected)
                self.registry.state.setdefault('host_targets', {})[kind] = profile
                self.registry.state['revision'] += 1
                self.registry.save()
            self.discover()
        return self.view()

    def operation(self, operation_id):
        receipt = self.registry.state.get('connection_operations', {}).get(operation_id)
        if receipt is None:
            return {'recorded': False}
        if receipt.get('action') == 'allow_capture' and receipt.get('status') != 'complete':
            identifier = receipt.get('instance_id')
            adapter = self.adapters.get(identifier)
            if adapter is not None:
                row = next((r for r in self.view()['connections'] if r['instance_id'] == identifier), {})
                capture = row.get('capture', {})
                if capture.get('permission') == 'granted' and capture.get('review_sha') == receipt.get('reviewed_sha'):
                    return {'recorded': True, 'status': 'complete', 'reconciled': True,
                            'result': {'revision': self.registry.state['revision'], 'connection': row}}
        if receipt.get('opencode_rejected') or receipt.get('capture_rejected'):
            return {'recorded': True, 'status': 'complete', 'outcome': 'rejected',
                    'result': copy.deepcopy(receipt.get('result')), 'error_code': receipt.get('error_code')}
        if receipt['status'] != 'complete':
            for adapter in self.adapters.values():
                if getattr(adapter, 'kind', None) != 'opencode':
                    continue
                try:
                    result = adapter.original_result(operation_id)
                except (PolicyError, OSError, ValueError, KeyError):
                    result = None
                if result is not None:
                    return {'recorded': True, 'status': 'complete', 'result': result, 'reconciled': True}
        result = {'recorded': True, 'status': receipt['status'],
                  'result': copy.deepcopy(receipt.get('result')),
                  'error_code': receipt.get('error_code')}
        if receipt.get('outcome'):
            result['outcome'] = receipt['outcome']
        return result

    async def perform(self, identifier, action, expected, operation_id, reviewed_sha=None):
        if not isinstance(operation_id, str) or not re.fullmatch(r'[a-zA-Z0-9_-]{1,64}', operation_id):
            raise PolicyError('invalid_operation_id')
        if action not in ('preview', 'prepare', 'activate', 'review', 'allow_capture', 'defer_capture') or identifier not in self.adapters:
            raise PolicyError('connection_not_found', 404)
        kind = getattr(self.adapters[identifier], 'kind', 'codex')
        if action in ('allow_capture', 'defer_capture') and kind != 'codex':
            raise PolicyError('capture_permission_not_applicable', 409)
        if self.environments[kind]['environment_issue']:
            raise PolicyError(self.environments[kind]['environment_issue'], 409)
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
                operations[operation_id] = {'signature': signature, 'status': 'unknown',
                    'action': action, 'instance_id': identifier, 'reviewed_sha': reviewed_sha}
                r.state['revision'] += 1
                r.save()
            try:
                if action in ('allow_capture', 'defer_capture'):
                    row = next((r for r in self.view()['connections'] if r['instance_id'] == identifier), {})
                    if row.get('status') not in ('configured_pending_new_session', 'environment_receipt_unconfirmed'):
                        raise PolicyError('capture_connection_not_configured', 409)
                    if action == 'allow_capture':
                        await self.capture.allow(adapter, reviewed_sha)
                    else:
                        inspected = await self.capture.inspect(row, adapter)
                        if inspected.get('permission') != 'required' or inspected.get('review_sha') != reviewed_sha:
                            raise PolicyError('capture_permission_state_changed', 409)
                        self.capture.record(identifier, kind)['capture_deferred'] = True
                        self.capture.cache.pop(identifier, None)
                    result = row
                    result.pop('capture', None)
                elif action == 'preview':
                    result = getattr(adapter, 'preview', adapter.view)()
                elif action == 'activate':
                    result = await adapter.activate(r.state['revision'], reviewed_sha, **({'operation_id': operation_id} if getattr(adapter, 'kind', None) == 'opencode' else {}))
                else:
                    result = await getattr(adapter, action)(r.state['revision'], **({'operation_id': operation_id} if action == 'prepare' and getattr(adapter, 'kind', None) == 'opencode' else {}))
            except PolicyError as error:
                async with r.mutex:
                    operations[operation_id]['error_code'] = error.code
                    if action in ('allow_capture', 'defer_capture') and error.code in (
                            'capture_review_changed', 'capture_config_version_unavailable',
                            'capture_connection_not_configured', 'capture_permission_state_changed',
                            'capture_hooks_unavailable', 'capture_hooks_source_changed', 'capture_hook_hash_unavailable'):
                        operations[operation_id].update(capture_rejected=True,
                            result={'revision': r.state['revision'], 'connection': row})
                    if getattr(adapter, 'kind', None) == 'opencode' and adapter.safely_rejected(operation_id, action):
                        try:
                            row = adapter.view()
                        except PolicyError:
                            row = {'instance_id': adapter.id, 'kind': 'opencode', 'status': 'requires_review'}
                        operations[operation_id].update(opencode_rejected=True,
                            result={'revision': r.state['revision'], 'connection': row})
                    r.save()
                raise
            async with r.mutex:
                records = 'codex_connections' if kind == 'codex' else 'connections'
                record = r.state.get(records, {}).get(identifier, {})
                binary_changed = False
                binary_mtime = None
                try:
                    binary = Path(adapter.executable).stat()
                    fingerprint = [binary.st_ino, binary.st_size, binary.st_mtime_ns]
                    binary_mtime = binary.st_mtime_ns / 1e9
                    binary_changed = bool(record.get('executable_fingerprint') and
                        record['executable_fingerprint'] != fingerprint)
                except (OSError, TypeError):
                    fingerprint = None
                # Explicit review checks the existing installation before acknowledging
                # a host upgrade. Require a fresh host receipt; never reinstall here.
                if action == 'activate' or (action == 'review' and binary_changed and
                        result.get('status') == 'configured_pending_new_session'):
                    if action == 'review':
                        verify_artifact(r.state['agents'][identifier]['installation'])
                    now = time.time()
                    if action == 'activate':
                        record['activated_at'] = now
                        record.pop('upgraded_at', None)
                    else:
                        previous_activated = record.get('activated_at') or 0
                        if binary_mtime is not None and previous_activated < binary_mtime <= now:
                            record['activated_at'] = binary_mtime
                        else:
                            record['activated_at'] = now
                        record['upgraded_at'] = now
                    record['environment_receipt_required'] = kind != 'opencode'
                    r.state['agents'][identifier]['host_profile'] = self.environments[kind]['profile']
                    if fingerprint is not None:
                        record['executable_fingerprint'] = fingerprint
                result = {'revision': r.state['revision'], 'connection': result}
                operations[operation_id].update(status='complete', result=copy.deepcopy(result))
                r.save()
            return result
