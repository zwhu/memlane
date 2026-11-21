"""Unit tests for generation_http ClientCache request_rewriter injection."""
import unittest

import litellm
from litellm.llms.custom_httpx.http_handler import AsyncHTTPHandler, HTTPHandler

from generation_capacity import CapacityAsyncTransport, CapacityTransport
from generation_http import generation_http_policy
from language_egress import rewrite_llm_request


class GenerationHttpPolicyTests(unittest.IsolatedAsyncioTestCase):
    async def test_client_cache_injects_rewriter_transport_even_without_endpoint_gate(self):
        async with generation_http_policy(endpoint=None, request_rewriter=rewrite_llm_request) as gate:
            self.assertIsNone(gate)
            async_handler = AsyncHTTPHandler()
            sync_handler = HTTPHandler()
            litellm.in_memory_llm_clients_cache.set_cache('anthropic-async', async_handler)
            litellm.in_memory_llm_clients_cache.set_cache('anthropic-sync', sync_handler)
            self.assertIs(async_handler.client, litellm.aclient_session)
            self.assertIs(sync_handler.client, litellm.client_session)
            self.assertIsInstance(async_handler.client._transport, CapacityAsyncTransport)
            self.assertIsInstance(sync_handler.client._transport, CapacityTransport)
            self.assertIs(async_handler.client._transport.request_rewriter, rewrite_llm_request)
            self.assertIs(sync_handler.client._transport.request_rewriter, rewrite_llm_request)


if __name__ == '__main__':
    unittest.main()
