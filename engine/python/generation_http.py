"""Process-owned HTTP policy for the pinned LiteLLM SDK, not an engine patch.

OpenAI reads the SDK's explicit HTTP sessions. Other native adapters obtain
HTTPHandler instances through its configurable LLM client cache. Configure each
new handler before the SDK can use it, including after eviction/expiry.
"""
from contextlib import asynccontextmanager
from weakref import WeakSet


@asynccontextmanager
async def generation_http_policy():
    import httpx
    import litellm
    from litellm.caching.llm_caching_handler import LLMClientCache
    from litellm.llms.custom_httpx.http_handler import AsyncHTTPHandler, HTTPHandler

    previous = (litellm.aclient_session, litellm.client_session,
                litellm.in_memory_llm_clients_cache, litellm.module_level_client, litellm.module_level_aclient)
    handlers = WeakSet()

    class ClientCache(LLMClientCache):
        def set_cache(self, key, value, **kwargs):
            if isinstance(value, (AsyncHTTPHandler, HTTPHandler)):
                value.follow_redirects = False
                value.client.follow_redirects = False
                handlers.add(value)
            return super().set_cache(key, value, **kwargs)

    async_client = httpx.AsyncClient(follow_redirects=False)
    sync_client = httpx.Client(follow_redirects=False)
    cache = ClientCache()
    # Ollama model metadata uses these handlers directly, outside the cache.
    metadata_sync = HTTPHandler(follow_redirects=False)
    metadata_async = AsyncHTTPHandler(follow_redirects=False)
    litellm.aclient_session, litellm.client_session = async_client, sync_client
    litellm.in_memory_llm_clients_cache = cache
    litellm.module_level_client, litellm.module_level_aclient = metadata_sync, metadata_async
    try:
        yield
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
        metadata_sync.close()
        await metadata_async.close()
