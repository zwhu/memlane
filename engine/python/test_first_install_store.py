import asyncio
import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from uuid import UUID, uuid4

from first_install_store import initialize, StoreInitializationError, write_private, read_private, configure_environment


class FakeSDK:
    """Protocol fixture only: no Cognee import or real content database."""
    max_keys = 10

    def __init__(self):
        self.user = SimpleNamespace(id=uuid4())
        self.rows = []
        self.generated = self.committed = self.migrated = 0
        self.failed_migrations = []

    async def migrations(self):
        self.migrated += 1
        return self.failed_migrations

    async def default_user(self):
        return self.user

    async def keys(self, user):
        return list(self.rows)

    @staticmethod
    def prepare_key(raw):
        return 'synthetic-hash:' + raw

    def new_key(self, user):
        self.generated += 1
        raw = f'{self.generated:064x}'
        return SimpleNamespace(id=uuid4(), user_id=user.id, api_key=self.prepare_key(raw)), raw

    def restore_key(self, pending):
        return SimpleNamespace(id=UUID(pending['key_id']), user_id=UUID(pending['owner_id']),
                               api_key=self.prepare_key(pending['raw_key']))

    async def commit_key(self, row):
        self.committed += 1
        self.rows.append(row)


class FirstInstallStoreTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.spec = dict(schema=1, store_id='a' * 32, source_commit='e' * 40,
                         model_digest='f' * 64, root=str(self.root))
        marker = {key: self.spec[key] for key in ('schema', 'store_id', 'source_commit', 'model_digest')}
        write_private(self.root / '.memlane-first-store.json', json.dumps(marker))

    def run_initializer(self, sdk, after=None):
        return asyncio.run(initialize(self.spec, sdk=sdk, after_step=after))

    def test_restart_reuses_same_official_owner_and_key(self):
        sdk = FakeSDK()
        first = self.run_initializer(sdk)
        token = read_private(self.root / 'owner.token')
        second = self.run_initializer(sdk)
        self.assertEqual(first, second)
        self.assertEqual((sdk.generated, sdk.committed), (1, 1))
        self.assertEqual(token, read_private(self.root / 'owner.token'))
        self.assertNotIn(token.strip(), json.dumps(second))
        self.assertFalse((self.root / '.owner-key-pending.json').exists())

    def test_every_commit_boundary_resumes_without_duplicate_key(self):
        for stage in ('owner', 'pending_key', 'key_committed', 'token_saved', 'identity_saved'):
            with self.subTest(stage=stage):
                # Fresh synthetic state per boundary, never another real library.
                for name in ('store-identity.json', 'owner.token', '.owner-key-pending.json'):
                    (self.root / name).unlink(missing_ok=True)
                sdk = FakeSDK()

                def interrupt(step):
                    if step == stage:
                        raise RuntimeError('simulated interruption')

                with self.assertRaisesRegex(RuntimeError, 'simulated interruption'):
                    self.run_initializer(sdk, interrupt)
                receipt = self.run_initializer(sdk)
                self.assertTrue(receipt['storage_initialized'])
                self.assertEqual((sdk.generated, sdk.committed), (1, 1))
                self.assertEqual(len(sdk.rows), 1)

    def test_unknown_committed_key_without_pending_material_is_retained(self):
        sdk = FakeSDK()
        row, raw = sdk.new_key(sdk.user)
        sdk.rows.append(row)
        with self.assertRaisesRegex(StoreInitializationError, 'existing_owner_key_requires_review'):
            self.run_initializer(sdk)
        self.assertEqual((sdk.generated, sdk.committed, len(sdk.rows)), (1, 0, 1))
        self.assertFalse((self.root / 'owner.token').exists())

    def test_conflicting_token_is_never_overwritten(self):
        sdk = FakeSDK()
        write_private(self.root / 'owner.token', 'unrelated-token\n')
        with self.assertRaisesRegex(StoreInitializationError, 'existing_owner_key_requires_review'):
            self.run_initializer(sdk)
        self.assertEqual(read_private(self.root / 'owner.token'), 'unrelated-token\n')
        self.assertEqual(sdk.generated, 0)

    def test_failed_migration_never_creates_an_owner_key(self):
        sdk = FakeSDK()
        sdk.failed_migrations = ['synthetic-relational']
        with self.assertRaisesRegex(StoreInitializationError, 'official_store_migration_failed'):
            self.run_initializer(sdk)
        self.assertEqual(sdk.generated, 0)
        self.assertFalse((self.root / 'store-identity.json').exists())

    def test_unknown_or_changed_store_ownership_blocks_sdk_calls(self):
        sdk = FakeSDK()
        self.spec['store_id'] = 'b' * 32
        with self.assertRaisesRegex(StoreInitializationError, 'store_ownership_or_inputs_changed'):
            self.run_initializer(sdk)
        self.assertEqual(sdk.migrated, 0)

    def test_sdk_environment_excludes_host_credentials_and_prepares_owned_paths(self):
        spec = {**self.spec, 'model_cache': str(self.root / 'existing-model')}
        with patch.dict(os.environ, {'PATH': '/usr/bin:/bin', 'OPENAI_API_KEY': 'synthetic-host-secret',
                                     'DEFAULT_USER_PASSWORD': 'synthetic-host-password'}, clear=True):
            configure_environment(spec, self.root)
            self.assertNotIn('OPENAI_API_KEY', os.environ)
            self.assertNotIn('DEFAULT_USER_PASSWORD', os.environ)
            self.assertEqual(os.environ['HF_HUB_OFFLINE'], '1')
            self.assertEqual(Path(os.environ['DB_PATH']), self.root / 'system/databases')
            self.assertTrue(Path(os.environ['DB_PATH']).is_dir())
            self.assertFalse(Path(spec['model_cache']).exists())


if __name__ == '__main__':
    unittest.main()
