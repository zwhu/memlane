"""Actual HTTP admission must snapshot each Agent's defaults, never saved model IDs."""
import asyncio
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest

from aiohttp.test_utils import TestClient, TestServer
from codex_completion_endpoint import create_app


class DefaultRequestTests(unittest.IsolatedAsyncioTestCase):
    async def test_every_bridge_follows_changed_default_and_freezes_inflight_request(self):
        for kind in ('codex','pi','traex'):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temporary:
                root=Path(temporary);home=root/'agent';home.mkdir()
                config=home/('settings.json' if kind=='pi' else 'traecli.toml' if kind=='traex' else 'config.toml')
                def select(model,effort):
                    if kind=='pi':config.write_text(json.dumps({'defaultModel':model,'defaultProvider':'deepseek','defaultThinkingLevel':effort}))
                    else:config.write_text('model = '+json.dumps(model)+'\nmodel_reasoning_effort = '+json.dumps(effort)+'\n')
                select('native-first','high')
                token=root/'token';token.write_text('x'*40)
                entered=asyncio.Event();release=asyncio.Event();calls=[]
                async def execute(args,*unused):
                    calls.append((args.model,getattr(args,'agent_defaults_snapshot',None)))
                    entered.set();await release.wait();return '{"answer":"ok"}'
                args=SimpleNamespace(model='obsolete-saved-model',bridge_kind=kind,bridge_provider='ollama',
                    token_file=token,auth_home=home,execute_request=execute,max_concurrency=1,max_inflight=2)
                client=TestClient(TestServer(create_app(args)));await client.start_server()
                body={'model':args.model,'messages':[{'role':'user','content':'synthetic'}]}
                try:
                    first=asyncio.create_task(client.post('/v1/chat/completions',json=body,headers={'Authorization':'Bearer '+'x'*40}))
                    await asyncio.wait_for(entered.wait(),2)
                    select('native-second','medium');release.set()
                    response=await first;self.assertEqual(response.status,200)
                    self.assertEqual(calls[0][0],'native-first','saved model overrode the Agent default')
                    self.assertEqual((await response.json())['model'],'native-first')
                    async with client.post('/v1/chat/completions',json=body,headers={'Authorization':'Bearer '+'x'*40}) as response:
                        self.assertEqual(response.status,200)
                        self.assertEqual((await response.json())['model'],'native-second')
                    self.assertEqual([c[0] for c in calls],['native-first','native-second'])
                    self.assertEqual(args.model,'obsolete-saved-model','routing config was mutated in place')
                    self.assertEqual(calls[0][1]['model'],'native-first')
                    self.assertEqual(calls[1][1]['model'],'native-second')
                finally:
                    release.set();await client.close()

    async def test_unreadable_default_does_not_fall_back_to_saved_or_other_agent_model(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary);token=root/'token';token.write_text('x'*40);calls=[]
            async def execute(*args):calls.append(args);return 'must not run'
            args=SimpleNamespace(model='old-fallback',bridge_kind='traex',token_file=token,auth_home=root,
                                 execute_request=execute,max_concurrency=1,max_inflight=1)
            client=TestClient(TestServer(create_app(args)));await client.start_server()
            try:
                async with client.post('/v1/chat/completions',json={'model':args.model,'messages':[{'role':'user','content':'synthetic'}]},headers={'Authorization':'Bearer '+'x'*40}) as response:
                    self.assertEqual(response.status,409)
                    self.assertEqual((await response.json())['error']['code'],'agent_default_model_unavailable')
                self.assertFalse(calls)
            finally:await client.close()


class DefaultMetadataTests(unittest.TestCase):
    def test_selected_profile_and_explicit_effort_are_read_without_sampling_defaults(self):
        from agent_bridge_runtime import read_agent_defaults
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary)
            (root/'config.toml').write_text('model="base"\nprofile="chosen"\n[profiles.chosen]\nmodel="native-selected"\nmodel_reasoning_effort="medium"\n')
            selected=read_agent_defaults('codex',root)
            self.assertEqual(selected,{'model':'native-selected','provider':'openai','reasoning_effort':'medium','source':'agent_config'})
            (root/'traecli.toml').write_text('model="native-traex"\n')
            self.assertEqual(read_agent_defaults('traex',root),{'model':'native-traex','provider':'trae','source':'agent_config'})


    def test_exact_session_overrides_global_model_without_picking_other_threads(self):
        import os
        import sqlite3
        from unittest.mock import patch
        from agent_bridge_runtime import read_agent_defaults
        for kind in ('codex', 'traex'):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                config = root / ('config.toml' if kind == 'codex' else 'traecli.toml')
                config.write_text('model="global"\nmodel_reasoning_effort="high"\nservice_tier="fast"\n')
                database = root if kind == 'codex' else root / 'cli'
                database.mkdir(exist_ok=True)
                with sqlite3.connect(database / 'state_5.sqlite') as connection:
                    connection.execute('CREATE TABLE threads (id TEXT PRIMARY KEY, model TEXT, model_provider TEXT, reasoning_effort TEXT)')
                    connection.executemany('INSERT INTO threads VALUES (?,?,?,?)', [
                        ('selected', 'session-model', 'openai' if kind == 'codex' else 'trae', 'medium'),
                        ('other', 'unrelated-model', 'openai', 'low')])
                key = 'CODEX_THREAD_ID' if kind == 'codex' else 'TRAE_THREAD_ID'
                with patch.dict(os.environ, {key:'selected'}, clear=True):
                    actual = read_agent_defaults(kind, root)
                    self.assertEqual(actual['model'], 'session-model')
                    self.assertEqual(actual['reasoning_effort'], 'medium')
                    self.assertEqual(actual['service_tier'], 'fast')
                    self.assertEqual(actual['source'], 'agent_session')
                    self.assertEqual(actual['session_id'], 'selected')
                with patch.dict(os.environ, {key:'missing'}, clear=True):
                    actual = read_agent_defaults(kind, root)
                    self.assertEqual(actual['model'], 'global')
                    self.assertEqual(actual['source'], 'agent_config')
                    self.assertNotIn('session_id', actual)
