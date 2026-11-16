"""Pinned SDK scheduling with simulated inference, without a model or database."""
import asyncio
import importlib
import json
import os
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import httpx
from pydantic import BaseModel

from generation_capacity import EndpointCapacity, CapacityAsyncTransport


class OfficialParallelExtractionTests(unittest.IsolatedAsyncioTestCase):
    async def test_native_gather_shares_two_slots_and_integrates_only_after_extraction(self):
        root = Path(__file__).resolve().parents[2]
        manifest = json.loads((root / 'engine/coding-memory-components.json').read_text())['service']
        source = root / '.cache/coding-memory-components' / ('service-' + manifest['commit'])
        self.assertTrue(source.is_dir(), 'verified pinned SDK source must be prepared')
        home = tempfile.TemporaryDirectory()
        self.addCleanup(home.cleanup)
        sys.path.insert(0, str(source))
        try:
            # Upstream import initializes/rotates logs: never rotate user logs.
            with patch.dict(os.environ, {'HOME': home.name}):
                native = importlib.import_module('cognee.tasks.graph.extract_graph_from_data')
            self.assertTrue(Path(native.__file__).resolve().is_relative_to(source.resolve()))
        finally:
            sys.path.remove(str(source))
        class Graph(BaseModel):
            value: str
        capacity = EndpointCapacity('http://parallel.test', 2)
        active = peak = 0
        completed = []
        async def infer(request):
            nonlocal active, peak
            active += 1
            peak = max(peak, active)
            try:
                await asyncio.sleep(.02)
                return httpx.Response(200, text=request.content.decode())
            finally:
                active -= 1
        async with httpx.AsyncClient(transport=CapacityAsyncTransport(capacity, httpx.MockTransport(infer))) as client:
            async def extract(text, graph_model, **kwargs):
                result = await client.post('http://parallel.test/infer', content=text)
                completed.append(text)
                return graph_model(value=result.text)
            async def integrate(chunks, graphs, *args, **kwargs):
                self.assertEqual(len(completed), len(chunks))
                self.assertEqual([g.value for g in graphs], [c.text for c in chunks])
                self.assertEqual(capacity.active, 0)
                return chunks
            chunks = [SimpleNamespace(text=str(i)) for i in range(4)]
            with patch.object(native, '_temporal_hints_for', return_value=[[]] * 4), \
                    patch.object(native, 'extract_content_graph', extract), \
                    patch.object(native, 'get_configured_ontology_resolver', return_value=None), \
                    patch.object(native, 'get_configured_ontology_mode', return_value=None), \
                    patch.object(native, 'integrate_chunk_graphs', integrate):
                result = await native.extract_graph_from_data(chunks, Graph)
            self.assertEqual(result, chunks)
            self.assertEqual(peak, 2, 'native gather did not run two independent requests')
            self.assertEqual(len(completed), 4, 'effective request count changed')
            self.assertEqual(capacity.active, 0)
