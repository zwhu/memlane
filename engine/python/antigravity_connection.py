"""Native Gemini App plugin installation with the existing managed registry."""
import asyncio
import copy
import json
import os
from pathlib import Path
import subprocess
import time

from agent_connections import NativeConnection, safe_file, sha, verify_artifact
from project_control import private_write
from project_policy import PolicyError

NAME = 'memlane-cognee'


class AntigravityConnection(NativeConnection):
    def __init__(self, registry, config, profile, executable):
        self.registry, self.config, self.kind = registry, config, 'antigravity'
        self.profile = Path(profile).expanduser().absolute()
        self.executable = executable
        self.id = self.kind + '-' + sha(str(self.profile).encode())[:24]
        self.root = registry.path.parent / 'connections' / self.id
        self.target = self.profile / 'config.json'

    def view(self):
        row = super().view()
        row['entrypoints'] = ['desktop']
        row['capabilities']['auto_capture']['condition'] = 'Gemini App task/CodeMode; new conversations only; ordinary chat not verified'
        if row['status'] == 'configured_pending_new_session':
            self.installed()
        return row

    def settings(self):
        raw = safe_file(self.target)
        try:
            value = json.loads(raw) if raw else {}
            if not isinstance(value, dict) or not isinstance(value.get('plugins', {}), dict):
                raise ValueError()
        except (ValueError, TypeError):
            raise PolicyError('antigravity_configuration_requires_review', 409) from None
        return raw, value

    def preview(self):
        raw, value = self.settings()
        expected = Path.home() / '.gemini/config'
        if self.profile.resolve() != expected.resolve():
            raise PolicyError('antigravity_custom_environment_not_supported', 409)
        record = self.registry.state.get('connections', {}).get(self.id, {})
        directory = self.profile / 'plugins'
        for path in directory.iterdir() if directory.exists() else []:
            if 'cognee' in path.name.lower() and not (record.get('activated') and path == Path(record.get('native_plugin_dir', ''))):
                raise PolicyError('duplicate_antigravity_memory_plugin_requires_review', 409)
        if any('cognee' in name.lower() and name != NAME and options.get('enabled', True)
               for name, options in value.get('plugins', {}).items() if isinstance(options, dict)):
            raise PolicyError('duplicate_antigravity_memory_plugin_requires_review', 409)
        return {'instance_id': self.id, 'kind': self.kind, 'target': str(self.target),
                'baseline_sha256': sha(raw), 'preserve_other_plugins': True,
                'requires_reload': True, 'requires_host_trust': True}

    async def prepare(self, expected):
        previous = copy.deepcopy(self.registry.state.get('connections', {}).get(self.id, {}))
        active = None
        active_boundary = None
        if previous.get('activated'):
            if previous.get('status') != 'configured_pending_new_session' or previous.get('upgrade_review_sha') != self.upgrade_review_sha():
                raise PolicyError('active_antigravity_upgrade_requires_review', 409)
            active = copy.deepcopy(self.registry.state['agents'][self.id]['installation'])
            active_boundary = json.loads(safe_file(Path(previous['native_plugin_dir']) / '.memlane-installation.json'))['activated_at']
        await super().prepare(expected)
        # AI profile exports remain private to the management owner. A new
        # Agent does not inherit access to this explicitly protected source.
        for project in list(self.registry.state['projects'].values()):
            if active is None and project.get('name') == 'ml-user-profiles':
                await self.registry.set_policy(self.registry.state['revision'], {'access': 'none'},
                                               project_id=project['id'], agent_id=self.id)
        async with self.registry.mutex:
            agent = self.registry.state['agents'][self.id]
            agent['host_profile'] = str(self.profile)
            agent['entrypoints'] = ['desktop']
            candidate = agent['installation']
            if active is not None:
                record = self.registry.state['connections'][self.id]
                record.update(prepared_installation=copy.deepcopy(candidate), active_installation=active,
                              active_boundary_at=active_boundary,
                              activated=True, activated_at=previous['activated_at'],
                              native_plugin_dir=previous['native_plugin_dir'],
                              enabled_plugin=previous.get('enabled_plugin', NAME),
                              upgrade_review_sha=previous['upgrade_review_sha'],
                              sessions_before_activation=previous.get('sessions_before_activation', []))
                agent['installation'] = active
            marker = Path(candidate['plugin_dir']) / '.memlane-installation.json'
            value = json.loads(marker.read_text())
            value.update(runtime_profile=str(self.profile), activated_at=0)
            private_write(marker, json.dumps(value).encode())
            self.registry.save()
        return self.view()

    def upgrade_review_sha(self):
        native = self.installed()
        installation = self.registry.state['agents'][self.id]['installation']
        return sha(json.dumps([str(native), installation['manifest_sha256'],
                               sha(safe_file(native / '.memlane-installation.json')),
                               sha(safe_file(self.target))]).encode())

    def installed(self, check_binding=True):
        record = self.registry.state['connections'][self.id]
        installation = self.registry.state['agents'][self.id]['installation']
        path = Path(record.get('native_plugin_dir', ''))
        if not path.is_absolute() or path.parent != self.profile / 'plugins':
            raise PolicyError('antigravity_installation_requires_review', 409)
        _, settings = self.settings()
        if settings.get('plugins', {}).get(path.name, {}).get('enabled') is not True:
            raise PolicyError('antigravity_plugin_not_enabled', 409)
        verify_artifact({**installation, 'plugin_dir': str(path)})
        if check_binding and safe_file(path / '.memlane-installation.json') != safe_file(Path(installation['plugin_dir']) / '.memlane-installation.json'):
            raise PolicyError('antigravity_installation_binding_changed', 409)
        return path

    async def activate(self, expected, reviewed_sha):
        r = self.registry
        async with r.mutex:
            r.check_revision(expected)
            record = r.state['connections'].get(self.id, {})
            if record.get('status') != 'prepared' or reviewed_sha != record.get('proposed_sha256'):
                raise PolicyError('reviewed_prepared_connection_required', 409)
            before, settings = self.settings()
            if sha(before) != record['preview']['baseline_sha256']:
                raise PolicyError('host_configuration_changed', 409)
            self.preview()
            installation = record.get('prepared_installation') or r.state['agents'][self.id]['installation']
            if record.get('prepared_installation') and record.get('upgrade_review_sha') != self.upgrade_review_sha():
                raise PolicyError('active_antigravity_upgrade_requires_review', 409)
            verify_artifact(installation)
            private_write(self.root / 'config-before.json', before)
            record.update(status='activation_unknown', sessions_before_activation=[
                key for key in r.state['sessions'] if json.loads(key)[0] == self.id])
            if record.get('prepared_installation'):
                r.state['agents'][self.id]['installation'] = copy.deepcopy(installation)
            r.state['revision'] += 1
            r.save()
            env = {key: value for key, value in os.environ.items() if not key.startswith(('COGNEE_', 'MEMLANE_'))}
            env['HOME'] = installation['home']
            async def native(*arguments):
                try:
                    result = await asyncio.to_thread(subprocess.run, [self.executable, 'plugin', *arguments],
                        env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=45)
                except subprocess.TimeoutExpired:
                    raise PolicyError('antigravity_installation_result_unknown', 503) from None
                if result.returncode:
                    raise PolicyError('antigravity_installation_requires_review', 409)
                return result
            await native('validate', installation['plugin_dir'], '--json')
            await native('install', installation['plugin_dir'])
            candidates = []
            for path in (self.profile / 'plugins').iterdir():
                manifest = path / 'plugin.json'
                if manifest.is_file() and not path.is_symlink():
                    if json.loads(safe_file(manifest)).get('name') == NAME:
                        candidates.append(path)
            if len(candidates) != 1:
                raise PolicyError('antigravity_installed_path_unconfirmed', 409)
            path = candidates[0]
            record['native_plugin_dir'] = str(path)
            await native('enable', path.name)
            after, actual = self.settings()
            preserved = copy.deepcopy(actual)
            preserved.get('plugins', {}).pop(path.name, None)
            baseline = copy.deepcopy(settings)
            baseline.get('plugins', {}).pop(path.name, None)
            if preserved != baseline:
                raise PolicyError('antigravity_other_configuration_changed', 409)
            activated_at = record['active_boundary_at'] if record.get('active_installation') else time.time()
            self.publish_activation_binding(path, activated_at)
            self.installed()
            record.update(status='configured_pending_new_session', activated=True, activated_at=activated_at,
                          enabled_plugin=path.name, native_config_sha256=sha(after))
            for key in ('prepared_installation', 'active_installation', 'active_boundary_at', 'upgrade_review_sha'):
                record.pop(key, None)
            r.save()
            return self.view()

    def publish_activation_binding(self, native_path, activated_at):
        installation = self.registry.state['agents'][self.id]['installation']
        source = Path(installation['plugin_dir']) / '.memlane-installation.json'
        native = Path(native_path) / '.memlane-installation.json'
        marker = json.loads(safe_file(source))
        marker['activated_at'] = activated_at
        raw = json.dumps(marker).encode()
        journal = {'source': str(source), 'native': str(native),
                   'before_source': sha(safe_file(source)), 'before_native': sha(safe_file(native)),
                   'after': marker}
        private_write(self.root / 'activation-boundary.json', json.dumps(journal).encode())
        private_write(source, raw)
        private_write(native, raw)

    def restore_activation_binding(self, native_path):
        journal_path = self.root / 'activation-boundary.json'
        if not journal_path.exists():
            return
        journal = json.loads(safe_file(journal_path))
        installation = self.registry.state['agents'][self.id]['installation']
        source = Path(installation['plugin_dir']) / '.memlane-installation.json'
        native = Path(native_path) / '.memlane-installation.json'
        raw = json.dumps(journal['after']).encode()
        if journal['source'] != str(source) or journal['native'] != str(native):
            raise PolicyError('antigravity_activation_binding_changed', 409)
        if (sha(safe_file(source)) not in (journal['before_source'], sha(raw))
                or sha(safe_file(native)) not in (journal['before_native'], sha(raw))):
            raise PolicyError('antigravity_activation_binding_changed', 409)
        private_write(source, raw)
        private_write(native, raw)

    async def review(self, expected):
        async with self.registry.mutex:
            self.registry.check_revision(expected)
            record = self.registry.state.get('connections', {}).get(self.id, {})
            if record.get('status') == 'prepared' and record.get('prepared_installation'):
                verify_artifact(record['prepared_installation'])
                self.installed()
                return self.view()
            path = self.installed(check_binding=False)
            if record.get('prepared_installation') and record.get('status') == 'activation_unknown':
                source = Path(self.registry.state['agents'][self.id]['installation']['plugin_dir']) / '.memlane-installation.json'
                journal_path = self.root / 'activation-boundary.json'
                journal = json.loads(safe_file(journal_path)) if journal_path.exists() else {}
                if journal.get('source') != str(source):
                    # A verified native copy may precede the marker journal.
                    # Publish its original boundary without reinstalling.
                    self.publish_activation_binding(path, record['active_boundary_at'])
            self.restore_activation_binding(path)
            self.installed()
            marker = json.loads(safe_file(path / '.memlane-installation.json'))
            if not marker.get('activated_at'):
                activated_at = time.time()
                self.publish_activation_binding(path, activated_at)
                record['activated_at'] = activated_at
            record.update(status='configured_pending_new_session', activated=True,
                          activated_at=json.loads(safe_file(path / '.memlane-installation.json'))['activated_at'])
            for key in ('prepared_installation', 'active_installation', 'active_boundary_at'):
                record.pop(key, None)
            record['upgrade_review_sha'] = self.upgrade_review_sha()
            self.registry.state['revision'] += 1
            self.registry.save()
        return self.view()
