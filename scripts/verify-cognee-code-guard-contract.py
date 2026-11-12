#!/usr/bin/env python3
"""Pinned real SDK exports and denial before original pipeline; no model/scan."""
import asyncio
import argparse
import importlib
import json
import hashlib
import os
from pathlib import Path
import sys
import tempfile
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[1]
SDK = ROOT / '.cache/coding-memory-components/service-ec6f40859e5f2df9e7862dde27d580ac3268ed43'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--exercise-enola', action='store_true', help='Run the installed pinned Enola on synthetic TS/Python only')
    args = parser.parse_args()
    from coding_memory_recipe import prepare_source
    source, verified = prepare_source('service', ROOT/'.cache/coding-memory-components')
    assert source == SDK
    with tempfile.TemporaryDirectory(prefix='memlane-code-guard-') as temporary:
        root = Path(temporary).resolve()
        for name in ('home', 'system', 'data', 'cache', 'logs'):
            (root / name).mkdir()
        os.environ.update(HOME=str(root/'home'), SYSTEM_ROOT_DIRECTORY=str(root/'system'),
            DATA_ROOT_DIRECTORY=str(root/'data'), CACHE_ROOT_DIRECTORY=str(root/'cache'),
            COGNEE_LOGS_DIR=str(root/'logs'), LOGS_ROOT_DIRECTORY=str(root/'logs'),
            COGNEE_LOG_FILE='false', TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false',
            PYTHON_DOTENV_DISABLED='1', HF_HUB_OFFLINE='1', LITELLM_LOCAL_MODEL_COST_MAP='true')
        os.chdir(root)
        sys.path[:0] = [str(SDK), str(ROOT/'engine/python')]
        import cognee
        assert Path(cognee.__file__).resolve().is_relative_to(SDK)
        from cognee.tasks.code_graph.resolve_repo import CodeRepositoryError
        import cognee_code_guard as guard
        guard.install()
        implementation = importlib.import_module('cognee.api.v1.remember.remember')
        package = importlib.import_module('cognee.api.v1.remember')
        repo = importlib.import_module('cognee.tasks.code_graph.code_repo')
        enola = importlib.import_module('cognee.tasks.code_graph.enola')
        extract = importlib.import_module('cognee.tasks.code_graph.extract_code_graph')
        code_package = importlib.import_module('cognee.tasks.code_graph')
        files = importlib.import_module('cognee.tasks.code_graph.code_files')
        assert cognee.remember is package.remember is implementation.remember
        assert extract.run_enola_generate is enola.run_enola_generate
        assert code_package.run_enola_generate is enola.run_enola_generate
        assert code_package.extract_code_graph is extract.extract_code_graph
        async def run():
            denied = 0
            for invoke in (
                lambda: cognee.remember('/not-an-existing-project', dataset_id=uuid4(), content_type='code'),
                lambda: repo.resolve_code_repository(Path('/not-an-existing-project')),
                lambda: repo.extract_code_repo_graph([]),
                lambda: enola.run_enola_generate('/not-an-existing-project'),
                lambda: code_package.extract_code_graph(repo_path='/not-an-existing-project'),
                lambda: files.extract_code_files_graph([]),
            ):
                try:
                    await invoke()
                except CodeRepositoryError as error:
                    assert error.status_code == 403
                    assert error.message == 'Enable and run this project code graph through Memlane.'
                    denied += 1
                else:
                    raise AssertionError('unguarded official entry')
            return denied
        denials = asyncio.run(run())
        enola_result = None
        if args.exercise_enola:
            from importlib.metadata import version
            from cognee_code_process import EnolaRuntime
            assert version('enola-cli') == '0.4.30'
            binary = (ROOT/'.local/cognee-python/bin/enola').resolve(strict=True)
            binary_sha = hashlib.sha256(binary.read_bytes()).hexdigest()
            scratch = root/'code-job'; scratch.mkdir(mode=0o700)
            repository = scratch/'synthetic-project'; repository.mkdir(mode=0o700)
            (repository/'package.json').write_text('{"name":"synthetic-project","version":"1.0.0"}')
            (repository/'helper.ts').write_text('export function greet(name: string) { return "Hello " + name; }\n')
            (repository/'index.ts').write_text('import {greet} from "./helper"; export function main() { return greet("world"); }\n')
            (repository/'main.py').write_text('def add(a, b):\n    return a + b\n\ndef run():\n    return add(1, 2)\n')
            original = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in repository.iterdir()}
            os.environ['ENOLA_PATH'] = str(binary)
            async def check(): return True
            value = guard.CodeExecution(str(repository), str(uuid4()), str(uuid4()), check,
                EnolaRuntime(str(binary), binary_sha, str(scratch), (str(root/'data'), str(root/'system'))))
            async def generate():
                with guard.owned_execution(value):
                    snapshot = await enola.run_enola_generate(repository, timeout=60)
                facts, receipt = enola.parse_enola_snapshot(snapshot)
                assert receipt and receipt.get('format_version') in (None, 1)
                symbols = [f for f in facts if f.get('kind') == 'symbol']
                assert any('greet' in str(f.get('name')) for f in symbols), 'TS symbol missing'
                assert any('add' in str(f.get('name')) for f in symbols), 'Python symbol missing'
                assert original == {name:hashlib.sha256((repository/name).read_bytes()).hexdigest() for name in original}
                return {'passed': True, 'enola_package_version':'0.4.30', 'binary_sha256': binary_sha,
                    'facts':len(facts), 'symbols':len(symbols), 'official_parser':True,
                    'synthetic_source_preserved':True, 'graph_written':False, 'model_used':False}
            enola_result = asyncio.run(generate())
        print(json.dumps({'passed': True, 'real_sdk_denials': denials,
            'sdk_commit': SDK.name.removeprefix('service-'), 'public_exports_and_enola_alias': True,
            'official_source_files_verified': len(verified),
            'guard_sha256': hashlib.sha256((ROOT/'engine/python/cognee_code_guard.py').read_bytes()).hexdigest(),
            'enola_execution': enola_result,
            'scope': 'fixed SDK denial and optional official generation/parser on synthetic source only; no graph writes, daily project, model, service or Native acceptance'}))


if __name__ == '__main__':
    main()
