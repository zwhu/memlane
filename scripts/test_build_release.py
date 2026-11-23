from __future__ import annotations

import hashlib
import json
from pathlib import Path
import plistlib
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import build_release


class BuildReleaseTests(unittest.TestCase):
    def _write_fake_repo(
        self,
        root: Path,
        *,
        version: str = '0.1.0-beta.1',
        min_sys: str = '26.0',
        with_graph: bool = True,
        missing_licenses: list[str] | None = None,
    ) -> None:
        (root / 'mygo.json').write_text(json.dumps({
            'name': 'Memlane',
            'slug': 'memlane',
            'identifier': 'io.github.zwhu.memlane',
            'version': version,
            'icon': 'assets/branding/memlane-icon.png',
            'main': './cmd/memlane-app',
            'out': 'build',
            'macos': {'minimumSystemVersion': min_sys, 'signingIdentity': '-'},
        }), encoding='utf-8')
        if with_graph:
            dist = root / 'web' / 'official-graph' / 'dist'
            dist.mkdir(parents=True, exist_ok=True)
            payloads = {
                'index.html': '<!doctype html><html></html>\n',
                'app.js': 'console.log("ok");\n',
                'app.css': 'body{margin:0}\n',
                'host.css': '.host{display:block}\n',
                'app.js.LEGAL.txt': '/* MIT */\n',
                'COGNEE-LICENSE': 'Apache License 2.0\n',
                'THIRD-PARTY-LICENSES.txt': 'react@19.2.4\nMIT\n',
                'build.json': json.dumps({
                    'upstream': '8853378eae5366addb064e758e560a456146ae1a',
                    'modifications': [],
                    'missingLicenseTexts': list(missing_licenses or []),
                    'inputs': {},
                    'outputs': {},
                }) + '\n',
            }
            hashes = {}
            for name, content in payloads.items():
                target = dist / name
                raw = content.encode('utf-8')
                target.write_bytes(raw)
                hashes[name] = hashlib.sha256(raw).hexdigest()
            (dist / 'assets.json').write_text(json.dumps({
                'protocol': 1,
                'upstream': '8853378eae5366addb064e758e560a456146ae1a',
                'files': hashes,
            }, indent=2) + '\n', encoding='utf-8')

    def test_validate_manifest_and_graph_assets_integrity(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            root = Path(raw_tmp).resolve()
            self._write_fake_repo(root, version='0.0.1')
            with self.assertRaises(ValueError):
                build_release.load_release_manifest(root)

            self._write_fake_repo(root, version='0.1.0-beta.1', min_sys='14.0')
            with self.assertRaises(ValueError):
                build_release.load_release_manifest(root)

            no_graph = root / 'no-graph'
            no_graph.mkdir()
            self._write_fake_repo(no_graph, version='0.1.0-beta.1', min_sys='26.0', with_graph=False)
            with self.assertRaises(ValueError):
                build_release.verify_graph_assets(no_graph)

            # Tampering app.css or host.css must fail SHA-256 verification.
            tampered = root / 'tampered'
            tampered.mkdir()
            self._write_fake_repo(tampered)
            build_release.verify_graph_assets(tampered)
            (tampered / 'web/official-graph/dist/app.css').write_text('body{color:red}\n', encoding='utf-8')
            with self.assertRaisesRegex(ValueError, 'sha256 mismatch for app.css'):
                build_release.verify_graph_assets(tampered)

            # Missing host.css must fail verification.
            missing_css = root / 'missing-css'
            missing_css.mkdir()
            self._write_fake_repo(missing_css)
            (missing_css / 'web/official-graph/dist/host.css').unlink()
            with self.assertRaisesRegex(ValueError, 'host.css'):
                build_release.verify_graph_assets(missing_css)

            # Unresolved missingLicenseTexts in build.json must fail verification.
            bad_lic = root / 'bad-lic'
            bad_lic.mkdir()
            self._write_fake_repo(bad_lic, missing_licenses=['example-pkg@1.0.0'])
            with self.assertRaisesRegex(ValueError, 'missingLicenseTexts'):
                build_release.verify_graph_assets(bad_lic)

    def test_dry_run_validates_without_writing_output_directory(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            root = Path(raw_tmp).resolve()
            self._write_fake_repo(root)
            out_dir = root / 'custom-out'
            code, res = build_release.run_build(
                ['--out-dir', str(out_dir), '--dry-run', '--json'],
                root=root,
                prepare_sdk_fn=lambda: '/tmp/fake-patched.mod',
            )
            self.assertEqual(code, 0)
            self.assertEqual(res['status'], 'dry_run')
            self.assertTrue(res['planned_dmg'].endswith('darwin-arm64/Memlane-0.1.0-beta.1-arm64.dmg'))
            self.assertFalse(out_dir.exists())

    def test_verify_info_plist_normalize_dmg_and_sha256sums(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            root = Path(raw_tmp).resolve()
            platform_dir = root / 'darwin-arm64'
            app = platform_dir / 'Memlane.app'
            contents = app / 'Contents'
            contents.mkdir(parents=True)
            with (contents / 'Info.plist').open('wb') as fh:
                plistlib.dump({
                    'CFBundleIdentifier': 'io.github.zwhu.memlane',
                    'CFBundleShortVersionString': '0.1.0-beta.1',
                    'LSMinimumSystemVersion': '26.0',
                }, fh)

            info = build_release.verify_info_plist(app, '0.1.0-beta.1', '26.0')
            self.assertEqual(info['short_version'], '0.1.0-beta.1')
            with self.assertRaises(ValueError):
                build_release.verify_info_plist(app, '0.1.0-beta.2', '26.0')

            raw_dmg = platform_dir / 'memlane-0.1.0-beta.1-arm64.dmg'
            raw_dmg.write_bytes(b'fake-dmg-payload')
            canonical = build_release.normalize_dmg_filename(platform_dir, 'memlane', 'Memlane', '0.1.0-beta.1', 'arm64')
            self.assertEqual(canonical.name, 'Memlane-0.1.0-beta.1-arm64.dmg')
            self.assertIn('Memlane-0.1.0-beta.1-arm64.dmg', [p.name for p in platform_dir.iterdir()])

            sums_path, sums = build_release.write_sha256_sums(platform_dir, [canonical])
            self.assertTrue(sums_path.is_file())
            expected_line = f'{sums[canonical.name]}  Memlane-0.1.0-beta.1-arm64.dmg'
            self.assertIn(expected_line, sums_path.read_text(encoding='utf-8'))

    def _init_git_repo(self, repo: Path, *, with_graph: bool = True) -> str:
        self._write_fake_repo(repo, with_graph=with_graph)
        (repo / 'README.md').write_text('# fixture\n', encoding='utf-8')
        subprocess.run(['git', 'init'], cwd=repo, check=True, capture_output=True)
        subprocess.run(['git', 'config', 'user.email', 'test@example.com'], cwd=repo, check=True, capture_output=True)
        subprocess.run(['git', 'config', 'user.name', 'Tester'], cwd=repo, check=True, capture_output=True)
        subprocess.run(['git', 'add', '.'], cwd=repo, check=True, capture_output=True)
        subprocess.run(['git', 'commit', '-m', 'init'], cwd=repo, check=True, capture_output=True)
        return subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip()

    def test_dry_run_git_ref_validates_target_ref_not_dirty_workspace(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            repo = Path(raw_tmp).resolve()
            head_sha = self._init_git_repo(repo)
            out_dir = repo / 'out'

            # Mutate the dirty working tree to an invalid version & tampered CSS;
            # --git-ref HEAD --dry-run must still validate the clean HEAD commit.
            self._write_fake_repo(repo, version='0.0.1')
            (repo / 'web/official-graph/dist/app.css').write_text('dirty-tamper\n', encoding='utf-8')

            code, res = build_release.run_build(
                ['--git-ref', 'HEAD', '--out-dir', str(out_dir), '--dry-run', '--json'],
                root=repo,
                prepare_sdk_fn=lambda: '/tmp/fake-patched.mod',
            )
            self.assertEqual(code, 0)
            self.assertEqual(res['git_ref'], 'HEAD')
            self.assertEqual(res['commit'], head_sha)
            self.assertEqual(res['manifest']['version'], '0.1.0-beta.1')
            self.assertFalse(out_dir.exists())

            local_dir = repo / '.local'
            cache_dir = repo / '.cache'
            local_dir.mkdir()
            cache_dir.mkdir()
            (local_dir / 'toolchain.txt').write_text('keep-local\n', encoding='utf-8')
            (cache_dir / 'go-build.txt').write_text('keep-cache\n', encoding='utf-8')

            with build_release._worktree_for_ref(repo, head_sha) as wt:
                self.assertTrue((wt / '.local').is_symlink())
                self.assertTrue((wt / '.cache').is_symlink())
                self.assertEqual((wt / '.local' / 'toolchain.txt').read_text(encoding='utf-8'), 'keep-local\n')
                wt_parent = wt.parent

            self.assertFalse(wt_parent.exists())
            self.assertTrue((local_dir / 'toolchain.txt').is_file())
            self.assertTrue((cache_dir / 'go-build.txt').is_file())
            worktrees = subprocess.check_output(['git', 'worktree', 'list', '--porcelain'], cwd=repo, text=True)
            self.assertNotIn(str(wt_parent), worktrees)

            # A commit lacking tracked web/official-graph/dist must fail --git-ref even if
            # the host working tree contains untracked dist/ assets (no dirty-workspace copy).
            with tempfile.TemporaryDirectory() as raw_tmp2:
                no_dist_repo = Path(raw_tmp2).resolve()
                no_dist_sha = self._init_git_repo(no_dist_repo, with_graph=False)
                # Populate untracked dist/ in the host working tree only.
                self._write_fake_repo(no_dist_repo, with_graph=True)
                with self.assertRaises(ValueError):
                    build_release.run_build(
                        ['--git-ref', no_dist_sha, '--dry-run', '--json'],
                        root=no_dist_repo,
                        prepare_sdk_fn=lambda: '/tmp/fake-patched.mod',
                    )


if __name__ == '__main__':
    unittest.main()
