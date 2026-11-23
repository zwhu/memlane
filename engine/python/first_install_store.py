"""Initialize one explicitly owned first-use store through the pinned Cognee SDK.

No SDK imports occur until paths, ownership and the fixed source are verified.
Credentials never appear on stdout or in the non-secret completion receipt.
"""
import argparse
import asyncio
import json
import os
from pathlib import Path
import stat
import sys
import tempfile
from uuid import UUID, uuid4


class StoreInitializationError(RuntimeError):
    pass


def read_private(path):
    path = Path(path)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'r') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
            raise StoreInitializationError('private_initialization_file_required')
        return stream.read()


def write_private(path, value):
    from _runtime_common import private_write
    private_write(path, value.encode('utf-8'))


def validate_spec(spec):
    root = Path(spec['root'])
    if spec.get('schema') != 1 or not root.is_absolute():
        raise StoreInitializationError('invalid_first_store_specification')
    for path in (root, *root.parents):
        if path.is_symlink():
            raise StoreInitializationError('first_store_path_must_not_traverse_links')
    if not root.is_dir() or root.stat().st_mode & 0o077:
        raise StoreInitializationError('private_owned_store_required')
    marker = json.loads(read_private(root / '.memlane-first-store.json'))
    expected = {key: spec[key] for key in ('schema', 'store_id', 'source_commit', 'model_digest')}
    if marker != expected:
        raise StoreInitializationError('store_ownership_or_inputs_changed')
    return root


def configure_environment(spec, root):
    # A manual invocation must be isolated too: never inherit account/provider
    # credentials, user dotenv, or default storage/cache paths into the SDK.
    preserved = {key: os.environ[key] for key in ('PATH', 'LANG', 'TMPDIR') if key in os.environ}
    os.environ.clear()
    os.environ.update(preserved)
    for name in ('home', 'system', 'data', 'cache', 'logs'):
        path = root / name
        if path.is_symlink():
            raise StoreInitializationError('linked_store_destination_rejected')
        path.mkdir(mode=0o700, exist_ok=True)
    database_directory = root / 'system/databases'
    if database_directory.is_symlink():
        raise StoreInitializationError('linked_store_destination_rejected')
    database_directory.mkdir(mode=0o700, exist_ok=True)
    os.environ.update(
        HOME=str(root / 'home'), SYSTEM_ROOT_DIRECTORY=str(root / 'system'),
        DATA_ROOT_DIRECTORY=str(root / 'data'), CACHE_ROOT_DIRECTORY=str(root / 'cache'),
        DB_PATH=str(root / 'system/databases'), CACHE_BACKEND='sqlite', CACHING='true',
        LOGS_ROOT_DIRECTORY=str(root / 'logs'), COGNEE_LOGS_DIR=str(root / 'logs'),
        TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false', PYTHON_DOTENV_DISABLED='1',
        HF_HOME=spec['model_cache'], HF_HUB_OFFLINE='1', TRANSFORMERS_OFFLINE='1',
        EMBEDDING_PROVIDER='fastembed', EMBEDDING_MODEL='BAAI/bge-small-zh-v1.5',
        EMBEDDING_DIMENSIONS='512', EMBEDDING_MAX_TOKENS='512',
        DEFAULT_USER_EMAIL='memlane-local@example.com', ENABLE_BACKEND_ACCESS_CONTROL='true',
        REQUIRE_AUTHENTICATION='true', COGNEE_AGENT_MODE='false', DISTILLATION_MODE='coding',
        LITELLM_LOCAL_MODEL_COST_MAP='true',
    )


class PinnedSDK:
    def __init__(self, spec):
        from official_service_dev import verify_source
        source = verify_source({'official_source': spec['source'], 'official_archive': spec['archive']})
        sys.path.insert(0, str(source))
        from cognee.modules.migrations.startup import run_migrations
        from cognee.modules.users.methods.get_default_user import get_default_user
        from cognee.modules.users.api_key.get_api_keys import get_api_keys
        from cognee.modules.users.api_key.create_api_key import build_api_key_row, apiKeySettings
        from cognee.modules.users.api_key.hash_api_key import prepare_api_key
        from cognee.modules.users.models.UserApiKey import UserApiKey
        from cognee.infrastructure.databases.relational import get_relational_engine
        self.migrations, self.default_user, self.keys = run_migrations, get_default_user, get_api_keys
        self.build_key, self.prepare_key = build_api_key_row, prepare_api_key
        self.key_model, self.engine = UserApiKey, get_relational_engine
        self.max_keys = apiKeySettings.max_user_api_keys

    def new_key(self, user):
        row, raw = self.build_key(user.id, 'Memlane local owner')
        # Assign the row identity before commit so an unknown commit can be read
        # back without generating another key. The official model/format is kept.
        row.id = uuid4()
        return row, raw

    def restore_key(self, pending):
        return self.key_model(id=UUID(pending['key_id']), user_id=UUID(pending['owner_id']),
                              api_key=self.prepare_key(pending['raw_key']),
                              label=pending['raw_key'][:8] + '****', name='Memlane local owner')

    async def commit_key(self, row):
        async with self.engine().get_async_session() as session:
            session.add(row)
            try:
                await session.commit()
            except Exception:
                # SQL exceptions may include bound key values. Keep the unknown
                # commit state and pending secret, never export the exception.
                raise StoreInitializationError('owner_key_commit_unconfirmed') from None


async def initialize(spec, sdk=None, after_step=None):
    root = validate_spec(spec)
    if sdk is None:
        configure_environment(spec, root)
        from ladybug_extension import prepare_json_extension, probe_json_extension
        try:
            prepare_json_extension(root / 'home')
            probe_json_extension()
        except Exception:
            raise StoreInitializationError('ladybug_json_extension_preflight_failed') from None
        sdk = PinnedSDK(spec)
    after_step = after_step or (lambda step: None)
    failed = await sdk.migrations()
    if failed:
        raise StoreInitializationError('official_store_migration_failed')
    after_step('migrations')
    user = await sdk.default_user()
    after_step('owner')
    owner_id = str(user.id)
    keys = await sdk.keys(user)
    receipt_path, token_path = root / 'store-identity.json', root / 'owner.token'
    pending_path = root / '.owner-key-pending.json'

    if receipt_path.exists():
        receipt = json.loads(read_private(receipt_path))
        raw = read_private(token_path).strip()
        matching = [row for row in keys if str(row.id) == receipt['key_id'] and
                    str(row.user_id) == owner_id and row.api_key == sdk.prepare_key(raw)]
        if (receipt.get('schema') != 1 or receipt.get('store_id') != spec['store_id'] or
                receipt.get('owner_id') != owner_id or receipt.get('source_commit') != spec['source_commit'] or
                not receipt.get('storage_initialized') or len(matching) != 1):
            raise StoreInitializationError('existing_store_identity_unconfirmed')
        if pending_path.exists():
            pending = json.loads(read_private(pending_path))
            if pending.get('raw_key') != raw or pending.get('key_id') != receipt['key_id']:
                raise StoreInitializationError('pending_key_conflicts_with_store_identity')
            pending_path.unlink()
        return receipt

    if pending_path.exists():
        pending = json.loads(read_private(pending_path))
        if (pending.get('schema') != 1 or pending.get('owner_id') != owner_id or
                pending.get('store_id') != spec['store_id']):
            raise StoreInitializationError('pending_owner_key_identity_changed')
        if (not isinstance(pending.get('raw_key'), str) or len(pending['raw_key']) != 64 or
                any(character not in '0123456789abcdef' for character in pending['raw_key'])):
            raise StoreInitializationError('pending_owner_key_material_invalid')
        row = sdk.restore_key(pending)
        raw = pending['raw_key']
    else:
        if token_path.exists() or keys:
            raise StoreInitializationError('existing_owner_key_requires_review')
        row, raw = sdk.new_key(user)
        pending = {'schema': 1, 'store_id': spec['store_id'], 'owner_id': owner_id,
                   'key_id': str(row.id), 'raw_key': raw}
        write_private(pending_path, json.dumps(pending))
    after_step('pending_key')
    matching = [key for key in keys if str(key.id) == pending['key_id']]
    if matching:
        if len(matching) != 1 or matching[0].api_key != sdk.prepare_key(raw) or str(matching[0].user_id) != owner_id:
            raise StoreInitializationError('owner_key_result_conflicts')
    else:
        if len(keys) >= sdk.max_keys:
            raise StoreInitializationError('owner_key_limit_reached')
        await sdk.commit_key(row)
    after_step('key_committed')
    if token_path.exists() and read_private(token_path).strip() != raw:
        raise StoreInitializationError('existing_owner_token_differs')
    write_private(token_path, raw + '\n')
    after_step('token_saved')
    receipt = {'schema': 1, 'store_id': spec['store_id'], 'owner_id': owner_id,
               'key_id': pending['key_id'], 'source_commit': spec['source_commit'],
               'storage_initialized': True}
    write_private(receipt_path, json.dumps(receipt))
    after_step('identity_saved')
    pending_path.unlink()
    return receipt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument('--config', type=Path)
    mode.add_argument('--check-resources', action='store_true')
    args = parser.parse_args()
    # Python -I omits the script directory. Resolve imports from the verified
    # runtime generation that contains this entry point.
    sys.path.insert(0, str(Path(__file__).resolve().parent))
    if args.check_resources:
        from official_service_dev import COMPONENT_MANIFEST
        json.loads(COMPONENT_MANIFEST.read_text())['service']
        with tempfile.TemporaryDirectory() as root:
            probe = Path(root) / 'private-write-probe'
            write_private(probe, 'memlane-first-store')
            if read_private(probe) != 'memlane-first-store' or stat.S_IMODE(probe.stat().st_mode) != 0o600:
                raise StoreInitializationError('first_store_resource_check_failed')
        return
    spec = json.loads(read_private(args.config))
    result = asyncio.run(initialize(spec))
    print(json.dumps(result))


if __name__ == '__main__':
    main()
