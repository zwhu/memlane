import asyncio
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
import uuid

from aiohttp.test_utils import TestServer
from project_control import Registry, private_write
from project_control_server import create_app
from test_project_control import OfficialFixture
from test_opencode_plugin_patch import official_sources
from opencode_plugin_patch import managed_http_client, managed_outbox, managed_capture_callbacks, managed_config_recall, managed_action_callbacks

ROOT = Path(__file__).resolve().parents[2]


class OpenCodeControlTests(unittest.IsolatedAsyncioTestCase):
    async def test_compiled_client_against_real_project_control_api(self):
        modules = os.environ.get('MEMLANE_OPENCODE_TEST_NODE_MODULES')
        if not modules:
            self.skipTest('set MEMLANE_OPENCODE_TEST_NODE_MODULES to fixed npm-ci dependencies')
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            for name in ('repo-a', 'repo-b'):
                (root / name).mkdir()
                subprocess.run(['git', 'init', '-q', str(root / name)], check=True)
            private_write(root / 'agent-token', b'a' * 40)
            private_write(root / 'manager-token', b'm' * 40)
            (root / 'package.json').write_text('{"type":"module"}')
            source = root / 'managed-control.ts'
            source.write_text((ROOT / 'engine/agents/opencode/managed-control.ts').read_text())
            official_source = official_sources()
            (root / 'runtime.ts').write_text(managed_outbox(official_source['runtime.ts']))
            (root / 'client.ts').write_text(managed_http_client(official_source['client.ts']))
            (root / 'types.ts').write_text(official_source['types.ts'])
            (root / 'node_modules').symlink_to(Path(modules).resolve(), target_is_directory=True)
            (root / 'plugin.ts').write_text(managed_action_callbacks(managed_config_recall(managed_capture_callbacks(official_source['plugin.ts']))))
            for name in ('managed-config.ts', 'managed-recall.ts', 'managed-actions.ts', 'managed-pending.ts', 'managed-backlog.ts',
                         'managed-http.ts', 'managed-outbox.ts', 'managed-hooks.ts'):
                (root / name).write_text((ROOT / 'engine/agents/opencode' / name).read_text())
            (root / 'managed-capture-window.ts').write_text((ROOT / 'engine/agents/opencode/managed-capture-window.ts').read_text())
            delivery = root / 'managed-delivery.ts'
            delivery.write_text((ROOT / 'engine/agents/opencode/managed-delivery.ts').read_text())
            build = await asyncio.to_thread(subprocess.run,
                ['node', str(Path(modules).resolve() / 'typescript/bin/tsc'), '--strict',
                 '--target', 'ES2022', '--module', 'NodeNext', '--moduleResolution', 'NodeNext',
                 '--skipLibCheck', '--types', 'node', '--typeRoots', str(Path(modules).resolve() / '@types'),
                 '--outDir', str(root / 'dist'), str(source), str(delivery), str(root / 'plugin.ts'), str(root / 'managed-pending.ts'), str(root / 'managed-backlog.ts')], capture_output=True, text=True, timeout=30)
            self.assertEqual(build.returncode, 0, build.stdout + build.stderr)
            official = OfficialFixture()
            registry = Registry(root / 'registry.json', official)
            registry.state['agents'] = {name: {'name': name, 'principal_id': str(uuid.uuid4())} for name in ('a', 'b')}
            registry.save()
            await registry.ensure_common(registry.state['revision'])
            server = TestServer(create_app(registry, 'm' * 40, {'a': 'a' * 40, 'b': 'b' * 40}))
            await server.start_server()
            try:
                env = {**os.environ, 'MEMLANE_OPENCODE_TEST_CONTROL': str(root / 'dist/managed-control.js'),
                       'MEMLANE_CONTROL_FIXTURE_ROOT': str(root),
                       'MEMLANE_CONTROL_FIXTURE_ORIGIN': str(server.make_url('')).rstrip('/')}
                run = await asyncio.to_thread(subprocess.run, ['node', '--test', str(ROOT / 'engine/agents/opencode/managed-control.test.mjs')],
                                              env=env, capture_output=True, text=True, timeout=40)
                self.assertEqual(run.returncode, 0, run.stdout + run.stderr)
                counts = {key: int(value) for key, value in re.findall(r'^# (tests|pass|fail) (\d+)$', run.stdout, re.M)}
                self.assertEqual(counts, {'tests': 36, 'pass': 36, 'fail': 0})
                print(json.dumps({'node_test': 'managed-control.test.mjs', **counts}))
            finally:
                await server.close()
                registry.close()


if __name__ == '__main__':
    unittest.main()
