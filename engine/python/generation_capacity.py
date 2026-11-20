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
from performance_trace import RequestTrace, HEADER

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
        self.origin = httpx.URL(endpoint).copy_with(path='/', query=None, fragment=None)
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

    def _reserve(self):
        ticket = _Ticket()
        with self._lock:
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

    async def acquire_async(self, request):
        ticket = self._reserve()
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

    def acquire(self, request):
        ticket = self._reserve()
        try:
            ticket.ready.result(timeout=self.wait_timeout)
            return ticket
        except BaseException as error:
            self.release(ticket)
            if isinstance(error, FutureTimeout):
                raise httpx.PoolTimeout('generation_capacity_wait_timeout', request=request) from None
            raise


class _AsyncBody(httpx.AsyncByteStream):
    def __init__(self, body, gate, ticket, trace=None, status=None):
        self.body, self.gate, self.ticket = body, gate, ticket
        self.closed = False
        self.trace, self.status = trace, status
        self.complete, self.bytes_read, self.error = False, 0, None

    async def __aiter__(self):
        try:
            async for chunk in self.body:
                self.bytes_read += len(chunk)
                yield chunk
            self.complete = True
        except BaseException as error:
            self.error = type(error).__name__
            raise
        finally:
            await self.aclose()

    async def aclose(self):
        if self.closed:
            return
        self.closed = True
        try:
            await self.body.aclose()
        except BaseException as error:
            self.error = type(error).__name__
            raise
        finally:
            self.gate.release(self.ticket)
            if self.trace:
                self.trace.finish(self.error or ('succeeded' if self.complete else 'closed_early'),
                                  status=self.status, output_bytes=self.bytes_read)


class _Body(httpx.SyncByteStream):
    def __init__(self, body, gate, ticket, trace=None, status=None):
        self.body, self.gate, self.ticket = body, gate, ticket
        self.closed = False
        self.trace, self.status = trace, status
        self.complete, self.bytes_read, self.error = False, 0, None

    def __iter__(self):
        try:
            for chunk in self.body:
                self.bytes_read += len(chunk)
                yield chunk
            self.complete = True
        except BaseException as error:
            self.error = type(error).__name__
            raise
        finally:
            self.close()

    def close(self):
        if self.closed:
            return
        self.closed = True
        try:
            self.body.close()
        except BaseException as error:
            self.error = type(error).__name__
            raise
        finally:
            self.gate.release(self.ticket)
            if self.trace:
                self.trace.finish(self.error or ('succeeded' if self.complete else 'closed_early'),
                                  status=self.status, output_bytes=self.bytes_read)


class CapacityAsyncTransport(httpx.AsyncBaseTransport):
    def __init__(self, gate, transport=None, *, request_rewriter=None):
        self.gate = gate
        self.transport = transport or httpx.AsyncHTTPTransport()
        self.request_rewriter = request_rewriter

    async def handle_async_request(self, request):
        if self.request_rewriter is not None:
            request = self.request_rewriter(request)
        if self.gate is None or not self.gate.matches(request):
            return await self.transport.handle_async_request(request)
        trace = RequestTrace('generation_http', backend_kind=self.gate.backend_kind)
        request.headers[HEADER] = trace.request_id
        trace.transition('capacity_queue')
        ticket = None
        try:
            ticket = await self.gate.acquire_async(request)
            trace.transition('dispatch_to_headers')
            response = await self.transport.handle_async_request(request)
            trace.transition('response_body')
            if response.is_stream_consumed:
                self.gate.release(ticket)
                trace.finish('succeeded', status=response.status_code, output_bytes=len(response.content))
            else:
                response.stream = _AsyncBody(response.stream, self.gate, ticket, trace, response.status_code)
            return response
        except BaseException as error:
            if ticket is not None:
                self.gate.release(ticket)
            trace.finish(type(error).__name__)
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
        if self.gate is None or not self.gate.matches(request):
            return self.transport.handle_request(request)
        trace = RequestTrace('generation_http', backend_kind=self.gate.backend_kind)
        request.headers[HEADER] = trace.request_id
        trace.transition('capacity_queue')
        ticket = None
        try:
            ticket = self.gate.acquire(request)
            trace.transition('dispatch_to_headers')
            response = self.transport.handle_request(request)
            trace.transition('response_body')
            if response.is_stream_consumed:
                self.gate.release(ticket)
                trace.finish('succeeded', status=response.status_code, output_bytes=len(response.content))
            else:
                response.stream = _Body(response.stream, self.gate, ticket, trace, response.status_code)
            return response
        except BaseException as error:
            if ticket is not None:
                self.gate.release(ticket)
            trace.finish(type(error).__name__)
            raise

    def close(self):
        self.transport.close()
