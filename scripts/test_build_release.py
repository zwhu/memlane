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
    def _write_fake_repo(self, root: Path, *, version: str = '0.1.0-beta.1', min_sys: str = '14.0', with_graph: bool = True) -> None:
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
            for rel in build_release.REQUIRED_GRAPH_FILES:
                p = root / rel
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_text('{"ok":true}\n', encoding='utf-8')

    def test_validate_manifest_and_missing_graph_assets(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            root = Path(raw_tmp).resolve()
            self._write_fake_repo(root, version='0.0.1')
            with self.assertRaises(ValueError):
                build_release.load_release_manifest(root)

            self._write_fake_repo(root, version='0.1.0-beta.1', min_sys='15.0')
            with self.assertRaises(ValueError):
                build_release.load_release_manifest(root)

            no_graph = root / 'no-graph'
            no_graph.mkdir()
            self._write_fake_repo(no_graph, version='0.1.0-beta.1', min_sys='14.0', with_graph=False)
            with self.assertRaises(ValueError):
                build_release.verify_graph_assets(no_graph)

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
                    'LSMinimumSystemVersion': '14.0',
                }, fh)

            info = build_release.verify_info_plist(app, '0.1.0-beta.1', '14.0')
            self.assertEqual(info['short_version'], '0.1.0-beta.1')
            with self.assertRaises(ValueError):
                build_release.verify_info_plist(app, '0.1.0-beta.2', '14.0')

            raw_dmg = platform_dir / 'memlane-0.1.0-beta.1-arm64.dmg'
            raw_dmg.write_bytes(b'fake-dmg-payload')
            canonical = build_release.normalize_dmg_filename(platform_dir, 'memlane', 'Memlane', '0.1.0-beta.1', 'arm64')
            self.assertEqual(canonical.name, 'Memlane-0.1.0-beta.1-arm64.dmg')
            self.assertIn('Memlane-0.1.0-beta.1-arm64.dmg', [p.name for p in platform_dir.iterdir()])

            sums_path, sums = build_release.write_sha256_sums(platform_dir, [canonical])
            self.assertTrue(sums_path.is_file())
            expected_line = f'{sums[canonical.name]}  Memlane-0.1.0-beta.1-arm64.dmg'
            self.assertIn(expected_line, sums_path.read_text(encoding='utf-8'))

    def _init_git_repo(self, repo: Path) -> str:
        self._write_fake_repo(repo)
        (repo / 'README.md').write_text('# fixture\n', encoding='utf-8')
        subprocess.run(['git', 'init'], cwd=repo, check=True, capture_output=True)
        subprocess.run(['git', 'config', 'user.email', 'test@example.com'], cwd=repo, check=True, capture_output=True)
        subprocess.run(['git', 'config', 'user.name', 'Tester'], cwd=repo, check=True, capture_output=True)
        subprocess.run(['git', 'add', '.'], cwd=repo, check=True, capture_output=True)
        subprocess.run(['git', 'commit', '-m', 'init'], cwd=repo, check=True, capture_output=True)
        return subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip()

    def test_dry_run_git_ref_resolves_commit_and_worktree_preserves_symlink_targets(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            repo = Path(raw_tmp).resolve()
            head_sha = self._init_git_repo(repo)
            out_dir = repo / 'out'
            code, res = build_release.run_build(
                ['--git-ref', 'HEAD', '--out-dir', str(out_dir), '--dry-run', '--json'],
                root=repo,
                prepare_sdk_fn=lambda: '/tmp/fake-patched.mod',
            )
            self.assertEqual(code, 0)
            self.assertEqual(res['git_ref'], 'HEAD')
            self.assertEqual(res['commit'], head_sha)
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


if __name__ == '__main__':
    unittest.main()
