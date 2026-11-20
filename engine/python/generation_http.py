"""Process-owned HTTP policy for the pinned LiteLLM SDK, not an engine patch.

OpenAI reads the SDK's explicit HTTP sessions. Other native adapters obtain
HTTPHandler instances through its configurable LLM client cache. Configure each
new handler before the SDK can use it, including after eviction/expiry.
"""
from contextlib import asynccontextmanager
from weakref import WeakSet


@asynccontextmanager
async def generation_http_policy(*, endpoint=None, max_concurrent_requests=2, wait_timeout=300, backend_kind='unknown', request_rewriter=None):
    import httpx
    import litellm
    from litellm.caching.llm_caching_handler import LLMClientCache
    from litellm.llms.custom_httpx.http_handler import AsyncHTTPHandler, HTTPHandler, _environment_proxy_mounts

    previous = (litellm.aclient_session, litellm.client_session,
                litellm.in_memory_llm_clients_cache, litellm.module_level_client, litellm.module_level_aclient)
    handlers = WeakSet()
    closing_clients = set()
    gate = None
    if endpoint:
        from generation_capacity import EndpointCapacity, CapacityAsyncTransport, CapacityTransport
        gate = EndpointCapacity(endpoint, max_concurrent_requests, wait_timeout, backend_kind=backend_kind)

    class ClientCache(LLMClientCache):
        def set_cache(self, key, value, **kwargs):
            if isinstance(value, (AsyncHTTPHandler, HTTPHandler)):
                value.follow_redirects = False
                value.client.follow_redirects = False
                if gate is not None:
                    # Public SDK client injection. Sharing these clients also
                    # survives SDK cache eviction. No SDK methods are patched.
                    displaced = value.client
                    shared = async_client if isinstance(value, AsyncHTTPHandler) else sync_client
                    if displaced is not shared:
                        if isinstance(displaced, httpx.AsyncClient):
                            import asyncio
                            closing = asyncio.create_task(displaced.aclose())
                            closing_clients.add(closing)
                            closing.add_done_callback(closing_clients.discard)
                        else:
                            displaced.close()
                        value.client = shared
                handlers.add(value)
            return super().set_cache(key, value, **kwargs)

    use_transport = gate is not None or request_rewriter is not None
    if use_transport:
        from generation_capacity import CapacityAsyncTransport, CapacityTransport
    async_client = httpx.AsyncClient(follow_redirects=False,
        **({'transport': CapacityAsyncTransport(gate, request_rewriter=request_rewriter),
            'mounts': _environment_proxy_mounts(lambda proxy: CapacityAsyncTransport(
                gate, httpx.AsyncHTTPTransport(proxy=proxy), request_rewriter=request_rewriter))}
           if use_transport else {}))
    sync_client = httpx.Client(follow_redirects=False,
        **({'transport': CapacityTransport(gate, request_rewriter=request_rewriter),
            'mounts': _environment_proxy_mounts(lambda proxy: CapacityTransport(
                gate, httpx.HTTPTransport(proxy=proxy), request_rewriter=request_rewriter))}
           if use_transport else {}))
    cache = ClientCache()
    # Ollama model metadata uses these handlers directly, outside the cache.
    metadata_sync = HTTPHandler(follow_redirects=False)
    metadata_async = AsyncHTTPHandler(follow_redirects=False)
    litellm.aclient_session, litellm.client_session = async_client, sync_client
    litellm.in_memory_llm_clients_cache = cache
    litellm.module_level_client, litellm.module_level_aclient = metadata_sync, metadata_async
    try:
        yield gate
    finally:
        (litellm.aclient_session, litellm.client_session, litellm.in_memory_llm_clients_cache,
         litellm.module_level_client, litellm.module_level_aclient) = previous
        # Only close this scope's sessions. SDK eviction may already have closed
        # a handler; do not access its recreating client property in that case.
        for handler in handlers:
            if isinstance(handler, AsyncHTTPHandler):
                await handler.close()
            else:
                handler.close()
        await async_client.aclose()
        sync_client.close()
        if closing_clients:
            import asyncio
            await asyncio.gather(*closing_clients)
        metadata_sync.close()
        await metadata_async.close()
