import hashlib
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest

from project_control import Registry
from session_relations import (
    clear_relation_cache,
    resolve_session_relations,
    resolve_single_session_relation,
)


class _DummyOfficial:
    pass


def _agy_sid(iid, cid):
    return 'codex_antigravity-' + hashlib.sha256(f'{iid}\0{cid}'.encode('utf-8')).hexdigest()


def _oc_sid(iid, cid):
    return 'codex_opencode-' + hashlib.sha256(f'{iid}\0{cid}'.encode('utf-8')).hexdigest()


def _pi_sid(iid, cid):
    return 'codex_pi-' + hashlib.sha256(f'{iid}\0{cid}'.encode('utf-8')).hexdigest()


class SessionRelationsTest(unittest.TestCase):
    def setUp(self):
        clear_relation_cache()
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.registry = Registry(self.root / 'registry.json', _DummyOfficial())

    def tearDown(self):
        clear_relation_cache()
        self.registry.close()
        self.tmp.cleanup()

    def test_antigravity_parent_child_qa_and_orphan_isolation(self):
        iid = 'antigravity-test01'
        agy_profile = self.root / '.gemini' / 'config'
        agy_cli = self.root / '.gemini' / 'antigravity-cli'
        agy_cli.mkdir(parents=True)
        db = agy_cli / 'conversation_summaries.db'
        con = sqlite3.connect(db)
        con.executescript('''
            CREATE TABLE conversation_summaries (
                conversation_id TEXT PRIMARY KEY,
                parent_conversation_id TEXT,
                agent_name TEXT,
                nesting_depth INTEGER
            );
            CREATE TABLE conversation_parents (
                conversation_id TEXT PRIMARY KEY,
                parent_id TEXT
            );
        ''')
        con.execute('INSERT INTO conversation_summaries VALUES (?,?,?,?)', ('parent-1', '', '', 0))
        con.execute('INSERT INTO conversation_summaries VALUES (?,?,?,?)', ('foreign-parent', '', '', 0))
        # Subagent with role via conversation_summaries (may have QAs too, not just tools!)
        con.execute('INSERT INTO conversation_summaries VALUES (?,?,?,?)', ('child-qa', 'parent-1', 'research', 1))
        # Subagent linked via conversation_parents fallback
        con.execute('INSERT INTO conversation_summaries VALUES (?,?,?,?)', ('child-tools', '', 'image-generator', 1))
        con.execute('INSERT INTO conversation_parents VALUES (?,?)', ('child-tools', 'parent-1'))
        # Orphan subagent whose parent lives in a DIFFERENT project dataset
        con.execute('INSERT INTO conversation_summaries VALUES (?,?,?,?)', ('child-foreign', 'foreign-parent', 'explorer', 1))
        con.commit()
        con.close()

        self.registry.state['agents'] = {
            iid: {'kind': 'antigravity', 'host_profile': str(agy_profile)},
        }

        s_parent = _agy_sid(iid, 'parent-1')
        s_child_qa = _agy_sid(iid, 'child-qa')
        s_child_tools = _agy_sid(iid, 'child-tools')
        s_child_foreign = _agy_sid(iid, 'child-foreign')
        # Note foreign-parent is intentionally NOT in dataset session_ids!
        rels = resolve_session_relations(
            [s_parent, s_child_qa, s_child_tools, s_child_foreign],
            registry=self.registry,
        )
        self.assertNotIn(s_parent, rels)
        self.assertEqual(
            rels[s_child_qa],
            {'is_subagent': True, 'parent_session_id': s_parent, 'subagent_role': 'research', 'nesting_depth': 1},
        )
        self.assertEqual(
            rels[s_child_tools],
            {'is_subagent': True, 'parent_session_id': s_parent, 'subagent_role': 'image-generator', 'nesting_depth': 1},
        )
        self.assertEqual(
            rels[s_child_foreign],
            {'is_subagent': True, 'subagent_role': 'explorer', 'nesting_depth': 1},
        )

        self_rel, children = resolve_single_session_relation(
            s_parent,
            [s_parent, s_child_qa, s_child_tools, s_child_foreign],
            registry=self.registry,
        )
        self.assertIsNone(self_rel)
        self.assertEqual(set(children), {s_child_qa, s_child_tools})

    def test_codex_spawn_edges_multilevel_ancestor_compression_and_guardian(self):
        codex_dir = self.root / '.codex'
        codex_dir.mkdir(parents=True)
        # state_4 and state_5 -> must pick highest numbered state_5.sqlite
        (codex_dir / 'state_4.sqlite').write_bytes(b'')
        db = codex_dir / 'state_5.sqlite'
        con = sqlite3.connect(db)
        con.executescript('''
            CREATE TABLE thread_spawn_edges (
                parent_thread_id TEXT, child_thread_id TEXT, status TEXT
            );
            CREATE TABLE threads (
                id TEXT PRIMARY KEY,
                source TEXT,
                agent_nickname TEXT,
                agent_role TEXT,
                attached_thread_parent_id TEXT
            );
        ''')
        con.execute('INSERT INTO threads VALUES (?,?,?,?,?)', ('t-root', 'cli', None, None, None))
        # Intermediate mid-1 is NOT captured in Cognee, while root & leaf (depth=2) ARE!
        con.execute('INSERT INTO thread_spawn_edges VALUES (?,?,?)', ('t-root', 't-mid', 'closed'))
        con.execute('INSERT INTO thread_spawn_edges VALUES (?,?,?)', ('t-mid', 't-leaf', 'open'))
        con.execute('INSERT INTO thread_spawn_edges VALUES (?,?,?)', ('t-root', 't-direct', 'closed'))
        con.execute(
            'INSERT INTO threads VALUES (?,?,?,?,?)',
            ('t-mid', json.dumps({'subagent': {'thread_spawn': {'parent_thread_id': 't-root', 'depth': 1, 'agent_role': 'explorer'}}}), 'Scout', 'explorer', None),
        )
        con.execute(
            'INSERT INTO threads VALUES (?,?,?,?,?)',
            ('t-leaf', json.dumps({'subagent': {'thread_spawn': {'parent_thread_id': 't-mid', 'depth': 2, 'agent_role': 'worker'}}}), 'Worker', 'worker', None),
        )
        con.execute(
            'INSERT INTO threads VALUES (?,?,?,?,?)',
            ('t-direct', json.dumps({'subagent': {'thread_spawn': {'parent_thread_id': 't-root', 'depth': 1}}}), 'Hume', None, None),
        )
        con.execute(
            'INSERT INTO threads VALUES (?,?,?,?,?)',
            ('t-guard', json.dumps({'subagent': {'other': 'guardian'}}), None, None, None),
        )
        con.commit()
        con.close()

        self.registry.state['agents'] = {
            'codex-1': {'kind': 'codex', 'host_profile': str(codex_dir)},
        }
        dataset_sids = ['codex_t-root', 'codex_t-leaf', 'codex_t-direct', 'codex_t-guard']
        rels = resolve_session_relations(dataset_sids, registry=self.registry)

        self.assertNotIn('codex_t-root', rels)
        # t-leaf must collapse through missing t-mid directly onto visible ancestor codex_t-root!
        self.assertEqual(rels['codex_t-leaf']['parent_session_id'], 'codex_t-root')
        self.assertEqual(rels['codex_t-leaf']['nesting_depth'], 2)
        self.assertEqual(rels['codex_t-leaf']['subagent_role'], 'worker')
        self.assertEqual(rels['codex_t-direct']['subagent_role'], 'Hume')
        self.assertEqual(rels['codex_t-guard'], {'is_subagent': True, 'subagent_role': 'guardian', 'nesting_depth': 1})

    def test_opencode_and_pi_and_registry_fallback(self):
        oc_iid = 'opencode-inst'
        oc_db = self.root / 'opencode.db'
        con = sqlite3.connect(oc_db)
        con.execute('CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, title TEXT)')
        con.execute('INSERT INTO session VALUES (?,?,?)', ('ses_root', None, 'main'))
        con.execute('INSERT INTO session VALUES (?,?,?)', ('ses_child', 'ses_root', 'helper'))
        con.commit()
        con.close()

        pi_iid = 'pi-inst'
        pi_sessions = self.root / 'pi-agent' / 'sessions' / 'proj'
        pi_sessions.mkdir(parents=True)
        pi_parent_file = pi_sessions / '001.jsonl'
        pi_child_file = pi_sessions / '002.jsonl'
        pi_parent_file.write_text(json.dumps({'type': 'session', 'id': 'pi-root'}) + '\n', encoding='utf-8')
        pi_child_file.write_text(json.dumps({'type': 'session', 'id': 'pi-child', 'parentSession': str(pi_parent_file)}) + '\n', encoding='utf-8')

        self.registry.state['agents'] = {
            oc_iid: {'kind': 'opencode'},
            pi_iid: {'kind': 'pi', 'host_profile': str(self.root / 'pi-agent')},
        }
        self.registry.state['session_relations'] = {
            'codex_custom-child': {
                'is_subagent': True,
                'parent_session_id': 'codex_custom-root',
                'subagent_role': 'custom-agent',
                'nesting_depth': 1,
            }
        }
        sids = [
            _oc_sid(oc_iid, 'ses_root'),
            _oc_sid(oc_iid, 'ses_child'),
            _pi_sid(pi_iid, 'pi-root'),
            _pi_sid(pi_iid, 'pi-child'),
            'codex_custom-root',
            'codex_custom-child',
        ]
        rels = resolve_session_relations(
            sids,
            registry=self.registry,
            codex_config={'opencode_db': str(oc_db)},
        )
        self.assertEqual(rels[_oc_sid(oc_iid, 'ses_child')]['parent_session_id'], _oc_sid(oc_iid, 'ses_root'))
        self.assertEqual(rels[_pi_sid(pi_iid, 'pi-child')]['parent_session_id'], _pi_sid(pi_iid, 'pi-root'))
        self.assertEqual(rels[_pi_sid(pi_iid, 'pi-child')]['subagent_role'], 'branch')
        self.assertEqual(rels['codex_custom-child']['parent_session_id'], 'codex_custom-root')
        self.assertEqual(rels['codex_custom-child']['subagent_role'], 'custom-agent')

    def test_corrupt_or_missing_host_dbs_fail_open_and_cache_invalidates_on_mtime(self):
        codex_dir = self.root / '.codex'
        codex_dir.mkdir(parents=True)
        bad = codex_dir / 'state_1.sqlite'
        bad.write_bytes(b'not a sqlite file')
        self.registry.state['agents'] = {'codex-1': {'kind': 'codex', 'host_profile': str(codex_dir)}}
        self.assertEqual(resolve_session_relations(['codex_abc'], registry=self.registry), {})

        # Valid DB then updated on disk -> cache must refresh when mtime/size changes
        bad.unlink()
        db = codex_dir / 'state_2.sqlite'
        con = sqlite3.connect(db)
        con.execute('CREATE TABLE thread_spawn_edges (parent_thread_id TEXT, child_thread_id TEXT)')
        con.execute('INSERT INTO thread_spawn_edges VALUES (?,?)', ('p1', 'c1'))
        con.commit()
        con.close()
        rels1 = resolve_session_relations(['codex_p1', 'codex_c1', 'codex_c2'], registry=self.registry)
        self.assertIn('codex_c1', rels1)
        self.assertNotIn('codex_c2', rels1)

        con = sqlite3.connect(db)
        con.execute('INSERT INTO thread_spawn_edges VALUES (?,?)', ('p1', 'c2'))
        con.commit()
        con.close()
        # Bump mtime explicitly beyond filesystem granularity if same second
        st = db.stat()
        import os
        os.utime(db, ns=(st.st_atime_ns, st.st_mtime_ns + 5_000_000))
        rels2 = resolve_session_relations(['codex_p1', 'codex_c1', 'codex_c2'], registry=self.registry)
        self.assertIn('codex_c2', rels2)


if __name__ == '__main__':
    unittest.main()
