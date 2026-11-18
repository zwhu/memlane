import ast
import asyncio
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock

from lesson_provenance_patch import apply, prepare, MODULE, LANGUAGE_PROMPTS
from language_settings import language_settings, knowledge_language_instructions


class LessonProvenanceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        root = Path(__file__).resolve().parents[2]
        cls.manifest = json.loads((root / 'engine/coding-memory-components.json').read_text())['service']
        cls.source = root / '.cache/coding-memory-components' / ('service-' + cls.manifest['commit'])
        cls.original = (cls.source / MODULE).read_text()
        cls.patched = apply(cls.original)

    def functions(self, *names, **bindings):
        tree = ast.parse(self.patched)
        nodes = [node for node in tree.body if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name in names]
        code = ast.Module(body=nodes, type_ignores=[])
        # Postpone upstream annotations without importing any runtime or store.
        namespace = {'__builtins__': __builtins__, **bindings}
        import __future__
        exec(compile(code, '<pinned-provenance-test>', 'exec', flags=__future__.annotations.compiler_flag), namespace)
        return namespace

    def test_each_lesson_uses_its_own_earliest_linked_qa_not_session_start(self):
        qas = [{'qa_id': q, 'time': day, 'context': json.dumps({'date': day})}
               for q, day in [('a', '2026-11-14'), ('b', '2026-11-15'), ('c', '2026-11-16')]]
        f = self.functions('lesson_turn_context',
            entry_turns=lambda entry, rows: [r for r in rows if r['qa_id'] in entry.source_qa_ids],
            newest_qa_row=lambda rows: max(rows, key=lambda r: r['time']) if rows else None,
            parse_qa_context=lambda context: json.loads(context or '{}'))['lesson_turn_context']
        a = f([SimpleNamespace(source_qa_ids=['a', 'b'])], qas)
        b = f([SimpleNamespace(source_qa_ids=['b'])], qas)
        self.assertEqual((a['first_source_date'], b['first_source_date']), ('2026-11-14', '2026-11-15'))
        self.assertEqual(a['date'], '2026-11-15', 'native latest-state ordering changed')
        self.assertEqual(a['source_qa_ids'], ['a', 'b'])
        self.assertEqual(f([SimpleNamespace(source_qa_ids=[])], qas)['source_date_status'], 'unconfirmed')

    def test_reprocessing_preserves_trusted_earliest_date_before_replacement(self):
        async def scenario():
            document = SimpleNamespace(metadata={'state_key': 'feature', 'lesson_type': 'state', 'first_source_date': '2026-11-16'})
            f = self.functions('build_coding_lesson_documents',
                render_coding_lesson_document=lambda *a, **k: 'unchanged-body',
                lesson_metadata=lambda *a: dict(document.metadata),
                lesson_data_id=lambda *a: 'same-official-id',
                LessonDocument=lambda **k: SimpleNamespace(**k),
                find_lessons_by_state_key=AsyncMock(return_value=[SimpleNamespace(
                    system_metadata={'lesson_type': 'state', 'first_source_date': '2026-11-14'},
                    external_metadata={'first_source_date': '1999-01-01'})]))['build_coding_lesson_documents']
            result = await f(SimpleNamespace(dataset=SimpleNamespace(id='ds'), session_id='s'),
                [SimpleNamespace(lesson_type='state', state_key='feature', statement='same', turn_context={})], ['project'])
            self.assertEqual(result[0].metadata['first_source_date'], '2026-11-14')
            self.assertEqual(result[0].text, 'unchanged-body')
        asyncio.run(scenario())

    def test_component_is_verified_and_upstream_is_immutable(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / 'base'; (source / MODULE).parent.mkdir(parents=True)
            (source / MODULE).write_text(self.original)
            from capture_service_patch import MANAGER, ADAPTER
            for name in (MANAGER, ADAPTER):
                (source / name).parent.mkdir(parents=True, exist_ok=True)
                (source / name).write_bytes((self.source / name).read_bytes())
            for name in LANGUAGE_PROMPTS:
                (source / name).parent.mkdir(parents=True, exist_ok=True)
                (source / name).write_text((self.source / name).read_text())
            manifest = {'packages': ['cognee'], 'commit': self.manifest['commit']}
            target = prepare(source, manifest, Path(directory) / 'generated')
            self.assertEqual((source / MODULE).read_text(), self.original)
            self.assertEqual((target / MODULE).read_text(), self.patched)
            for name in LANGUAGE_PROMPTS:
                self.assertEqual((source / name).read_bytes(), (self.source / name).read_bytes())
                self.assertEqual((target / name).read_text(), (source / name).read_text() + knowledge_language_instructions())
            receipt = json.loads((Path(directory) / 'generated/lesson-provenance-applied.json').read_text())
            self.assertEqual(receipt['language'], language_settings())
            self.assertEqual(receipt['language']['language'], 'zh-CN')
            self.assertFalse(receipt['language']['configurable'])
            (target / LANGUAGE_PROMPTS[0]).write_text('changed')
            with self.assertRaisesRegex(ValueError, 'component_changed'):
                prepare(source, manifest, Path(directory) / 'generated')
            (target / LANGUAGE_PROMPTS[0]).write_text((source / LANGUAGE_PROMPTS[0]).read_text() + knowledge_language_instructions())
            (target / MODULE).write_text('changed')
            with self.assertRaisesRegex(ValueError, 'component_changed'):
                prepare(source, manifest, Path(directory) / 'generated')
        with self.assertRaises(ValueError):
            apply(self.original + self.original)
