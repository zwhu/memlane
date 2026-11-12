"""Source-only integrity checks; no downloads, models or component build."""
import io
import json
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'engine/python'))
import coding_memory_recipe as recipe
from cognee_component_contract import coding_contract


class Tests(unittest.TestCase):
    def test_lock_and_runtime_contract_are_the_same_fixed_pair(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.assertIsNone(coding_contract(root))
            marker = root / 'memlane-coding-memory.json'
            marker.write_text(json.dumps(recipe.contract()))
            self.assertEqual(coding_contract(root), recipe.contract())
            bad = recipe.contract(); bad['plugin_commit'] = 'foreign-source'
            marker.write_text(json.dumps(bad))
            with self.assertRaises(ValueError): coding_contract(root)

    def test_fixed_archive_extraction_detects_modified_extra_and_symlinked_source(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / 'source.tar.gz'
            with tarfile.open(archive, 'w:gz') as stream:
                for name, data in [('cognee/__init__.py', b'# synthetic public SDK\n'), ('cognee/prompt.txt', b'synthetic prompt'), ('pyproject.toml', b'[project]\nname="cognee"\n')]:
                    entry = tarfile.TarInfo('fixed/' + name); entry.size = len(data); entry.mode = 0o644
                    stream.addfile(entry, io.BytesIO(data))
            item = {'commit': 'synthetic', 'archive_sha256': recipe.digest(archive), 'packages': ['cognee']}
            with patch.object(recipe, 'recipe', lambda: {'service': item}), patch.object(recipe, 'obtain_archive', lambda item, cache: archive):
                tree, hashes = recipe.prepare_source('service', root)
                self.assertIn('cognee/prompt.txt', hashes)
                self.assertEqual(recipe.prepare_source('service', root)[1], hashes)
                source = tree / 'cognee/__init__.py'
                source.write_text('# altered')
                with self.assertRaisesRegex(ValueError, 'fixed_source_tree_changed'): recipe.prepare_source('service', root)
                source.write_text('# synthetic public SDK\n')
                extra = tree / 'extra.py'; extra.write_text('# extra')
                with self.assertRaisesRegex(ValueError, 'fixed_source_tree_changed'): recipe.prepare_source('service', root)
                extra.unlink()
                # A planted ancestor must not receive extraction writes.
                source.unlink(); (tree / 'cognee/prompt.txt').unlink(); (tree / 'cognee').rmdir()
                outside = root / 'outside'; outside.mkdir()
                (tree / 'cognee').symlink_to(outside, target_is_directory=True)
                with self.assertRaisesRegex(ValueError, 'fixed_source_tree_changed'): recipe.prepare_source('service', root)
                self.assertEqual(list(outside.iterdir()), [])


if __name__ == '__main__': unittest.main()
