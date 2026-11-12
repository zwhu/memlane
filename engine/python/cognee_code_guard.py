"""Fail-closed gates around the fixed official code pipeline, not a parser.

Only a private in-process owner context can reach it. A public HTTP option,
model result, plugin auto-index setting or dataset credential grants no scan.
The managed executor must supply a live scope check on every guarded boundary.
"""
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass
from functools import wraps
import importlib
import inspect
import os
from uuid import UUID

_execution = ContextVar('memlane_official_code_execution', default=None)


@dataclass(frozen=True)
class CodeExecution:
    repository: str
    dataset: str
    user: str
    check: object
    runtime: object = None


def denied():
    from cognee.tasks.code_graph.resolve_repo import CodeRepositoryError
    raise CodeRepositoryError(message='Enable and run this project code graph through Memlane.', status_code=403)


@contextmanager
def owned_execution(value):
    if not isinstance(value, CodeExecution) or not callable(value.check):
        denied()
    for identifier in (value.dataset, value.user):
        if str(UUID(identifier)) != identifier:
            denied()
    if not os.path.isabs(value.repository) or os.path.normpath(value.repository) != value.repository:
        denied()
    token = _execution.set(value)
    try:
        yield
    finally:
        _execution.reset(token)


async def require(repository=None, dataset=None, user=None):
    value = _execution.get()
    if value is None:
        denied()
    if repository is not None and os.fspath(repository) != value.repository:
        denied()
    if dataset is not None and str(dataset) != value.dataset:
        denied()
    if user is not None and str(user.id) != value.user:
        denied()
    if await value.check() is not True:
        denied()
    return value


def install():
    remember_module = importlib.import_module('cognee.api.v1.remember.remember')
    if getattr(remember_module.remember, '_memlane_code_guard', False):
        return
    package = importlib.import_module('cognee.api.v1.remember')
    cognee = importlib.import_module('cognee')
    repo = importlib.import_module('cognee.tasks.code_graph.code_repo')
    enola = importlib.import_module('cognee.tasks.code_graph.enola')
    extract = importlib.import_module('cognee.tasks.code_graph.extract_code_graph')
    code_package = importlib.import_module('cognee.tasks.code_graph')
    code_files = importlib.import_module('cognee.tasks.code_graph.code_files')
    cluster = importlib.import_module('cognee.tasks.code_graph.cluster')
    # The fixed SDK timeout path does not own coroutine cancellation. Replace
    # only process lifecycle; generation arguments, fact parsing and graph
    # ingestion remain the official implementation.
    from cognee_code_process import run_enola
    enola._run_enola = run_enola
    find_binary = getattr(enola, 'find_enola_binary', None)
    if find_binary is not None:
        def owned_binary():
            value = _execution.get()
            if value is not None and value.runtime is not None:
                return value.runtime.binary
            return find_binary()
        enola.find_enola_binary = owned_binary
    original = remember_module.remember
    signature = inspect.signature(original)

    @wraps(original)
    async def remember(*args, **kwargs):
        bound = signature.bind(*args, **kwargs).arguments
        options = bound.get('kwargs', {})
        if options.get('content_type') == 'code':
            value = await require()
            data = bound.get('data')
            if not isinstance(data, (str, os.PathLike)) or os.fspath(data) != value.repository:
                denied()
            # No default dataset/user resolution or detached SDK background
            # job may escape the owner's frozen scope and cancellation.
            if str(bound.get('dataset_id')) != value.dataset or str(getattr(options.get('user'), 'id', '')) != value.user:
                denied()
            if options.get('index_vectors') not in (None, False) or bound.get('run_in_background', False):
                denied()
        return await original(*args, **kwargs)
    remember._memlane_code_guard = True
    cognee.remember = package.remember = remember_module.remember = remember

    def gate(module, name, fields=(), reject=False):
        old = getattr(module, name)
        sig = inspect.signature(old)
        @wraps(old)
        async def wrapped(*args, **kwargs):
            if reject:
                denied()
            values = sig.bind(*args, **kwargs).arguments
            params = {target: values[source] for source, target in fields if source in values}
            if any(target in ('dataset', 'user', 'context') and params.get(target) is None for _, target in fields):
                denied()
            if 'context' in params:
                context = params.pop('context')
                params['dataset'] = getattr(getattr(context, 'dataset', None), 'id', None)
                if params['dataset'] is None:
                    denied()
            if 'dataset' in params:
                params['dataset'] = getattr(params['dataset'], 'id', params['dataset'])
            await require(**params)
            return await old(*args, **kwargs)
        setattr(module, name, wrapped)
        return old, wrapped

    gate(repo, 'resolve_code_repository', (('directory', 'repository'), ('dataset_id', 'dataset'), ('user', 'user')))
    gate(repo, 'resolve_code_repository_url', reject=True)
    gate(repo, 'extract_code_repo_graph')
    gate(code_files, 'extract_code_files_graph', reject=True)
    old, wrapped = gate(extract, 'extract_code_graph')
    if getattr(code_package, 'extract_code_graph', None) is old:
        code_package.extract_code_graph = wrapped
    old, wrapped = gate(enola, 'run_enola_generate', (('repo_path', 'repository'),))
    if getattr(extract, 'run_enola_generate', None) is old:
        extract.run_enola_generate = wrapped
    if getattr(code_package, 'run_enola_generate', None) is old:
        code_package.run_enola_generate = wrapped
    gate(cluster, 'refresh_dataset_code_graph', (('dataset', 'dataset'), ('user', 'user')))
    gate(cluster, 'load_dataset_cluster', (('ctx', 'context'),))
    # One authorized worktree per owner operation. Cross-repository clustering
    # and remote cloning are not granted by this local project capability.
    old, wrapped = gate(enola, 'run_enola_cluster', reject=True)
    if getattr(extract, 'run_enola_cluster', None) is old:
        extract.run_enola_cluster = wrapped
    if getattr(cluster, 'run_enola_cluster', None) is old:
        cluster.run_enola_cluster = wrapped
