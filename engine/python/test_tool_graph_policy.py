from pathlib import Path
import tempfile
import unittest

from aiohttp.test_utils import TestClient, TestServer

from project_control import Registry
from project_control_server import create_app
from project_policy import PolicyError
from tool_graph_policy import configure, settings


class ToolGraphPolicyTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.path = Path(self.directory.name) / 'registry.json'
        self.registry = Registry(self.path, None)
        self.registry.state['projects'] = {'a': {'id': 'a', 'name': 'A'}, 'b': {'id': 'b', 'name': 'B'}}
        self.registry.save()

    async def asyncTearDown(self):
        self.registry.close()
        self.directory.cleanup()

    async def test_global_off_project_overrides_and_reset_persist(self):
        self.assertFalse(settings(self.registry.state, 'a')['effective'])
        await configure(self.registry, 0, True, 'a')
        self.assertTrue(settings(self.registry.state, 'a')['effective'])
        self.assertFalse(settings(self.registry.state, 'b')['effective'])
        await configure(self.registry, 1, False, 'b')
        await configure(self.registry, 2, True)
        self.assertTrue(settings(self.registry.state, 'a')['effective'])
        self.assertFalse(settings(self.registry.state, 'b')['effective'])
        await configure(self.registry, 3, None, 'b')
        self.assertTrue(settings(self.registry.state, 'b')['effective'])
        self.assertEqual(settings(self.registry.state, 'b')['source'], 'global')
        self.registry.close()
        self.registry = Registry(self.path, None)
        self.assertTrue(settings(self.registry.state, 'a')['effective'])
        self.assertTrue(settings(self.registry.state, 'b')['effective'])

    async def test_invalid_types_and_stale_writes_do_not_change_settings(self):
        for value in (None, 1, 'true', {}, []):
            with self.assertRaises(PolicyError):
                await configure(self.registry, 0, value)
        await configure(self.registry, 0, True)
        with self.assertRaises(PolicyError):
            await configure(self.registry, 0, False)
        self.assertTrue(settings(self.registry.state)['effective'])

    async def test_management_route_saves_without_starting_models(self):
        client = TestClient(TestServer(create_app(self.registry, 'm' * 40, {})))
        await client.start_server()
        try:
            response = await client.put('/v1/tool-graph/settings', json={'expected_revision': 0, 'enabled': True})
            self.assertEqual(response.status, 401)
            response = await client.put('/v1/projects/a/tool-graph/settings',
                json={'expected_revision': 0, 'enabled': True}, headers={'Authorization': 'Bearer ' + 'm' * 40})
            self.assertEqual(response.status, 200)
            result = await response.json()
            self.assertTrue(next(p for p in result['projects'] if p['id'] == 'a')['tool_graph']['effective'])
            self.assertFalse(result['tool_graph']['effective'])
        finally:
            await client.close()
