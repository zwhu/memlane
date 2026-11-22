"""Unit tests for local_body_reader safe dir_fd+O_NOFOLLOW fast-path, previews, and JSON budgets."""
import json
from pathlib import Path
import sqlite3
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import uuid

from local_body_reader import (
    LocalPathSecurityError,
    fit_items_to_json_budget,
    read_document_body_with_fallback,
    read_local_file_body,
    read_local_preview_snippet,
    serialized_json_bytes,
    truncate_utf8_runes,
)
from user_profiles.documents import ProfileDocuments


class _FakeStreamContent:
    def __init__(self, payload: bytes):
        self._payload = payload

    async def iter_chunked(self, size: int):
        for idx in range(0, len(self._payload), size):
            yield self._payload[idx : idx + size]


class _FakeHTTPResp:
    def __init__(self, status: int, payload: bytes):
        self.status = status
        self._payload = payload
        self.content = _FakeStreamContent(payload)

    async def read(self):
        return self._payload

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_args):
        return False


class _FakeSession:
    def __init__(self, payload: bytes, status: int = 200, *, authorized_datasets=None, auth_status: int = 200):
        self.payload = payload
        self.status = status
        self.authorized_datasets = authorized_datasets
        self.auth_status = auth_status
        self.calls = []

    def get(self, url, **kwargs):
        self.calls.append(url)
        if url.endswith('/api/v1/datasets'):
            if self.auth_status != 200:
                return _FakeHTTPResp(self.auth_status, b'denied')
            ds_list = [{'id': ds} for ds in (self.authorized_datasets or ())]
            return _FakeHTTPResp(200, json.dumps(ds_list).encode('utf-8'))
        return _FakeHTTPResp(self.status, self.payload)


class LocalBodyReaderTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.official_data = self.root / 'official-store'
        self.data_dir = self.official_data / 'data'
        self.db_dir = self.official_data / 'system' / 'databases'
        self.data_dir.mkdir(parents=True)
        self.db_dir.mkdir(parents=True)

    def tearDown(self):
        self.tmp.cleanup()

    def test_dir_fd_nofollow_reads_valid_and_blocks_symlinks_and_traversals(self):
        sub = self.data_dir / 'nested'
        sub.mkdir()
        doc = sub / 'note.md'
        doc.write_text('你好 Memlane 正文内容', encoding='utf-8')

        # Valid file via file:// URI and plain path
        res1 = read_local_file_body(f'file://{doc}', self.official_data)
        self.assertIsNotNone(res1)
        self.assertEqual(res1['body'], '你好 Memlane 正文内容')
        self.assertFalse(res1['truncated'])
        self.assertEqual(res1['source'], 'local_fastpath')

        res2 = read_local_file_body('nested/note.md', self.official_data)
        self.assertEqual(res2['body'], '你好 Memlane 正文内容')

        # Outside secret file
        secret = self.root / 'outside-secret.txt'
        secret.write_text('TOP_SECRET', encoding='utf-8')

        # 1. Leaf symlink inside data/ -> Fail-Closed 403
        leaf_link = sub / 'escape.md'
        leaf_link.symlink_to(secret)
        with self.assertRaises(LocalPathSecurityError):
            read_local_file_body(str(leaf_link), self.official_data)

        # 2. Intermediate directory symlink inside data/ -> Fail-Closed 403
        outside_dir = self.root / 'outside-dir'
        outside_dir.mkdir()
        (outside_dir / 'leak.md').write_text('LEAK', encoding='utf-8')
        dir_link = self.data_dir / 'dir-escape'
        dir_link.symlink_to(outside_dir)
        with self.assertRaises(LocalPathSecurityError):
            read_local_file_body(str(dir_link / 'leak.md'), self.official_data)

        # 3. Path traversal '..' -> Fail-Closed 403
        with self.assertRaises(LocalPathSecurityError):
            read_local_file_body(f'{self.data_dir}/../outside-secret.txt', self.official_data)
        with self.assertRaises(LocalPathSecurityError):
            read_local_file_body('../outside-secret.txt', self.official_data)

        # 4. Absolute outside path -> Fail-Closed 403
        with self.assertRaises(LocalPathSecurityError):
            read_local_file_body(str(secret), self.official_data)

        # Preview reader safely returns empty on symlink without leaking
        self.assertEqual(read_local_preview_snippet(str(leaf_link), self.official_data), ('', False))

    def test_utf8_truncation_wrapper_strip_and_json_escape_budget(self):
        # Multi-byte Chinese body truncated on byte limit never splits a UTF-8 character
        doc = self.data_dir / 'chinese.md'
        doc.write_text('中' * 100, encoding='utf-8')  # 300 bytes
        res = read_local_file_body(str(doc), self.official_data, max_bytes=10)
        self.assertTrue(res['truncated'])
        self.assertEqual(res['body'], '中' * 3)  # 9 bytes, cleanly trimmed before 4th char
        self.assertEqual(res['bytes_total'], 300)

        # Session learning wrapper stripped in preview snippet
        wrapped = self.data_dir / 'lesson.md'
        wrapped.write_text(
            'Lesson-Type: state\nState-Key: k1\n\n# Session learning (session s-1)\n\n真实记忆正文第一句。' + ('字' * 300),
            encoding='utf-8',
        )
        snip, trunc = read_local_preview_snippet(str(wrapped), self.official_data, max_runes=15, max_utf8_bytes=60)
        self.assertTrue(trunc)
        self.assertTrue(snip.startswith('真实记忆正文第一句。'))
        self.assertLessEqual(len(snip), 15)

        # Rune truncation respects both max_runes and max_utf8_bytes
        s_cut, was_cut = truncate_utf8_runes('测试中文字符串', max_runes=10, max_utf8_bytes=7)
        self.assertTrue(was_cut)
        self.assertEqual(s_cut, '测试')  # 6 bytes <= 7

        # JSON escape expansion (6x for control chars \u0001) enforced on ACTUAL serialized JSON bytes!
        evil_items = [
            {'doc_id': f'd-{i}', 'preview_text': '\x01"\n' * 100, 'preview_truncated': False}
            for i in range(40)
        ]

        def builder(subset, trimmed):
            return {'items': subset, 'has_more': trimmed}

        payload, kept, trimmed = fit_items_to_json_budget(builder, evil_items, max_json_bytes=2048)
        self.assertLessEqual(serialized_json_bytes(payload), 2048)
        self.assertTrue(any(row['preview_truncated'] for row in kept) or trimmed)

    async def test_encryption_and_nonlocal_fallback_and_user_profile_fastpath(self):
        ds_id = str(uuid.uuid4())
        doc_local = str(uuid.uuid4())
        doc_s3 = str(uuid.uuid4())
        doc_profile = str(uuid.uuid4())

        local_file = self.data_dir / 'local.md'
        local_file.write_text('local fastpath body', encoding='utf-8')
        prof_file = self.data_dir / 'profile.md'
        prof_file.write_text('<!-- memlane-user-profile v1 -->\n\n# My Portrait\nFast body!', encoding='utf-8')

        con = sqlite3.connect(self.db_dir / 'cognee_db')
        con.execute('''
            CREATE TABLE data (
                id CHAR(32) PRIMARY KEY, dataset_id CHAR(32), raw_data_location VARCHAR,
                mime_type VARCHAR, content_hash VARCHAR
            )
        ''')
        con.executemany(
            'INSERT INTO data VALUES (?,?,?,?,?)',
            [
                (uuid.UUID(doc_local).hex, uuid.UUID(ds_id).hex, f'file://{local_file}', 'text/markdown', 'h1'),
                (uuid.UUID(doc_s3).hex, uuid.UUID(ds_id).hex, 's3://bucket/remote.md', 'text/markdown', 'h2'),
                (uuid.UUID(doc_profile).hex, uuid.UUID(ds_id).hex, str(prof_file), 'text/markdown', 'h3'),
            ],
        )
        con.commit()
        con.close()

        fake_session = _FakeSession(b'official fallback body', authorized_datasets=[ds_id])
        fake_official = SimpleNamespace(url='http://127.0.0.1:9999', session=fake_session, token_file=None)

        # 1. Local unencrypted -> verifies official access once (cached 2s), ZERO /raw HTTP calls!
        r_local = await read_document_body_with_fallback(self.official_data, ds_id, doc_local, official=fake_official)
        self.assertEqual(r_local['source'], 'local_fastpath')
        self.assertEqual(r_local['body'], 'local fastpath body')
        self.assertEqual([u for u in fake_session.calls if '/raw' in u], [])
        self.assertEqual(len([u for u in fake_session.calls if u.endswith('/api/v1/datasets')]), 1)

        # 2. FILE_ENCRYPTION_ENABLED -> transparent fallback to official /raw
        with patch.dict('os.environ', {'FILE_ENCRYPTION_ENABLED': 'true'}):
            r_enc = await read_document_body_with_fallback(self.official_data, ds_id, doc_local, official=fake_official)
        self.assertEqual(r_enc['source'], 'official_fallback')
        self.assertEqual(r_enc['body'], 'official fallback body')
        self.assertEqual(len([u for u in fake_session.calls if '/raw' in u]), 1)

        # 3. Non-local URI (s3://) -> transparent fallback to official /raw
        r_s3 = await read_document_body_with_fallback(self.official_data, ds_id, doc_s3, official=fake_official)
        self.assertEqual(r_s3['source'], 'official_fallback')
        self.assertEqual(len([u for u in fake_session.calls if '/raw' in u]), 2)

        # 4. ProfileDocuments.read() verifies live auth (no stale cache) + uses local fast-path with ZERO /raw calls
        raw_calls_before = len([u for u in fake_session.calls if '/raw' in u])
        ds_calls_before = len([u for u in fake_session.calls if u.endswith('/api/v1/datasets')])
        profiles_docs = ProfileDocuments(fake_official, official_data=self.official_data)
        portrait = await profiles_docs.read(ds_id, doc_profile)
        self.assertIn('My Portrait', portrait)
        self.assertIn('Fast body!', portrait)
        self.assertEqual(len([u for u in fake_session.calls if '/raw' in u]), raw_calls_before)
        self.assertEqual(len([u for u in fake_session.calls if u.endswith('/api/v1/datasets')]), ds_calls_before + 1)

        # 5. Immediate live revocation (R2-PY-P0-01): removing ds_id from authorized_datasets denies IMMEDIATELY!
        from project_policy import PolicyError
        fake_session.authorized_datasets = []
        with self.assertRaises(PolicyError) as revoked_err:
            await read_document_body_with_fallback(self.official_data, ds_id, doc_local, official=fake_official)
        self.assertEqual(revoked_err.exception.status, 403)

        # 6. official=None fails closed with 503 (never silently allows reads!)
        with self.assertRaises(PolicyError) as none_err:
            await read_document_body_with_fallback(self.official_data, ds_id, doc_local, official=None)
        self.assertEqual(none_err.exception.status, 503)

        # 7. ProfileDocuments.read() /raw fallback (official_data=None) preserves 401 and 403 verbatim!
        tok_file = self.root / 'tok.secret'
        tok_file.write_text('secret-token', encoding='utf-8')
        tok_file.chmod(0o600)
        off_raw_401 = SimpleNamespace(url='http://127.0.0.1:9999', session=_FakeSession(b'', status=401), token_file=tok_file)
        with self.assertRaises(PolicyError) as p_raw_401:
            await ProfileDocuments(off_raw_401, official_data=None).read(ds_id, doc_profile)
        self.assertEqual(p_raw_401.exception.status, 401)
        off_raw_403 = SimpleNamespace(url='http://127.0.0.1:9999', session=_FakeSession(b'', status=403), token_file=tok_file)
        with self.assertRaises(PolicyError) as p_raw_403:
            await ProfileDocuments(off_raw_403, official_data=None).read(ds_id, doc_profile)
        self.assertEqual(p_raw_403.exception.status, 403)

    async def test_official_auth_401_403_fifo_nonblocking_binary_fallback_and_sole_item_budget(self):
        import os
        from local_body_reader import encode_json_utf8, invalidate_official_read_access_cache
        from project_policy import PolicyError

        ds_id = str(uuid.uuid4())
        doc_ok = str(uuid.uuid4())
        doc_bin = str(uuid.uuid4())
        ok_file = self.data_dir / 'ok.md'
        ok_file.write_text('secret content', encoding='utf-8')
        bin_file = self.data_dir / 'binary.bin'
        bin_file.write_bytes(b'\xff\xfe\x00\x01binary')
        fifo_path = self.data_dir / 'trap.fifo'
        os.mkfifo(fifo_path)

        con = sqlite3.connect(self.db_dir / 'cognee_db')
        con.execute('''
            CREATE TABLE data (
                id CHAR(32) PRIMARY KEY, dataset_id CHAR(32), raw_data_location VARCHAR,
                mime_type VARCHAR, content_hash VARCHAR
            )
        ''')
        con.executemany(
            'INSERT INTO data VALUES (?,?,?,?,?)',
            [
                (uuid.UUID(doc_ok).hex, uuid.UUID(ds_id).hex, str(ok_file), 'text/markdown', 'h1'),
                (uuid.UUID(doc_bin).hex, uuid.UUID(ds_id).hex, str(bin_file), 'application/octet-stream', 'h2'),
            ],
        )
        con.commit()
        con.close()

        # 1. FIFO inside data_dir MUST fail-closed non-blockingly (PY-P1-03)
        with self.assertRaises(LocalPathSecurityError) as fifo_err:
            read_local_file_body(str(fifo_path), self.official_data)
        self.assertEqual(fifo_err.exception.status, 403)

        # 2. Official 401 and 403 preserved Fail-Closed (never rewritten to 503, never cached) (PY-P0-05)
        invalidate_official_read_access_cache()
        off_401 = SimpleNamespace(url='http://127.0.0.1:9999', session=_FakeSession(b'', auth_status=401), token_file=None)
        with self.assertRaises(PolicyError) as err401:
            await read_document_body_with_fallback(self.official_data, ds_id, doc_ok, official=off_401)
        self.assertEqual(err401.exception.status, 401)
        with self.assertRaises(PolicyError) as prof401:
            await ProfileDocuments(off_401, official_data=self.official_data).read(ds_id, doc_ok)
        self.assertEqual(prof401.exception.status, 401)

        off_403 = SimpleNamespace(url='http://127.0.0.1:9999', session=_FakeSession(b'', authorized_datasets=[]), token_file=None)
        with self.assertRaises(PolicyError) as err403:
            await read_document_body_with_fallback(self.official_data, ds_id, doc_ok, official=off_403)
        self.assertEqual(err403.exception.status, 403)

        # 3. Non-text/binary local file falls back to bounded official /raw (PY-P1-03)
        off_ok = SimpleNamespace(url='http://127.0.0.1:9999', session=_FakeSession(b'decoded text', authorized_datasets=[ds_id]), token_file=None)
        r_bin = await read_document_body_with_fallback(self.official_data, ds_id, doc_bin, official=off_ok)
        self.assertEqual(r_bin['source'], 'official_fallback')
        self.assertEqual(r_bin['body'], 'decoded text')

        # 4. UTF-8 byte budget & sole-item shrink (never items=[] + has_more=True) (PY-P1-02)
        zh_items = [{'doc_id': 'sole', 'title': '中文标题' * 200, 'preview_text': '中文正文' * 200, 'preview_truncated': False}]
        payload, kept, trimmed = fit_items_to_json_budget(
            lambda sub, tr: {'items': sub, 'has_more': tr},
            zh_items,
            max_json_bytes=512,
        )
        raw_wire = encode_json_utf8(payload)
        self.assertLessEqual(len(raw_wire), 512)
        self.assertEqual(len(kept), 1)
        self.assertFalse(trimmed)
        self.assertNotIn(b'\\u4e2d', raw_wire)

        # 5. finalize_budgeted_payload: anchor re-windowing + re-measurement + outer fixed_trimmer + 500 on impossible budget
        from local_body_reader import finalize_budgeted_payload
        anchor_id = str(uuid.uuid4())
        many_items = [
            {'doc_id': str(uuid.uuid4()), 'title': 'T' * 40, 'preview_text': 'P' * 400, 'preview_truncated': False}
            for _ in range(8)
        ]
        many_items[5]['doc_id'] = anchor_id
        many_items[5]['title'] = 'ANCHOR_ZH_' + ('中文' * 30)
        p_anc, raw_anc = finalize_budgeted_payload(
            lambda sub, lo, hi: {'items': sub, 'lo': lo, 'hi': hi, 'has_prev': lo > 0, 'has_more': hi < len(many_items)},
            many_items,
            max_json_bytes=700,
            anchor_doc_id=anchor_id,
        )
        self.assertLessEqual(len(raw_anc), 700)
        self.assertIn(anchor_id, [it['doc_id'] for it in p_anc['items']])
        self.assertEqual(raw_anc, encode_json_utf8(p_anc))

        # Impossible budget raises 500 response_budget_exceeded instead of emitting over-budget 200
        with self.assertRaises(PolicyError) as over_err:
            finalize_budgeted_payload(
                lambda sub, lo, hi: {'fixed_blob': 'x' * 500, 'items': sub},
                [],
                max_json_bytes=128,
            )
        self.assertEqual(over_err.exception.status, 500)


if __name__ == '__main__':
    unittest.main()
