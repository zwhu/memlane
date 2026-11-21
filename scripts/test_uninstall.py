import fcntl
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import uninstall


class UninstallScriptTests(unittest.TestCase):
    def _build_fixture(self, tmp: Path):
        home = tmp / 'home'
        runtime_dir = home / 'Library' / 'Application Support' / 'Memlane'
        components_dir = runtime_dir / 'components'
        store_dir = runtime_dir / 'official-store'
        for p in (home, runtime_dir, components_dir, store_dir):
            p.mkdir(parents=True, exist_ok=True)

        # Runtime store + config (must be preserved unless --remove-data --confirm-remove-data).
        (runtime_dir / 'runtime.json').write_text('{"mode": "daily"}\n', encoding='utf-8')
        (store_dir / 'memory.db').write_text('important-user-data\n', encoding='utf-8')

        # Managed components + unrelated dir inside components.
        uv_dir = components_dir / 'uv-0.12.3'
        uv_dir.mkdir()
        (uv_dir / '.memlane-tool.json').write_text('{}\n', encoding='utf-8')
        custom_dir = components_dir / 'user-custom-dir'
        custom_dir.mkdir()
        (custom_dir / 'keep.txt').write_text('keep\n', encoding='utf-8')

        # LaunchAgent.
        la = home / 'Library' / 'LaunchAgents' / 'com.memlane.desktop.plist'
        la.parent.mkdir(parents=True, exist_ok=True)
        la.write_text('<plist>memlane</plist>\n', encoding='utf-8')

        # 1. Codex config (preserving user comment + unrelated MCP server & feature).
        codex_toml = home / '.codex' / 'config.toml'
        codex_toml.parent.mkdir(parents=True, exist_ok=True)
        codex_toml.write_text(
            '# User Codex config\n'
            'model = "gpt-5"\n\n'
            '[mcp_servers.context7]\n'
            'command = "npx"\n\n'
            '[marketplaces.memlane]\n'
            'source_type = "local"\n'
            'source = "/tmp/memlane-marketplace"\n\n'
            '[plugins."cognee@memlane"]\n'
            'enabled = true\n\n'
            '[plugins."other-plugin@official"]\n'
            'enabled = true\n',
            encoding='utf-8',
        )
        codex_cache = home / '.codex' / 'plugins' / 'cache' / 'memlane' / 'cognee'
        codex_cache.mkdir(parents=True)
        (codex_cache / 'plugin.py').write_text('pass\n', encoding='utf-8')

        # 2. Antigravity config + plugin dir.
        ag_dir = home / '.gemini' / 'config'
        (ag_dir / 'plugins' / 'memlane-cognee').mkdir(parents=True)
        (ag_dir / 'plugins' / 'memlane-cognee' / '.memlane-installation.json').write_text('{}\n', encoding='utf-8')
        (ag_dir / 'plugins' / 'other-plugin').mkdir(parents=True)
        (ag_dir / 'config.json').write_text(
            json.dumps({
                'theme': 'dark',
                'plugins': {
                    'memlane-cognee': {'enabled': True},
                    'other-plugin': {'enabled': True},
                },
            }),
            encoding='utf-8',
        )

        # 3. pi extension.
        pi_ext = home / '.pi' / 'agent' / 'extensions'
        (pi_ext / 'memlane-cognee').mkdir(parents=True)
        (pi_ext / 'memlane-cognee' / 'index.ts').write_text('export default {}\n', encoding='utf-8')
        (pi_ext / 'user-ext').mkdir(parents=True)
        (pi_ext / 'user-ext' / 'index.ts').write_text('keep\n', encoding='utf-8')

        # 4. TraeX config + marketplace/cache.
        trae_dir = home / '.traecli'
        trae_dir.mkdir(parents=True)
        (trae_dir / 'traecli.toml').write_text(
            'theme = "solarized"\n\n'
            '[plugins]\n'
            '"memlane-cognee@memlane" = { enabled = true }\n'
            '"other@community" = { enabled = true }\n',
            encoding='utf-8',
        )
        (trae_dir / '.tmp' / 'marketplaces' / 'memlane-local').mkdir(parents=True)
        (trae_dir / 'plugins' / 'cache' / 'local' / 'memlane-cognee').mkdir(parents=True)

        return home, runtime_dir, components_dir, store_dir

    def test_dry_run_makes_zero_mutations_and_json_is_valid(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            tmp = Path(raw_tmp).resolve()
            home, runtime_dir, components_dir, store_dir = self._build_fixture(tmp)
            proc = subprocess.run(
                [sys.executable, str(Path(__file__).resolve().parent / 'uninstall.py'),
                 '--home', str(home), '--runtime-dir', str(runtime_dir), '--components-dir', str(components_dir), '--json'],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            receipt = json.loads(proc.stdout)
            self.assertTrue(receipt['dry_run'])
            self.assertEqual(receipt['status'], 'dry_run')
            self.assertGreaterEqual(len(receipt['planned']), 5)
            self.assertEqual(receipt['applied'], [])
            self.assertTrue((components_dir / 'uv-0.12.3').exists())
            self.assertTrue((home / '.pi/agent/extensions/memlane-cognee').exists())
            self.assertTrue((store_dir / 'memory.db').exists())

    def test_locked_service_owner_blocks_apply_and_warns_on_dry_run(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            tmp = Path(raw_tmp).resolve()
            home, runtime_dir, components_dir, store_dir = self._build_fixture(tmp)
            owner_lock = store_dir / '.service-owner'
            with owner_lock.open('w', encoding='utf-8') as f:
                fcntl.flock(f.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                try:
                    code_dry, dry_res = uninstall.run_uninstall([
                        '--home', str(home), '--runtime-dir', str(runtime_dir), '--components-dir', str(components_dir),
                    ])
                    self.assertEqual(code_dry, 0)
                    self.assertTrue(dry_res['locked_owners'])

                    code_apply, apply_res = uninstall.run_uninstall([
                        '--apply', '--home', str(home), '--runtime-dir', str(runtime_dir), '--components-dir', str(components_dir),
                    ])
                    self.assertEqual(code_apply, 1)
                    self.assertEqual(apply_res['status'], 'refused_service_running')
                    self.assertTrue((components_dir / 'uv-0.12.3').exists())
                finally:
                    fcntl.flock(f.fileno(), fcntl.LOCK_UN)

    def test_apply_cleans_only_memlane_entries_creates_backups_and_preserves_data(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            tmp = Path(raw_tmp).resolve()
            home, runtime_dir, components_dir, store_dir = self._build_fixture(tmp)
            code, res = uninstall.run_uninstall([
                '--apply', '--home', str(home), '--runtime-dir', str(runtime_dir),
                '--components-dir', str(components_dir), '--launch-agent', str(home / 'Library/LaunchAgents/com.memlane.desktop.plist'),
            ])
            self.assertEqual(code, 0, res)
            self.assertEqual(res['status'], 'completed')
            self.assertGreaterEqual(len(res['backups']), 3)

            # Components cleaned; unrelated preserved.
            self.assertFalse((components_dir / 'uv-0.12.3').exists())
            self.assertTrue((components_dir / 'user-custom-dir/keep.txt').is_file())
            self.assertFalse((home / 'Library/LaunchAgents/com.memlane.desktop.plist').exists())

            # Store and runtime.json preserved.
            self.assertTrue((store_dir / 'memory.db').is_file())
            self.assertTrue((runtime_dir / 'runtime.json').is_file())

            # Codex cleaned & preserved user entries/comment.
            codex_text = (home / '.codex/config.toml').read_text(encoding='utf-8')
            self.assertIn('# User Codex config', codex_text)
            self.assertIn('[mcp_servers.context7]', codex_text)
            self.assertIn('[plugins."other-plugin@official"]', codex_text)
            self.assertNotIn('cognee@memlane', codex_text)
            self.assertNotIn('[marketplaces.memlane]', codex_text)
            self.assertFalse((home / '.codex/plugins/cache/memlane/cognee').exists())

            # Antigravity cleaned & preserved other plugin.
            ag_data = json.loads((home / '.gemini/config/config.json').read_text(encoding='utf-8'))
            self.assertEqual(ag_data['theme'], 'dark')
            self.assertNotIn('memlane-cognee', ag_data['plugins'])
            self.assertIn('other-plugin', ag_data['plugins'])
            self.assertFalse((home / '.gemini/config/plugins/memlane-cognee').exists())
            self.assertTrue((home / '.gemini/config/plugins/other-plugin').is_dir())

            # pi cleaned & preserved user extension.
            self.assertFalse((home / '.pi/agent/extensions/memlane-cognee').exists())
            self.assertTrue((home / '.pi/agent/extensions/user-ext/index.ts').is_file())

            # TraeX cleaned & preserved unrelated setting/plugin.
            trae_text = (home / '.traecli/traecli.toml').read_text(encoding='utf-8')
            self.assertIn('theme = "solarized"', trae_text)
            self.assertIn('"other@community"', trae_text)
            self.assertNotIn('memlane-cognee@memlane', trae_text)
            self.assertFalse((home / '.traecli/.tmp/marketplaces/memlane-local').exists())
            self.assertFalse((home / '.traecli/plugins/cache/local/memlane-cognee').exists())

    def test_remove_data_requires_explicit_confirmation(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            tmp = Path(raw_tmp).resolve()
            home, runtime_dir, components_dir, store_dir = self._build_fixture(tmp)

            code_refused, res_refused = uninstall.run_uninstall([
                '--apply', '--remove-data', '--home', str(home), '--runtime-dir', str(runtime_dir), '--components-dir', str(components_dir),
            ])
            self.assertEqual(code_refused, 2)
            self.assertEqual(res_refused['status'], 'refused_unconfirmed_data_removal')
            self.assertTrue((store_dir / 'memory.db').exists())

            code_ok, _ = uninstall.run_uninstall([
                '--apply', '--remove-data', '--confirm-remove-data',
                '--home', str(home), '--runtime-dir', str(runtime_dir), '--components-dir', str(components_dir),
            ])
            self.assertEqual(code_ok, 0)
            self.assertFalse((store_dir / 'memory.db').exists())
            self.assertFalse((runtime_dir / 'runtime.json').exists())

    def test_symlink_target_is_refused_without_touching_external_files(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            tmp = Path(raw_tmp).resolve()
            home, runtime_dir, components_dir, _ = self._build_fixture(tmp)
            external = tmp / 'external-target'
            external.mkdir()
            (external / 'precious.txt').write_text('do-not-delete\n', encoding='utf-8')

            pi_ext = home / '.pi/agent/extensions/memlane-cognee'
            # Replace memlane-cognee with a symlink to external.
            for child in list(pi_ext.iterdir()):
                child.unlink()
            pi_ext.rmdir()
            os.symlink(external, pi_ext)

            code, res = uninstall.run_uninstall([
                '--apply', '--home', str(home), '--runtime-dir', str(runtime_dir), '--components-dir', str(components_dir),
            ])
            self.assertEqual(code, 0)
            self.assertTrue(any('symlink-rejected' in s for s in res['skipped']))
            self.assertTrue((external / 'precious.txt').is_file())


if __name__ == '__main__':
    unittest.main()
