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

import agent_discovery

from project_control import loopback_origin, private_write
from project_policy import PolicyError, connection_mutated, mark_connection_mutation, phased_connection_action


UPGRADE_FIELDS = ('prepared_installation', 'active_installation', 'active_record')


def sha(data):
    return hashlib.sha256(data).hexdigest()


def safe_file(path):
    if any(parent.is_symlink() for parent in (path, *path.parents)):
        raise PolicyError('host_configuration_requires_review', 409)
    if path.exists() and not path.is_file():
        raise PolicyError('host_configuration_requires_review', 409)
    return path.read_bytes() if path.exists() else b''


RECONCILABLE_PRE_MUTATION_ERROR_CODES = frozenset("""
    revision_conflict trae_ide_configuration_requires_review trae_ide_hook_changed_requires_review
    cursor_configuration_requires_review cursor_loader_changed_requires_review duplicate_cursor_memory_plugin_requires_review
    cursor_third_party_import_requires_review codex_config_requires_review codex_activation_requires_prepared_result
    codex_legacy_consumers_still_active codex_project_config_requires_review codex_preview_changed
    pi_configuration_requires_review existing_pi_extension_requires_review duplicate_pi_memory_extension_requires_review
    traex_configuration_requires_review duplicate_traex_memory_plugin_requires_review
    claude_configuration_requires_review duplicate_claude_memory_plugin_requires_review
    antigravity_configuration_requires_review antigravity_custom_environment_not_supported
    duplicate_antigravity_memory_plugin_requires_review active_antigravity_upgrade_requires_review
    opencode_configuration_requires_review existing_opencode_loader_requires_review duplicate_opencode_memory_plugin_requires_review
    reviewed_prepared_connection_required connection_preview_changed host_executable_unavailable managed_plugin_input_mismatch
    capture_review_changed capture_config_version_unavailable capture_connection_not_configured capture_permission_state_changed
    capture_hooks_unavailable capture_hooks_source_changed capture_hook_hash_unavailable
    environment_missing environment_unavailable verification_environment_not_allowed
    trae_ide_repair_preview_changed trae_ide_hook_repair_not_needed
""".split())

REPAIR_DEFAULTS = {'needs_hook_repair': False, 'repair_reason': '', 'repair_summary': ''}
UNKNOWN_CONNECTION_STATES = ('preparation_unknown', 'activation_unknown', 'staging_unknown')


def validate_operation_id(operation_id):
    if not isinstance(operation_id, str) or not re.fullmatch(r'[a-zA-Z0-9_-]{1,64}', operation_id):
        raise PolicyError('invalid_operation_id')


def persist_operation(registry, operation_id, receipt):
    registry.state.setdefault('connection_operations', {})[operation_id] = receipt
    store = getattr(registry, 'sessions_store', None)
    if store is not None:
        store.upsert_operation(operation_id, receipt)
    else:
        registry.save()


def pending_trae_repair(state, operation_id):
    for identity, record in state.get('connections', {}).items():
        journal = record.get('trae_ide_repair_journal') or {}
        if journal.get('operation_id') == operation_id:
            return identity, journal
    return None


def replay_operation(receipt, signature):
    complete = receipt['status'] == 'complete'
    phase = receipt.get('phase') or ('pre_mutation' if complete else 'post_mutation')
    if receipt['signature'] != signature:
        raise PolicyError('operation_id_conflict', 409, phase=phase)
    if not complete:
        raise PolicyError('connection_operation_requires_review', 409, phase=phase)
    if receipt.get('outcome') == 'rejected':
        raise PolicyError(receipt.get('error_code') or 'connection_operation_rejected',
                          receipt.get('error_status', 409), phase=receipt.get('phase'))
    return copy.deepcopy(receipt['result'])


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

    def last_hook_error(self):
        if self.kind != 'trae_ide':
            return None
        from trae_ide_hook import ALL_DIAGNOSTIC_EVENTS, PAYLOAD_KEYS, read_hook_errors
        from trae_ide_connection import redact_diagnostic_text

        state = self.registry.state
        agent = state.get('agents', {}).get(self.id, {})
        home = (agent.get('installation') or {}).get('home') or Path.home()
        directory = Path(home) / '.cognee-plugin' / ('trae_ide-' + self.id)
        activated_at = state.get('connections', {}).get(self.id, {}).get('activated_at')
        cutoff = activated_at if type(activated_at) in (int, float) else 0.0
        receipt = state.get('connection_receipts', {}).get(self.id, {})
        if (isinstance(receipt, dict) and receipt.get('artifact_verified') and receipt.get('environment_verified')
                and type(receipt.get('received_at')) in (int, float) and receipt['received_at'] > cutoff):
            cutoff = receipt['received_at']
        for item in reversed(read_hook_errors(directory)):
            if not isinstance(item, dict):
                continue
            at = item.get('at')
            if type(at) not in (int, float) or not 0 < at < float('inf') or at < cutoff:
                continue
            keys = item.get('payload_keys')
            result = {
                'at': float(at), 'timestamp': str(item.get('timestamp', ''))[:80],
                'event': item.get('event') if item.get('event') in tuple(ALL_DIAGNOSTIC_EVENTS) else '',
                'script': 'trae_ide_hook.py', 'status': item.get('status') if type(item.get('status')) is int else None,
                'error_code': str(item.get('error_code', ''))[:80], 'error_type': str(item.get('error_type', ''))[:80],
                'reason': str(item.get('reason', ''))[:120],
                'payload_keys': sorted(key for key in PAYLOAD_KEYS if isinstance(keys, list) and key in keys),
            }
            snippet = item.get('stderr_snippet')
            if isinstance(snippet, str) and (clean := redact_diagnostic_text(snippet)):
                result['stderr_snippet'] = clean
            return result
        return None

    def view(self):
        record = self.registry.state.get('connections', {}).get(self.id, {})
        status = record.get('status', 'not_connected' if self.component_source_available() else 'component_unavailable')
        if self.kind == 'trae_ide' and record.get('trae_ide_repair_journal') is not None:
            status = 'activation_unknown'
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
            hook_error = self.last_hook_error()
            if hook_error is not None:
                result['last_hook_error'] = hook_error
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
        verify_hook_artifact({**installation, 'host_kind': self.kind, 'instance_id': self.id}, str(self.profile))
        from cursor_connection import proposal
        raw = safe_file(self.target)
        hook = record.get('cursor_next_hook')
        if not isinstance(hook, dict) or sha(raw) != record.get('cursor_proposal_sha256'):
            raise PolicyError('cursor_loader_changed_requires_review', 409)
        proposal(raw, hook, None)

    def trae_ide_installed(self, installation, record, *, hooks=None):
        from trae_ide_connection import verify_hooks
        expected_hooks = record.get('trae_ide_next_hooks') if hooks is None else hooks
        verify_hook_artifact({**installation, 'host_kind': self.kind, 'instance_id': self.id}, str(self.profile))
        raw = safe_file(self.target)
        digest = record.get('trae_ide_proposal_sha256')
        if isinstance(digest, str):
            if sha(raw) != digest:
                raise PolicyError('trae_ide_hook_changed_requires_review', 409)
        elif hooks is None and isinstance(record.get('proposed_sha256'), str):
            if sha((installation['manifest_sha256'] + sha(raw)).encode()) != record['proposed_sha256']:
                raise PolicyError('trae_ide_hook_changed_requires_review', 409)
        verify_hooks(raw, expected_hooks, Path(installation['plugin_dir']) / 'scripts/trae_ide_hook.py')

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
        elif self.kind == 'antigravity':
            self.installed()

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

    def _require_supported_trae_hooks(self):
        if self.kind == 'trae_ide':
            from trae_hooks_migration import inspect_trae_hooks_repair

            repair = inspect_trae_hooks_repair(self.registry, self, self.config)
            if repair['blockers']:
                raise PolicyError(repair['blockers'][0]['code'], 409)
            if repair['needs_hook_repair']:
                raise PolicyError('trae_ide_hook_repair_required', 409)

    def _check_preparation_state(self, record):
        if (record.get('status') in ('activation_unknown', 'preparation_unknown')
                or self.kind == 'trae_ide' and record.get('trae_ide_repair_journal') is not None):
            raise PolicyError('connection_result_requires_review', 409)

    @phased_connection_action
    async def prepare(self, expected):
        r = self.registry
        r.check_revision(expected)
        previous = r.state.get('connections', {}).get(self.id, {})
        self._check_preparation_state(previous)
        if not self.executable or not Path(self.executable).is_file():
            raise PolicyError('host_executable_unavailable', 503)
        if not self.component_source_available():
            raise PolicyError('managed_plugin_input_mismatch', 503)
        self.preview()
        self._require_supported_trae_hooks()
        if self.id not in r.state['agents'] and not previous.get('activated'):
            mark_connection_mutation()
            directory = await r.provision_agent(expected, self.id, '本机 ' + {'pi': 'pi', 'traex': 'TRAE CLI', 'trae_ide': 'TRAE IDE', 'claude_code': 'Claude Code', 'cursor': 'Cursor', 'antigravity': 'Gemini App'}[self.kind])
            expected = directory['revision']
        async with r.mutex:
            previous = copy.deepcopy(r.state.get('connections', {}).get(self.id, {}))
            self._check_preparation_state(previous)
            r.check_revision(expected)
            agent = r.state['agents'].get(self.id)
            if agent is None:
                raise PolicyError('agent_creation_requires_review', 409)
            active_installation = active_record = None
            if previous.get('activated'):
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
                            'executable_fingerprint', 'sessions_before_activation', 'enabled_plugin',
                            'native_plugin_dir', 'upgrade_review_sha', 'native_config_sha256', 'active_boundary_at'):
                    if key in active_record:
                        record[key] = copy.deepcopy(active_record[key])
            if self.kind == 'cursor':
                record['cursor_hook'] = current.get('cursor_hook') or current.get('cursor_next_hook')
                record['cursor_active_installation'] = copy.deepcopy(
                    active_installation if active_installation is not None else current.get('cursor_active_installation'))
            if self.kind == 'trae_ide':
                record['trae_ide_hooks'] = current.get('trae_ide_hooks')
            mark_connection_mutation()
            r.state.setdefault('connections', {})[self.id] = record
            r.state['revision'] += 1
            r.save()
            from project_installation import plan_installation, prepare
            control = self.config.get('project_control', {})
            source = control.get('plugin_dir') or self.config.get('plugin_dir')
            control_url = control.get('url') or self.config['project_control_url']
            gateway_url = loopback_origin(control_url) + '/cognee'
            # Rebinding a service must not move pending buffers to a new HOME.
            active_marker_path = None
            if self.kind in ('cursor', 'trae_ide') and active_installation is not None:
                active_marker_path = Path(active_installation['plugin_dir']) / '.memlane-installation.json'
                active_marker_bytes = safe_file(active_marker_path)
            try:
                installation = None
                if self.kind == 'antigravity' and active_installation is not None:
                    planned = plan_installation(source, r.path.parent, self.id, host_kind=self.kind,
                                                python=self.config['python'])
                    if planned['plugin_dir'] == active_installation['plugin_dir']:
                        # Staging this generation again would overwrite the live marker.
                        installation = active_installation
                if installation is None:
                    installation = prepare(source, r.path.parent, self.id, agent, gateway_url, control_url,
                                           self.config.get('agent_host_home', agent.get('installation', {}).get('home') or str(Path.home())),
                                           host_kind=self.kind, python=self.config['python'])
                if self.kind in ('cursor', 'trae_ide'):
                    marker = Path(installation['plugin_dir']) / '.memlane-installation.json'
                    marker_data = json.loads(safe_file(marker))
                    marker_data['runtime_profile'] = str(self.profile)
                    if self.kind == 'trae_ide':
                        marker_data.update(hook_origin=loopback_origin(control_url),
                                           hook_token_file=agent['control_token_file'])
                    private_write(marker, json.dumps(marker_data).encode())
            except BaseException:
                if active_marker_path is not None:
                    private_write(active_marker_path, active_marker_bytes)
                raise
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
                record['trae_ide_proposal_sha256'] = sha(proposed)
                record['proposed_sha256'] = sha((installation['manifest_sha256'] + sha(proposed)).encode())
            else:
                record['proposed_sha256'] = installation['manifest_sha256']
            record['preview']['proposed_sha256'] = record['proposed_sha256']
            record['status'] = 'prepared'
            r.save()
            return self.view()

    @phased_connection_action
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
            if self.kind in ('cursor', 'trae_ide'):
                verify_hook_artifact({**installation, 'host_kind': self.kind, 'instance_id': self.id}, str(self.profile))
            else:
                verify_artifact({**installation, 'host_kind': self.kind, 'instance_id': self.id})
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
                self._require_supported_trae_hooks()
                proposed = proposal(safe_file(self.target), record['trae_ide_hooks'], record['trae_ide_next_hooks'])
                if sha((installation['manifest_sha256'] + sha(proposed)).encode()) != reviewed_sha:
                    raise PolicyError('connection_preview_changed', 409)
            mark_connection_mutation()
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

    @phased_connection_action
    async def review(self, expected):
        r = self.registry
        async with r.mutex:
            r.check_revision(expected)
            record = r.state.get('connections', {}).get(self.id, {})
            if self.kind == 'trae_ide' and record.get('trae_ide_repair_journal') is not None:
                from trae_hooks_migration import recover_trae_hooks_repair

                return recover_trae_hooks_repair(r, self)
            status = record.get('status')
            has_active_snapshot = (record.get('activated') and isinstance(record.get('active_record'), dict)
                                   and isinstance(record.get('active_installation'), dict))
            preview = record.get('preview')
            baseline_sha = preview.get('baseline_sha256') if isinstance(preview, dict) else None
            if status == 'preparation_unknown' or (status == 'prepared' and has_active_snapshot):
                baseline = safe_file(self.target)
                if not isinstance(baseline_sha, str) or not baseline_sha or sha(baseline) != baseline_sha:
                    raise PolicyError('host_configuration_changed', 409)
                if status == 'preparation_unknown':
                    self._verify_staged_artifacts()
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
                else:
                    self.preview()
                    installation = restored_record = None
                error = 'preparation_cancelled' if status == 'prepared' else 'preparation_did_not_install'
                return self._finish_review(agent, restored_record, installation, 'prepare', error)
            if status == 'activation_unknown':
                agent = r.state['agents'][self.id]
                installation = agent.get('installation')
                try:
                    pi_unwritten = self.kind == 'pi' and sha(safe_file(self.target)) != record.get('proposed_sha256')
                    if pi_unwritten and not has_active_snapshot:
                        return self.view()
                    if pi_unwritten:
                        raise PolicyError('existing_pi_extension_requires_review', 409)
                    if not isinstance(installation, dict):
                        raise PolicyError('connection_artifact_changed', 409)
                    if self.kind == 'trae_ide':
                        self.trae_ide_installed(installation, record)
                    elif self.kind == 'cursor':
                        self.cursor_installed(installation, record)
                    else:
                        bound = {**installation, 'host_kind': self.kind, 'instance_id': self.id}
                        verify_artifact(bound)
                        if self.kind == 'claude_code':
                            self.claude_installed(bound)
                        elif self.kind == 'traex':
                            self.traex_installed(bound)
                except PolicyError:
                    if (not has_active_snapshot or not isinstance(baseline_sha, str) or not baseline_sha
                            or sha(safe_file(self.target)) != baseline_sha):
                        raise
                    active_installation, active_record = record['active_installation'], record['active_record']
                    self._verify_active_installation(active_installation, active_record)
                    if self._candidate_host_install_started(installation, active_installation):
                        raise
                    return self._finish_review(agent, active_record, active_installation, 'activate',
                                               'activation_did_not_install')

                recovered = dict(record, status='configured_pending_new_session', activated=True,
                                 activated_at=time.time(), environment_receipt_required=True)
                if self.kind == 'trae_ide':
                    recovered['trae_ide_hooks'] = record['trae_ide_next_hooks']
                elif self.kind == 'cursor':
                    recovered['cursor_hook'] = record['cursor_next_hook']
                    recovered['cursor_active_installation'] = installation
                for key in UPGRADE_FIELDS:
                    recovered.pop(key, None)
                recovered.pop('upgraded_at', None)
                return self._finish_review(agent, recovered, installation, 'activate')
            # Review only the original cache and enabled binding, never install.
            return self.view()

    def _candidate_host_install_started(self, installation, active_installation):
        if self.kind not in ('traex', 'claude_code'):
            return False
        marketplace = installation.get('marketplace_name') if isinstance(installation, dict) else None
        if not isinstance(marketplace, str) or not re.fullmatch(r'[a-zA-Z0-9_-]+', marketplace):
            return True
        if marketplace == active_installation.get('marketplace_name'):
            return False
        # Native managers can publish cache or marketplace state before
        # enabling the plugin in the main host configuration.
        cache = self.profile / 'plugins/cache' / marketplace
        if cache.exists() or cache.is_symlink():
            return True
        if self.kind == 'traex':
            receipt = self.profile / '.tmp/marketplaces' / marketplace
            return receipt.exists() or receipt.is_symlink()
        plugins = self.profile / 'plugins'
        try:
            known = json.loads(safe_file(plugins / 'known_marketplaces.json') or b'{}')
            installed = json.loads(safe_file(plugins / 'installed_plugins.json'))
            if not isinstance(known, dict) or not isinstance(installed['plugins'], dict):
                raise ValueError()
            return marketplace in known or 'cognee@' + marketplace in installed['plugins']
        except (ValueError, KeyError, TypeError):
            raise PolicyError('claude_installation_requires_review', 409) from None

    def _finish_review(self, agent, record, installation, action, error=None):
        r = self.registry
        connections = r.state['connections']
        previous_record = connections[self.id]
        previous_agent = agent.copy() if agent is not None else None
        revision = r.state['revision']
        pending = [(operation, operation.copy()) for operation in r.state.get('connection_operations', {}).values()
                   if operation.get('instance_id') == self.id and operation.get('action') == action
                   and operation.get('status') != 'complete']
        mark_connection_mutation()
        try:
            if record is None:
                del connections[self.id]
            else:
                connections[self.id] = copy.deepcopy(record)
            if agent is not None:
                if installation is None:
                    agent.pop('installation', None)
                else:
                    agent['installation'] = copy.deepcopy(installation)
            r.state['revision'] = revision + 1
            row = self.view()
            if record is not None and row['status'] != record['status']:
                raise PolicyError('host_configuration_changed', 409)
            result = {'revision': r.state['revision'], 'connection': row}
            for operation, _ in pending:
                operation.update(status='complete', result=copy.deepcopy(result))
                if error:
                    operation.update(outcome='rejected', error_code=f'{self.kind}_{error}')
                else:
                    operation.pop('outcome', None)
                    operation.pop('error_code', None)
            r.save()
        except BaseException:
            connections[self.id] = previous_record
            if agent is not None:
                agent.clear()
                agent.update(previous_agent)
            r.state['revision'] = revision
            for operation, previous in pending:
                operation.clear()
                operation.update(previous)
            raise
        return row

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
                raw = safe_file(plugin_dir / '.memlane-project-access.json')
                manifest = json.loads(raw)
                if not isinstance(manifest, dict) or manifest.get('host_kind', self.kind) != self.kind:
                    raise ValueError()
                verify_manifest_files(plugin_dir, raw)
                marker_path = plugin_dir / '.memlane-installation.json'
                marker = json.loads(safe_file(marker_path))
                if (not isinstance(marker, dict) or marker_path.stat().st_mode & 0o077
                        or marker.get('instance_id') != self.id or marker.get('host_kind') != self.kind):
                    raise ValueError()
                if self.kind in ('pi', 'traex', 'trae_ide', 'claude_code'):
                    subdir = '.claude-plugin' if self.kind == 'claude_code' else '.agents/plugins'
                    if not isinstance(json.loads(safe_file(generation / subdir / 'marketplace.json')), dict):
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
        return marker
    except (OSError, ValueError, KeyError, TypeError):
        raise PolicyError('connection_artifact_changed', 409) from None


def verify_hook_artifact(installation, profile):
    marker = verify_artifact(installation)
    if (not profile or marker.get('runtime_profile') != profile
            or (installation['host_kind'] == 'trae_ide'
                and not (marker.get('hook_origin') and marker.get('hook_token_file')))):
        raise PolicyError('connection_binding_changed', 409)


def verify_manifest_files(root, raw):
    files = json.loads(raw)['files']
    if not isinstance(files, dict) or not files:
        raise PolicyError('connection_artifact_changed', 409)
    for name, digest in files.items():
        path = root / name
        if Path(name).is_absolute() or '..' in Path(name).parts or not path.is_file() or sha(safe_file(path)) != digest:
            raise PolicyError('connection_artifact_changed', 409)


def detect_executable(kind, settings, legacy=None):
    return agent_discovery.detect_executable(kind, settings, legacy, home=Path.home(), which=shutil.which)


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
            host_config = {**config, 'auth_home': codex['profile'],
                           'codex_executable': executable}
            adapter = CodexConnection(registry, host_config)
            adapters[adapter.id] = adapter
        else:
            missing_codex = {'instance_id': 'codex-unavailable', 'kind': 'codex', 'detected': False,
                             'status': 'not_connected'}
        for kind in ('pi', 'traex', 'trae_ide', 'claude_code', 'cursor'):
            settings = configured[kind]
            executable = detect_executable(kind, settings, config.get(kind + '_executable'))
            adapter = NativeConnection(registry, config, kind, settings['profile'], executable)
            adapters[adapter.id] = adapter

        from opencode_connection import OpenCodeConnection
        settings = configured['opencode']
        adapter = OpenCodeConnection(registry, config, settings['profile'],
                                     detect_executable('opencode', settings, config.get('opencode_executable')))
        adapters[adapter.id] = adapter

        from antigravity_connection import AntigravityConnection
        settings = configured['antigravity']
        adapter = AntigravityConnection(registry, config, settings['profile'],
                                        detect_executable('antigravity', settings, config.get('antigravity_executable')))
        adapters[adapter.id] = adapter
        return environments, adapters, missing_codex

    @staticmethod
    def _connection_rows(registry, environments, adapters, missing_codex):
        known_codex = any(getattr(a, 'kind', 'codex') == 'codex' for a in adapters.values())
        from trae_hooks_migration import inspect_trae_hooks_repair

        rows = [{**missing_codex, **REPAIR_DEFAULTS}] if missing_codex and not known_codex else []
        for adapter in adapters.values():
            try:
                row = adapter.view()
                row.setdefault('kind', 'codex')
                if row['kind'] == 'codex' and missing_codex:
                    row['detected'] = False
            except PolicyError as error:
                row = {'instance_id': adapter.id, 'kind': getattr(adapter, 'kind', 'codex'),
                       'status': 'requires_review', 'error_code': error.code}
            row.update(REPAIR_DEFAULTS)
            repair_pending = row['kind'] == 'trae_ide' and row['status'] == 'activation_unknown'
            if row['kind'] == 'trae_ide':
                repair = inspect_trae_hooks_repair(registry, adapter, adapter.config)
                row.update({key: repair[key] for key in REPAIR_DEFAULTS})
            details = environments[row['kind']]
            row.update(details)
            if details['environment_issue'] and not repair_pending:
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
                if row['executable_changed'] and not repair_pending:
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

    def _pending_trae_repair(self, operation_id, receipt):
        record = self.registry.state.get('connections', {}).get(receipt.get('instance_id'), {})
        journal = record.get('trae_ide_repair_journal') or {}
        return receipt.get('action') == 'repair-trae-hooks' and journal.get('operation_id') == operation_id

    def operation(self, operation_id):
        receipt = self.registry.state.get('connection_operations', {}).get(operation_id)
        if receipt is None:
            if pending := pending_trae_repair(self.registry.state, operation_id):
                return self._operation_receipt(operation_id, {
                    'status': 'unknown', 'phase': 'post_mutation',
                    'instance_id': pending[0], 'action': 'repair-trae-hooks'})
            return {'recorded': False}
        if self._pending_trae_repair(operation_id, receipt):
            return self._operation_receipt(operation_id, {
                **receipt, 'status': 'unknown', 'phase': 'post_mutation', 'outcome': None, 'result': None})
        scope = {'recorded': True, 'operation_id': operation_id, 'instance_id': receipt.get('instance_id') or '',
                 'action': receipt.get('action') or ''}
        may_recover = receipt.get('phase') != 'post_mutation'
        if receipt.get('action') == 'allow_capture' and receipt.get('status') != 'complete':
            identifier = receipt.get('instance_id')
            adapter = self.adapters.get(identifier)
            if adapter is not None:
                row = next((r for r in self.view()['connections'] if r['instance_id'] == identifier), {})
                capture = row.get('capture', {})
                if capture.get('permission') == 'granted' and capture.get('review_sha') == receipt.get('reviewed_sha'):
                    return {**scope, 'status': 'complete', 'reconciled': True,
                            'result': {'revision': self.registry.state['revision'], 'connection': row}}
        if may_recover and (receipt.get('opencode_rejected') or receipt.get('capture_rejected')):
            return {**scope, 'status': 'complete', 'outcome': 'rejected',
                    'result': copy.deepcopy(receipt.get('result')), 'error_code': receipt.get('error_code')}
        if receipt.get('status') != 'complete':
            for adapter in self.adapters.values():
                if getattr(adapter, 'kind', None) != 'opencode':
                    continue
                try:
                    result = adapter.original_result(operation_id)
                except (PolicyError, OSError, ValueError, KeyError):
                    result = None
                if result is not None:
                    return {**scope, 'status': 'complete', 'result': result, 'reconciled': True}
        return self._operation_receipt(operation_id, receipt)

    @staticmethod
    def _operation_receipt(operation_id, receipt):
        result = {'recorded': True, 'operation_id': operation_id, 'instance_id': receipt.get('instance_id') or '',
                  'action': receipt.get('action') or '', 'status': receipt['status'],
                  'result': copy.deepcopy(receipt.get('result')),
                  'error_code': receipt.get('error_code')}
        for key in ('outcome', 'phase', 'reconciled'):
            if key in receipt:
                result[key] = receipt[key]
        return result

    async def perform(self, identifier, action, expected, operation_id, reviewed_sha=None):
        validate_operation_id(operation_id)
        if action not in ('preview', 'prepare', 'activate', 'review', 'allow_capture', 'defer_capture') or identifier not in self.adapters:
            raise PolicyError('connection_not_found', 404)
        return await self._perform(identifier, action, expected, operation_id, reviewed_sha)

    @phased_connection_action
    async def _perform(self, identifier, action, expected, operation_id, reviewed_sha):
        kind = getattr(self.adapters[identifier], 'kind', 'codex')
        r, adapter = self.registry, self.adapters[identifier]
        signature = sha(json.dumps([identifier, action, reviewed_sha]).encode())
        async with self.mutex:
            operations = r.state.setdefault('connection_operations', {})
            if pending_trae_repair(r.state, operation_id):
                raise PolicyError('operation_id_conflict', 409, phase='post_mutation')
            if operation_id in operations:
                return replay_operation(operations[operation_id], signature)
            receipt = {'signature': signature, 'status': 'unknown',
                       'action': action, 'instance_id': identifier, 'reviewed_sha': reviewed_sha}
            async with r.mutex:
                try:
                    r.check_revision(expected)
                except PolicyError as error:
                    receipt.update(status='complete', outcome='rejected', phase='pre_mutation',
                                   error_code=error.code, error_status=error.status, result={'revision': r.state['revision']})
                    persist_operation(r, operation_id, receipt)
                    raise
                persist_operation(r, operation_id, receipt)
                record = r.state.get('connections', {}).get(identifier, {})
                recovering_native = (
                    action == 'review' and isinstance(adapter, NativeConnection) and kind != 'antigravity'
                    and (record.get('status') in ('prepared', 'preparation_unknown', 'activation_unknown')
                         or kind == 'trae_ide' and record.get('trae_ide_repair_journal') is not None)
                )
            try:
                if self.environments[kind]['environment_issue']:
                    raise PolicyError(self.environments[kind]['environment_issue'], 409)
                if action in ('allow_capture', 'defer_capture') and kind != 'codex':
                    raise PolicyError('capture_permission_not_applicable', 409)
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
                        mark_connection_mutation()
                        self.capture.record(identifier, kind)['capture_deferred'] = True
                        self.capture.cache.pop(identifier, None)
                    result = row
                    result.pop('capture', None)
                elif action == 'preview':
                    result = adapter.view() if kind == 'codex' else getattr(adapter, 'preview', adapter.view)()
                elif action == 'activate':
                    result = await adapter.activate(expected, reviewed_sha, **({'operation_id': operation_id} if getattr(adapter, 'kind', None) == 'opencode' else {}))
                else:
                    result = await getattr(adapter, action)(expected, **({'operation_id': operation_id} if action == 'prepare' and getattr(adapter, 'kind', None) == 'opencode' else {}))
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
                    # Native journal recovery has already restored or set its activation boundary.
                    if action == 'activate' or (action == 'review' and not recovering_native and binary_changed and
                            result.get('status') == 'configured_pending_new_session'):
                        if action == 'review':
                            verify_artifact(r.state['agents'][identifier]['installation'])
                        mark_connection_mutation()
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
            except PolicyError as error:
                async with r.mutex:
                    pre_mutation = not connection_mutated() and error.phase != 'post_mutation'
                    if kind == 'opencode' and action in ('prepare', 'activate'):
                        pre_mutation = pre_mutation and adapter.safely_rejected(operation_id, action)
                    error.phase = 'pre_mutation' if pre_mutation else 'post_mutation'
                    receipt.update(phase=error.phase, error_code=error.code, error_status=error.status)
                    if pre_mutation:
                        receipt.update(status='complete', outcome='rejected',
                                       result={'revision': r.state['revision'], 'connection': self._safe_row(adapter)})
                    else:
                        receipt.update(status='unknown')
                        receipt.pop('outcome', None)
                        receipt.pop('result', None)
                    persist_operation(r, operation_id, receipt)
                raise

    @staticmethod
    def _safe_row(adapter):
        try:
            return adapter.view()
        except (PolicyError, OSError, KeyError, ValueError, TypeError, AttributeError):
            return {'instance_id': adapter.id, 'kind': getattr(adapter, 'kind', 'codex'), 'status': 'requires_review'}

    def _snapshot_adapter(self, identifier):
        if identifier not in self.adapters:
            raise PolicyError('connection_not_found', 404)
        adapter = copy.copy(self.adapters[identifier])
        adapter.registry = _StateSnapshotRegistry(self.registry)
        return adapter

    async def preview_readonly(self, identifier):
        adapter = self._snapshot_adapter(identifier)
        kind = getattr(adapter, 'kind', 'codex')
        issue = self.environments[kind].get('environment_issue')
        return await asyncio.to_thread(self._preview_snapshot, adapter, issue)

    def _preview_snapshot(self, adapter, issue):
        from trae_hooks_migration import inspect_trae_hooks_repair

        state = adapter.registry.state
        kind = getattr(adapter, 'kind', 'codex')
        record = state.get('codex_connections' if kind == 'codex' else 'connections', {}).get(adapter.id, {})
        status = record.get('status', 'not_connected')
        if kind == 'trae_ide' and record.get('trae_ide_repair_journal') is not None:
            status = 'activation_unknown'
        result = dict(revision=state['revision'], instance_id=adapter.id, kind=kind,
                      status=status, target=str(adapter.target),
                      baseline_sha256='', proposed_sha256='', reviewed_sha='', preserve_other_plugins=True,
                      requires_reload=True, requires_host_trust=True, diff_summary='Host configuration review', blockers=[],
                      backup_plan='', summary='Review host configuration', blocker_details=[],
                      connection={'kind': kind, **self._safe_row(adapter)}, **REPAIR_DEFAULTS)
        blockers = result['blocker_details']
        if issue:
            blockers.append({'code': issue, 'status': 409, 'phase': 'pre_mutation'})
        if kind == 'trae_ide':
            repair = inspect_trae_hooks_repair(adapter.registry, adapter, self.config)
            if repair['needs_hook_repair']:
                keys = (*REPAIR_DEFAULTS, 'baseline_sha256', 'proposed_sha256', 'reviewed_sha')
                result.update({key: repair[key] for key in keys})
                result.update(backup_plan=repair['backup_plan'], summary=repair['repair_summary'],
                              diff_summary=repair['repair_summary'])
                result['diff_details'] = {key: repair[key] for key in (
                    'mode', 'removed_events', 'retained_events', 'updated_events',
                    'preserved_third_party_groups', 'backup_plan', 'roll_forward_artifact')}
                blockers.extend(repair['blockers'])
                result['preview'] = {key: repair[key] for key in keys}
                result['blockers'] = list(dict.fromkeys(blocker['code'] for blocker in blockers))
                result['plan_digest'] = result['proposed_sha256'] or result['baseline_sha256']
                result['ok'] = repair['repairable'] and not blockers
                return result
            blockers.extend(repair['blockers'])
        prepared = record.get('preview') or {}
        if prepared:
            result['preview'] = copy.deepcopy(prepared)
            result.update({key: prepared[key] for key in ('disabled_plugins', 'enabled_plugin') if key in prepared})
        try:
            baseline = safe_file(adapter.target)
            result['baseline_sha256'] = sha(baseline)
            preview = adapter.preview()
            result.setdefault('preview', preview)
            result.update({key: preview[key] for key in (
                'baseline_sha256', 'preserve_other_plugins', 'requires_reload', 'requires_host_trust') if key in preview})
            if record.get('status') != 'prepared':
                self._plan_preview_proposal(adapter, kind, baseline, record, result)
        except (PolicyError, OSError, KeyError, ValueError, TypeError, AttributeError) as error:
            blocker = {'code': getattr(error, 'code', 'host_configuration_requires_review'),
                       'status': getattr(error, 'status', 409), 'phase': 'pre_mutation'}
            if blocker not in blockers:
                blockers.append(blocker)
        if record.get('status') == 'prepared':
            result['diff_summary'] = 'Apply the prepared host configuration'
            result['proposed_sha256'] = result['reviewed_sha'] = record.get('proposed_sha256') or prepared.get('proposed_sha256', '')
            if (result['baseline_sha256'] != prepared.get('baseline_sha256')
                    or kind == 'opencode' and not blockers and preview.get('configuration_sha256') != prepared.get('configuration_sha256')):
                blockers.append({'code': 'host_configuration_changed', 'status': 409, 'phase': 'pre_mutation'})
        result['plan_digest'] = result['proposed_sha256'] or result['baseline_sha256']
        result['blockers'] = list(dict.fromkeys(blocker['code'] for blocker in blockers))
        result['ok'] = not blockers
        return result

    def _plan_preview_proposal(self, adapter, kind, baseline, record, result):
        from project_installation import plan_installation

        source = self.config.get('project_control', {}).get('plugin_dir') or self.config.get('plugin_dir')
        python = self.config.get('python')
        state = adapter.registry.state
        status = record.get('status')
        agent = state.get('agents', {}).get(adapter.id)
        if kind == 'codex':
            if status in ('activation_unknown', 'staging_unknown'):
                raise PolicyError('codex_connection_result_requires_review', 409)
        elif kind == 'opencode':
            if status in ('activation_unknown', 'preparation_unknown'):
                raise PolicyError('connection_result_requires_review', 409)
        else:
            adapter._check_preparation_state(record)
        if agent is None and adapter.id in state.get('enrollments', {}):
            raise PolicyError('agent_creation_requires_review', 409)
        executable = adapter.executable
        if (not executable or not Path(executable).is_file()
                or kind == 'opencode' and not os.access(executable, os.X_OK)):
            code = 'codex_executable_unavailable' if kind == 'codex' else 'host_executable_unavailable'
            raise PolicyError(code, 503)
        if kind == 'opencode':
            if not adapter.artifact or not adapter.artifact_sha:
                raise PolicyError('opencode_component_required', 503)
            return
        if kind == 'codex':
            marker = Path(source) / '.memlane-project-access.json' if source else None
            if marker is None or marker.parent.is_symlink() or not marker.is_file() or marker.is_symlink():
                raise PolicyError('managed_plugin_input_mismatch', 503)
            default_home = str(Path.home())
        else:
            if not adapter.component_source_available():
                raise PolicyError('managed_plugin_input_mismatch', 503)
            if not python or not Path(python).is_absolute() or not Path(python).is_file():
                raise PolicyError('host_helper_python_required')
            if kind == 'antigravity' and record.get('activated'):
                if status != 'configured_pending_new_session' or record.get('upgrade_review_sha') != adapter.upgrade_review_sha():
                    raise PolicyError('active_antigravity_upgrade_requires_review', 409)
            default_home = (agent or {}).get('installation', {}).get('home') or str(Path.home())
        home = self.config.get('agent_host_home', default_home)
        if not home or not Path(home).is_absolute() or not Path(home).is_dir():
            raise PolicyError('absolute_host_home_required')
        planned = plan_installation(source, adapter.registry.path.parent, adapter.id, host_kind=kind, python=python)
        digest = planned['manifest_sha256']
        if kind == 'codex':
            from project_codex_connection import proposal

            proposed, disabled = proposal(baseline, planned)
            digest = sha(proposed)
            fields = {'disabled_plugins': disabled, 'enabled_plugin': 'cognee@' + planned['marketplace_name']}
            result.update(fields)
            result['preview'].update(fields)
        elif kind == 'pi':
            digest = sha(pi_stub(planned))
        elif kind in ('trae_ide', 'cursor'):
            if kind == 'trae_ide':
                from trae_ide_connection import loader, proposal

                previous = record.get('trae_ide_hooks') or record.get('trae_ide_next_hooks')
            else:
                from cursor_connection import loader, proposal

                previous = record.get('cursor_hook') or record.get('cursor_next_hook')
            proposed = proposal(baseline, previous, loader(planned, python))
            digest = sha((digest + sha(proposed)).encode())
        result['proposed_sha256'] = result['reviewed_sha'] = result['preview']['proposed_sha256'] = digest

    async def repair_trae_hooks(self, identifier, *, dry_run, expected_revision=None, operation_id=None, reviewed_sha=None):
        from trae_hooks_migration import apply_trae_hooks_repair, inspect_trae_hooks_repair

        adapter = self._snapshot_adapter(identifier)
        if getattr(adapter, 'kind', None) != 'trae_ide':
            raise PolicyError('connection_not_found', 404)
        if dry_run:
            return await asyncio.to_thread(inspect_trae_hooks_repair, adapter.registry, adapter, self.config)
        async with self.mutex, self.registry.mutex:
            return apply_trae_hooks_repair(self.registry, self.adapters[identifier], self.config,
                                           expected_revision=expected_revision, operation_id=operation_id,
                                           reviewed_sha=reviewed_sha)

    @staticmethod
    def _not_connected_host_untouched(adapter):
        kind = getattr(adapter, 'kind', 'codex')
        state = adapter.registry.state
        record = state.get('codex_connections' if kind == 'codex' else 'connections', {}).get(adapter.id, {})
        if record.get('activated'):
            return False
        prefix = 'memlane-' + sha(adapter.id.encode())[:12] + '-'
        fragment = f'managed-marketplaces/{adapter.id}/'
        raw_text = safe_file(adapter.target).decode('utf-8', errors='replace')
        if fragment in raw_text or prefix in raw_text:
            return False
        if kind in ('trae_ide', 'cursor'):
            try:
                parsed = json.loads(raw_text)
            except (ValueError, RecursionError):
                return True
            hooks = parsed.get('hooks') if isinstance(parsed, dict) else None
            if kind == 'trae_ide' and isinstance(hooks, dict):
                from trae_ide_connection import hook_commands, is_memlane_hook_group

                if any(is_memlane_hook_group(group) and any(fragment in args[2] for args in hook_commands(group))
                       for groups in hooks.values() if isinstance(groups, list) for group in groups):
                    return False
            if kind == 'cursor' and isinstance(hooks, dict):
                known_hook = record.get('cursor_hook') or record.get('cursor_next_hook')
                return not any(group == known_hook or fragment in str(group) or prefix in str(group)
                               for groups in hooks.values() if isinstance(groups, list)
                               for group in groups if isinstance(group, dict))
            return True
        if kind == 'pi':
            return True
        if kind == 'opencode' and f'opencode-installations/{adapter.id}/' in raw_text:
            return False
        profile = adapter.home if kind == 'codex' else adapter.profile
        cache = profile / 'plugins/cache'
        if cache.parent.is_symlink() or cache.is_symlink() or (cache.exists() and not cache.is_dir()):
            return False
        if cache.is_dir() and any(path.name.startswith(prefix) and (path.is_dir() or path.is_symlink())
                                  for path in cache.iterdir()):
            return False
        if kind == 'traex':
            try:
                return 'memlane-cognee@local' not in tomlkit.parse(raw_text).get('plugins', {})
            except ValueError:
                return 'memlane-cognee@local' not in raw_text
        if kind == 'antigravity':
            plugins = profile / 'plugins'
            return ('memlane-cognee' not in raw_text and not plugins.is_symlink()
                    and not (plugins.exists() and any(path.name.startswith('memlane-cognee') for path in plugins.iterdir())))
        return True

    def _reconciliation_host_untouched(self, adapter, *, action, error_code):
        from project_codex_connection import read_config, verify_cache

        kind = getattr(adapter, 'kind', 'codex')
        state = adapter.registry.state
        record = state.get('codex_connections' if kind == 'codex' else 'connections', {}).get(adapter.id, {})
        status = record.get('status', 'not_connected')
        if status in (*UNKNOWN_CONNECTION_STATES, 'agent_creation_unconfirmed') or record.get('trae_ide_repair_journal') or (
                adapter.id in state.get('enrollments', {}) and adapter.id not in state.get('agents', {})):
            return False
        try:
            if status == 'not_connected':
                return self._not_connected_host_untouched(adapter)
            if status in ('prepared', 'preview'):
                preview = record.get('preview') or {}
                baseline = preview.get('baseline_sha256')
                if status == 'preview' and baseline is None:
                    return self._not_connected_host_untouched(adapter)
                configuration = preview.get('configuration_sha256')
                return (sha(safe_file(adapter.target)) == baseline
                        and (kind != 'opencode' or configuration is None
                             or adapter.configuration_sha256() == configuration))
            if status != 'configured_pending_new_session':
                return False
            installation = state['agents'][adapter.id]['installation']
            if kind == 'codex':
                if sha(read_config(adapter.home)[0]) != record['preview']['proposed_sha256']:
                    return False
                path = adapter.home / 'plugins/cache' / installation['marketplace_name'] / 'cognee' / Path(record['staged_cache']).name
                verify_cache(path, installation)
            elif kind == 'opencode':
                adapter.installed(record, force=True)
            elif kind == 'antigravity':
                adapter.installed()
            else:
                adapter._verify_active_installation(installation, record)
            return True
        except (PolicyError, OSError, KeyError, ValueError, TypeError, AttributeError):
            return False

    def _reconcile_receipt(self, operation_id, receipt, *, reconcilable=False, reason='', reconciled=False):
        return dict(recorded=True, operation_id=operation_id, instance_id=receipt.get('instance_id') or '',
                    action=receipt.get('action') or '',
                    status=receipt['status'], outcome=receipt.get('outcome') or '',
                    phase=receipt.get('phase') or '', error_code=receipt.get('error_code') or '',
                    reconciled=reconciled, safe_to_unlock=receipt['status'] == 'complete', reason=reason,
                    result=copy.deepcopy(receipt.get('result')), revision=self.registry.state['revision'],
                    reconcilable=reconcilable, operation=self.operation(operation_id))

    async def reconcile_operation(self, operation_id, expected_revision=None, *, dry_run=False,
                                  instance_id=None, action=None):
        validate_operation_id(operation_id)
        r = self.registry
        async with self.mutex, r.mutex:
            operations = r.state.get('connection_operations', {})
            receipt = operations.get(operation_id)
            if receipt is None and (pending := pending_trae_repair(r.state, operation_id)):
                receipt = dict(status='unknown', phase='post_mutation', instance_id=pending[0],
                               action='repair-trae-hooks')
            unrecorded = receipt is None
            if unrecorded:
                if instance_id is None:
                    raise PolicyError('connection_operation_not_found', 404)
                if (not isinstance(instance_id, str) or instance_id not in self.adapters
                        or action not in ('prepare', 'activate', 'review', 'preview', 'allow_capture',
                                          'defer_capture', 'repair-trae-hooks')
                        or action == 'repair-trae-hooks' and getattr(self.adapters[instance_id], 'kind', None) != 'trae_ide'):
                    raise PolicyError('connection_not_found', 404)
                receipt = dict(status='unrecorded', action=action, instance_id=instance_id,
                               error_code='revision_conflict',
                               signature=sha(json.dumps([instance_id, action, None]).encode()))
            revision = r.state['revision']
            if self._pending_trae_repair(operation_id, receipt):
                receipt = {**receipt, 'status': 'unknown', 'phase': 'post_mutation', 'outcome': None, 'result': None}
            if not unrecorded:
                conflict_phase = receipt.get('phase') or (
                    'pre_mutation' if receipt.get('status') == 'complete' else 'post_mutation')
                for key, value in (('instance_id', instance_id), ('action', action)):
                    if value is not None and value != '' and (not isinstance(value, str) or value != receipt.get(key)):
                        raise PolicyError('operation_id_conflict', 409, phase=conflict_phase)
            if not dry_run:
                if expected_revision is None:
                    raise PolicyError('invalid_request_fields')
                r.check_revision(expected_revision)
            if receipt['status'] == 'complete':
                return {**self._reconcile_receipt(operation_id, receipt), 'already_complete': True}
            identifier = receipt.get('instance_id')
            action = receipt.get('action')
            phase = receipt.get('phase')
            code = receipt.get('error_code')
            reconcilable = (receipt['status'] in ('unknown', 'unrecorded') and phase != 'post_mutation'
                            and (phase == 'pre_mutation' or code in RECONCILABLE_PRE_MUTATION_ERROR_CODES)
                            and identifier in self.adapters)
            if reconcilable:
                snapshot = self._snapshot_adapter(identifier)
                state = snapshot.registry.state
                kind = getattr(snapshot, 'kind', 'codex')
                record = state.get('codex_connections' if kind == 'codex' else 'connections', {}).get(identifier, {})
                activated = record.get('activated') or (
                    kind == 'codex' and record.get('status') == 'configured_pending_new_session')
                provisioned = identifier in state.get('agents', {})
                prepare_conflict = action == 'prepare' and code == 'revision_conflict' and provisioned
                ambiguous_prepare = prepare_conflict and (not activated if unrecorded else phase != 'pre_mutation')
                reconcilable = (not ambiguous_prepare and not (unrecorded and action == 'activate' and activated)
                                and await asyncio.to_thread(self._reconciliation_host_untouched, snapshot,
                                                            action=action, error_code=code))
            reason = '' if reconcilable else 'connection_operation_ambiguous_requires_review'
            if dry_run:
                if unrecorded:
                    return dict(recorded=False, operation_id=operation_id, revision=revision, instance_id=identifier,
                                action=action, status='unrecorded', outcome='', phase='',
                                error_code=code if reconcilable else '', reconciled=False, reconcilable=reconcilable,
                                safe_to_unlock=False, reason=reason, result=None)
                return self._reconcile_receipt(operation_id, receipt, reconcilable=reconcilable, reason=reason)
            if not reconcilable:
                raise PolicyError(reason, 409, phase='post_mutation')
            row = self._safe_row(self.adapters[identifier])
            receipt.update(status='complete', outcome='rejected', phase='pre_mutation', reconciled=True,
                           result={'revision': revision + 1, 'connection': row})
            if unrecorded:
                r.state.setdefault('connection_operations', {})[operation_id] = receipt
            r.state['revision'] += 1
            r.save()
            return self._reconcile_receipt(operation_id, receipt, reconcilable=True, reconciled=True)
