"""Synthetic SDK gates only: no parser, model, filesystem scan or database."""
import asyncio
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from uuid import uuid4

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'engine/python'))
import cognee_code_guard as guard
from test_cognee_submission import fake_modules


class CodeError(Exception):
    def __init__(self, message, status_code):
        self.status_code = status_code
        super().__init__(message)


class Tests(unittest.TestCase):
    def test_all_known_paths_closed_and_private_context_is_exact_and_revocable(self):
        calls, checks = [], []
        dataset, user = str(uuid4()), SimpleNamespace(id=uuid4())
        repository = '/synthetic/private-snapshot'
        active = True
        async def remember(data, dataset_name='main_dataset', *, dataset_id=None, run_in_background=False, **kwargs):
            calls.append('remember'); return 'unchanged'
        async def resolve(directory, user=None, dataset_id=None, **kwargs):
            calls.append('resolve'); return 'resolved'
        async def generate(repo_path, timeout=600):
            calls.append('enola'); return 'generated'
        async def other(*args, **kwargs):
            calls.append('other')
        async def refresh(dataset, user):
            calls.append('refresh'); return 'refreshed'
        async def check():
            checks.append(True); return active
        sdk = fake_modules({
            'cognee': {'remember': remember},
            'cognee.api.v1.remember': {'remember': remember},
            'cognee.api.v1.remember.remember': {'remember': remember},
            'cognee.tasks.code_graph.resolve_repo': {'CodeRepositoryError': CodeError},
            'cognee.tasks.code_graph.code_repo': {'resolve_code_repository': resolve, 'resolve_code_repository_url': other, 'extract_code_repo_graph': other},
            'cognee.tasks.code_graph.enola': {'run_enola_generate': generate, 'run_enola_cluster': other},
            'cognee.tasks.code_graph.extract_code_graph': {'extract_code_graph': other, 'run_enola_generate': generate, 'run_enola_cluster': other},
            'cognee.tasks.code_graph': {'extract_code_graph': other, 'run_enola_generate': generate},
            'cognee.tasks.code_graph.code_files': {'extract_code_files_graph': other},
            'cognee.tasks.code_graph.cluster': {'refresh_dataset_code_graph': refresh, 'load_dataset_cluster': other, 'run_enola_cluster': other},
        })
        async def run():
            nonlocal active
            guard.install(); guard.install()
            public = sdk['cognee'].remember
            self.assertIs(public, sdk['cognee.api.v1.remember'].remember)
            self.assertIs(sdk['cognee.tasks.code_graph.extract_code_graph'].run_enola_generate,
                          sdk['cognee.tasks.code_graph.enola'].run_enola_generate)
            self.assertIs(sdk['cognee.tasks.code_graph'].run_enola_generate,
                          sdk['cognee.tasks.code_graph.enola'].run_enola_generate)
            denied_calls = [
                lambda: public(repository, dataset_id=dataset, user=user, content_type='code'),
                lambda: sdk['cognee.tasks.code_graph.code_repo'].resolve_code_repository(repository),
                lambda: sdk['cognee.tasks.code_graph.code_repo'].resolve_code_repository_url('https://invalid.example/repo'),
                lambda: sdk['cognee.tasks.code_graph.code_repo'].extract_code_repo_graph([]),
                lambda: sdk['cognee.tasks.code_graph.extract_code_graph'].extract_code_graph(),
                lambda: sdk['cognee.tasks.code_graph.enola'].run_enola_generate(repository),
                lambda: sdk['cognee.tasks.code_graph'].extract_code_graph(),
                lambda: sdk['cognee.tasks.code_graph.code_files'].extract_code_files_graph([]),
                lambda: sdk['cognee.tasks.code_graph.cluster'].refresh_dataset_code_graph(SimpleNamespace(id=dataset), user),
            ]
            for invoke in denied_calls:
                with self.assertRaises(CodeError) as error:
                    await invoke()
                self.assertEqual(error.exception.status_code, 403)
            self.assertEqual(calls, [])
            self.assertEqual(await public('ordinary knowledge'), 'unchanged')
            with guard.owned_execution(guard.CodeExecution(repository, dataset, str(user.id), check)):
                self.assertEqual(await public(repository, dataset_id=dataset, user=user, content_type='code'), 'unchanged')
                for changes in ({'dataset_id': uuid4()}, {'user': SimpleNamespace(id=uuid4())}, {'run_in_background': True}, {'index_vectors': True}):
                    options = dict(dataset_id=dataset, user=user, content_type='code'); options.update(changes)
                    with self.assertRaises(CodeError):
                        await public(repository, **options)
                with self.assertRaises(CodeError):
                    await public('/other/repo', dataset_id=dataset, user=user, content_type='code')
                self.assertEqual(await sdk['cognee.tasks.code_graph.enola'].run_enola_generate(repository), 'generated')
                self.assertEqual(await sdk['cognee.tasks.code_graph.cluster'].refresh_dataset_code_graph(SimpleNamespace(id=dataset), user), 'refreshed')
                with self.assertRaises(CodeError):
                    await sdk['cognee.tasks.code_graph.cluster'].refresh_dataset_code_graph(SimpleNamespace(id=uuid4()), user)
                active = False
                with self.assertRaises(CodeError):
                    await sdk['cognee.tasks.code_graph.enola'].run_enola_generate(repository)
            with self.assertRaises(CodeError):
                await public(repository, dataset_id=dataset, user=user, content_type='code')
            self.assertEqual(calls, ['remember', 'remember', 'enola', 'refresh'])
            self.assertGreater(len(checks), 2)
        with patch.dict(sys.modules, sdk):
            asyncio.run(run())


if __name__ == '__main__':
    unittest.main()
