"""Process-owned HTTP policy for the pinned LiteLLM SDK, not an engine patch.

OpenAI reads the SDK's explicit HTTP sessions. Other native adapters obtain
HTTPHandler instances through its configurable LLM client cache. Configure each
new handler before the SDK can use it, including after eviction/expiry.
"""
import asyncio
from contextlib import AsyncExitStack, asynccontextmanager

from .capacity import CapacityAsyncTransport, CapacityTransport, EndpointCapacity


@asynccontextmanager
async def generation_http_policy(*, endpoint=None, max_concurrent_requests=2, wait_timeout=300, backend_kind='unknown', request_rewriter=None, enable_capacity=False):
    import httpx
    import litellm
    from litellm.caching.llm_caching_handler import LLMClientCache
    from litellm.llms.custom_httpx.http_handler import AsyncHTTPHandler, HTTPHandler, _environment_proxy_mounts

    previous = (litellm.aclient_session, litellm.client_session,
                litellm.in_memory_llm_clients_cache, litellm.module_level_client, litellm.module_level_aclient)
    closing_clients = set()
    close_errors = []

    def closed(task):
        closing_clients.discard(task)
        if not task.cancelled() and (error := task.exception()) is not None:
            close_errors.append(error)

    gate = None
    if endpoint or enable_capacity:
        gate = EndpointCapacity(endpoint, max_concurrent_requests, wait_timeout, backend_kind=backend_kind)
    use_transport = gate is not None or request_rewriter is not None

    class ClientCache(LLMClientCache):
        def set_cache(self, key, value, **kwargs):
            if isinstance(value, (AsyncHTTPHandler, HTTPHandler)):
                value.follow_redirects = False
                value.client.follow_redirects = False
                if use_transport:
                    # Public SDK client injection. Sharing these clients also
                    # survives SDK cache eviction. No SDK methods are patched.
                    displaced = value.client
                    shared = async_client if isinstance(value, AsyncHTTPHandler) else sync_client
                    if displaced is not shared:
                        if isinstance(displaced, httpx.AsyncClient):
                            closing = asyncio.create_task(displaced.aclose())
                            closing_clients.add(closing)
                            closing.add_done_callback(closed)

                        else:
                            displaced.close()
                        value.client = shared
                elif isinstance(value, AsyncHTTPHandler):
                    stack.push_async_callback(value.close)
                else:
                    stack.callback(value.close)
            return super().set_cache(key, value, **kwargs)

    async with AsyncExitStack() as stack:
        async_client = await stack.enter_async_context(httpx.AsyncClient(follow_redirects=False,
            **({'transport': CapacityAsyncTransport(gate, request_rewriter=request_rewriter),
                'mounts': _environment_proxy_mounts(lambda proxy: CapacityAsyncTransport(
                    gate, httpx.AsyncHTTPTransport(proxy=proxy), request_rewriter=request_rewriter))}
               if use_transport else {})))
        sync_client = stack.enter_context(httpx.Client(follow_redirects=False,
            **({'transport': CapacityTransport(gate, request_rewriter=request_rewriter),
                'mounts': _environment_proxy_mounts(lambda proxy: CapacityTransport(
                    gate, httpx.HTTPTransport(proxy=proxy), request_rewriter=request_rewriter))}
               if use_transport else {})))
        # Ollama model metadata uses these handlers outside the cache.
        metadata_sync = HTTPHandler(follow_redirects=False)
        stack.callback(metadata_sync.close)
        metadata_async = AsyncHTTPHandler(follow_redirects=False)
        stack.push_async_callback(metadata_async.close)
        litellm.aclient_session, litellm.client_session = async_client, sync_client
        litellm.in_memory_llm_clients_cache = ClientCache()
        litellm.module_level_client, litellm.module_level_aclient = metadata_sync, metadata_async
        try:
            yield gate
        finally:
            (litellm.aclient_session, litellm.client_session, litellm.in_memory_llm_clients_cache,
             litellm.module_level_client, litellm.module_level_aclient) = previous
            if closing_clients:
                await asyncio.gather(*closing_clients)
            if close_errors:
                raise close_errors[0]
