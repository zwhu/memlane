"""Research only: actual HTTP admission path, synthetic execution, no model/store."""
import asyncio
import json
import tempfile
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch
from aiohttp.test_utils import TestClient, TestServer
import codex_completion_endpoint as endpoint

async def pool_probe():
    import httpx
    from openai import AsyncOpenAI
    with tempfile.TemporaryDirectory() as scratch:
        token = Path(scratch) / 'token'
        token.write_text('synthetic-pool-research-credential')
        args = SimpleNamespace(token_file=token, model='fixture', timeout=.05)
        active = peak = 0
        async def execute(*args):
            nonlocal active, peak
            active += 1
            peak = max(peak, active)
            try:
                await asyncio.sleep(.01)
                return 'synthetic'
            finally:
                active -= 1
        with patch.object(endpoint, 'execute', execute):
            observer = TestClient(TestServer(endpoint.create_app(args), handler_cancellation=True))
            await observer.start_server()
            try:
                async with httpx.AsyncClient(http2=False, limits=httpx.Limits(max_connections=2, max_keepalive_connections=2), timeout=10) as http:
                    sdk = AsyncOpenAI(api_key=token.read_text(), base_url=str(observer.make_url('/v1')), http_client=http, max_retries=0)
                    responses = await asyncio.gather(*(sdk.chat.completions.create(model='fixture', messages=[{'role':'user','content':'synthetic'}]) for _ in range(64)))
                    assert all(r.choices[0].message.content == 'synthetic' for r in responses)
                    response = await observer.get('/health')
                    health = await response.json()
                    assert health['active_or_waiting'] == 0 and peak == 2
                    print(json.dumps({'synthetic':True, 'real_model_calls':0, 'official_openai_sdk':True, 'shared_httpx_pool':2, 'burst':64, 'success':len(responses), 'sdk_retries':0, 'peak_execute':peak, 'final_active_or_waiting':health['active_or_waiting']}))
            finally:
                await observer.close()

async def main():
    with tempfile.TemporaryDirectory() as scratch:
        token = Path(scratch) / 'token'
        token.write_text('synthetic-queue-research-credential')
        args = SimpleNamespace(token_file=token, model='fixture', timeout=.05)
        entered = 0
        peak = 0
        gate = asyncio.Event()
        async def execute(*args):
            nonlocal entered, peak
            entered += 1
            peak = max(peak, entered)
            try:
                await gate.wait()
                return 'synthetic'
            finally:
                entered -= 1
        with patch.object(endpoint, 'execute', execute):
            client = TestClient(TestServer(endpoint.create_app(args), handler_cancellation=True))
            await client.start_server()
            async def health():
                response = await client.get('/health')
                return await response.json()
            async def wait_count(expected):
                for _ in range(200):
                    value = await health()
                    if value['active_or_waiting'] == expected:
                        return value
                    await asyncio.sleep(.005)
                raise AssertionError((expected, value))
            async def post():
                return await client.post('/v1/chat/completions',
                    json={'model':'fixture', 'messages':[{'role':'user','content':'synthetic'}]},
                    headers={'Authorization':'Bearer '+token.read_text()})
            tasks = [asyncio.create_task(post()) for _ in range(32)]
            try:
                await wait_count(32)
                overflow = await post()
                overflow_body = await overflow.json()
                assert overflow.status == 429 and overflow_body['error']['code'] == 'codex_busy'
                await asyncio.sleep(.15)  # three times configured execution timeout
                held = await health()
                assert held['active_or_waiting'] == 32 and entered == 2
                # Disconnect an executing and a waiting request. Production enables
                # handler_cancellation; both must release admission without restart.
                tasks[0].cancel()
                await asyncio.gather(tasks[0], return_exceptions=True)
                await wait_count(31)
                tasks[-1].cancel()
                await asyncio.gather(tasks[-1], return_exceptions=True)
                await wait_count(30)
                gate.set()
                results = await asyncio.gather(*tasks, return_exceptions=True)
                for value in results:
                    if not isinstance(value, BaseException):
                        assert value.status == 200
                        await value.read()
                final = await wait_count(0)
                print(json.dumps({'synthetic':True, 'real_model_calls':0,
                    'burst':32, 'overflow_status':overflow.status, 'overflow_code':overflow_body['error']['code'],
                    'peak_execute':peak, 'admitted_with_synthetic_executor_blocked':held['active_or_waiting'],
                    'active_and_queued_disconnect_released':True, 'final_active_or_waiting':final['active_or_waiting']}))
            finally:
                gate.set()
                for task in tasks:
                    if not task.done(): task.cancel()
                await asyncio.gather(*tasks, return_exceptions=True)
                await client.close()

async def gather_probe():
    gate = asyncio.Event()
    started = asyncio.Event()
    async def sibling():
        started.set()
        await gate.wait()
    task = asyncio.create_task(sibling())
    async def failed():
        await started.wait()
        raise ValueError('synthetic failure')
    try:
        await asyncio.gather(failed(), task)
    except ValueError:
        assert not task.done()
        print(json.dumps({'synthetic':True, 'gather_parent_failed_sibling_still_running':True}))
    finally:
        task.cancel()
        await asyncio.gather(task, return_exceptions=True)

asyncio.run(main())
asyncio.run(pool_probe())
asyncio.run(gather_probe())
