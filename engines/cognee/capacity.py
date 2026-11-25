"""Endpoint capacity shared by sync/async HTTP clients, independent of models.

Only transport attempts occupy capacity (including response consumption). SDK
retry/backoff remains outside it. No content, retry queue or engine patch lives
here. One policy owns one gate; multiple processes still require server-side
capacity protection.
"""
import asyncio
from collections import deque
from concurrent.futures import Future, TimeoutError as FutureTimeout
from dataclasses import dataclass, field
import math
import threading
from uuid import uuid4

import httpx


@dataclass(eq=False)
class _Ticket:
    ready: Future = field(default_factory=Future)
    granted: bool = False
    released: bool = False


class EndpointCapacity:
    def __init__(self, endpoint, limit=2, wait_timeout=300, *, backend_kind='unknown'):
        if type(limit) is not int or not 1 <= limit <= 256:
            raise ValueError('invalid_generation_capacity')
        if type(wait_timeout) not in (int, float) or not math.isfinite(wait_timeout) or wait_timeout <= 0:
            raise ValueError('invalid_generation_capacity_timeout')
        self.origin = httpx.URL(endpoint).copy_with(path='/', query=None, fragment=None) if endpoint else None
        self.limit, self.wait_timeout = limit, wait_timeout
        self.backend_kind = backend_kind
        self.active = self.peak = 0
        self._lock, self._waiting = threading.Lock(), deque()

    def matches(self, request):
        return request.url.copy_with(path='/', query=None, fragment=None) == self.origin

    def reconfigure(self, endpoint, limit, backend_kind):
        candidate = EndpointCapacity(endpoint, limit, self.wait_timeout, backend_kind=backend_kind)
        with self._lock:
            if self.active or self._waiting:
                raise RuntimeError('generation_capacity_busy')
            self.origin, self.limit, self.backend_kind = candidate.origin, candidate.limit, candidate.backend_kind

    def _reserve(self, request, matching_only):
        with self._lock:
            if matching_only and not self.matches(request):
                return None
            ticket = _Ticket()
            if self.active < self.limit:
                self.active += 1
                self.peak = max(self.peak, self.active)
                ticket.granted = True
                ticket.ready.set_result(None)
            else:
                self._waiting.append(ticket)
        return ticket

    def release(self, ticket):
        with self._lock:
            if ticket.released:
                return
            ticket.released = True
            if not ticket.granted:
                self._waiting.remove(ticket)
                ticket.ready.cancel()
                return
            self.active -= 1
            if self._waiting:
                successor = self._waiting.popleft()
                successor.granted = True
                self.active += 1
                successor.ready.set_result(None)

    async def acquire_async(self, request, *, matching_only=False):
        ticket = self._reserve(request, matching_only)
        if ticket is None:
            return None
        try:
            # Shield the cross-thread future: cancellation must relinquish a
            # slot even if grant and cancellation happen at the same instant.
            await asyncio.wait_for(asyncio.shield(asyncio.wrap_future(ticket.ready)), self.wait_timeout)
            return ticket
        except BaseException as error:
            self.release(ticket)
            if isinstance(error, TimeoutError):
                raise httpx.PoolTimeout('generation_capacity_wait_timeout', request=request) from None
            raise

    def acquire(self, request, *, matching_only=False):
        ticket = self._reserve(request, matching_only)
        if ticket is None:
            return None
        try:
            ticket.ready.result(timeout=self.wait_timeout)
            return ticket
        except BaseException as error:
            self.release(ticket)
            if isinstance(error, FutureTimeout):
                raise httpx.PoolTimeout('generation_capacity_wait_timeout', request=request) from None
            raise


class _AsyncBody(httpx.AsyncByteStream):
    def __init__(self, body, gate, ticket):
        self.body, self.gate, self.ticket = body, gate, ticket
        self.closed = False

    async def __aiter__(self):
        try:
            async for chunk in self.body:
                yield chunk
        finally:
            await self.aclose()

    async def aclose(self):
        if not self.closed:
            self.closed = True
            try:
                await self.body.aclose()
            finally:
                self.gate.release(self.ticket)


class _Body(httpx.SyncByteStream):
    def __init__(self, body, gate, ticket):
        self.body, self.gate, self.ticket = body, gate, ticket
        self.closed = False

    def __iter__(self):
        try:
            yield from self.body
        finally:
            self.close()

    def close(self):
        if not self.closed:
            self.closed = True
            try:
                self.body.close()
            finally:
                self.gate.release(self.ticket)


class CapacityAsyncTransport(httpx.AsyncBaseTransport):
    def __init__(self, gate, transport=None, *, request_rewriter=None):
        self.gate = gate
        self.transport = transport or httpx.AsyncHTTPTransport()
        self.request_rewriter = request_rewriter

    async def handle_async_request(self, request):
        if self.request_rewriter is not None:
            request = self.request_rewriter(request)
        ticket = await self.gate.acquire_async(request, matching_only=True) if self.gate else None
        if ticket is None:
            return await self.transport.handle_async_request(request)
        try:
            request.headers.setdefault('x-memlane-request-id', uuid4().hex)
            response = await self.transport.handle_async_request(request)
            if response.is_stream_consumed:
                self.gate.release(ticket)
            else:
                response.stream = _AsyncBody(response.stream, self.gate, ticket)
            return response
        except BaseException:
            self.gate.release(ticket)
            raise

    async def aclose(self):
        await self.transport.aclose()


class CapacityTransport(httpx.BaseTransport):
    def __init__(self, gate, transport=None, *, request_rewriter=None):
        self.gate = gate
        self.transport = transport or httpx.HTTPTransport()
        self.request_rewriter = request_rewriter

    def handle_request(self, request):
        if self.request_rewriter is not None:
            request = self.request_rewriter(request)
        ticket = self.gate.acquire(request, matching_only=True) if self.gate else None
        if ticket is None:
            return self.transport.handle_request(request)
        try:
            request.headers.setdefault('x-memlane-request-id', uuid4().hex)
            response = self.transport.handle_request(request)
            if response.is_stream_consumed:
                self.gate.release(ticket)
            else:
                response.stream = _Body(response.stream, self.gate, ticket)
            return response
        except BaseException:
            self.gate.release(ticket)
            raise

    def close(self):
        self.transport.close()
