"""Tests for transport-level LLM egress language policy."""
import json
from pathlib import Path
import sys
import unittest

import httpx
import litellm

sys.path.insert(0, str(Path(__file__).resolve().parent))
from generation_capacity import CapacityAsyncTransport, CapacityTransport
from generation_http import generation_http_policy
from language_egress import LANGUAGE_MARKER, inject_language_in_payload, is_distillation_prompt, rewrite_llm_request
from language_settings import knowledge_language_instructions

ROOT = Path(__file__).resolve().parents[2]
PROMPT_DIR = ROOT / '.cache/coding-memory-components/service-ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/infrastructure/llm/prompts'
PROMPT_FILES = (
    'coding/agent_context_extraction_system.txt',
    'coding/session_distillation_curator_system.txt',
    'coding/session_distillation_writer_system.txt',
    'coding/session_distillation_merge_system.txt',
    'session_distillation_curator_system.txt',
    'session_distillation_writer_system.txt',
)


class _RecordingSyncTransport(httpx.BaseTransport):
    def __init__(self):
        self.requests = []

    def handle_request(self, request):
        self.requests.append(request)
        return httpx.Response(200, json={'ok': True})


class _RecordingAsyncTransport(httpx.AsyncBaseTransport):
    def __init__(self):
        self.requests = []

    async def handle_async_request(self, request):
        self.requests.append(request)
        return httpx.Response(200, json={'ok': True})


class LanguageEgressTests(unittest.IsolatedAsyncioTestCase):
    def test_matches_all_six_official_distillation_prompts(self):
        if not PROMPT_DIR.is_dir():
            self.skipTest('cached official service prompts not present')
        for filename in PROMPT_FILES:
            text = (PROMPT_DIR / filename).read_text()
            self.assertTrue(is_distillation_prompt(text), filename)

    def test_wire_formats_and_idempotency(self):
        sample = 'You are reading one slice of a finished coding session in a software repository.\nTask details.'
        expected = sample + knowledge_language_instructions()

        # OpenAI messages (string + block list)
        openai_payload = {'messages': [{'role': 'system', 'content': sample}, {'role': 'user', 'content': 'hi'}]}
        out, changed = inject_language_in_payload(openai_payload)
        self.assertTrue(changed)
        self.assertEqual(out['messages'][0]['content'], expected)
        out2, changed2 = inject_language_in_payload(out)
        self.assertFalse(changed2)
        self.assertEqual(out2['messages'][0]['content'], expected)

        # OpenAI developer / responses instructions
        resp_payload = {'instructions': sample, 'input': [{'role': 'developer', 'content': [{'type': 'text', 'text': sample}]}]}
        out_r, changed_r = inject_language_in_payload(resp_payload)
        self.assertTrue(changed_r)
        self.assertEqual(out_r['instructions'], expected)
        self.assertEqual(out_r['input'][0]['content'][0]['text'], expected)

        # Anthropic top-level system (str and block list)
        anth_payload = {'system': [{'type': 'text', 'text': sample}], 'messages': [{'role': 'user', 'content': 'hi'}]}
        out_a, changed_a = inject_language_in_payload(anth_payload)
        self.assertTrue(changed_a)
        self.assertEqual(out_a['system'][0]['text'], expected)

        # Gemini systemInstruction
        gem_payload = {'systemInstruction': {'parts': [{'text': sample}]}}
        out_g, changed_g = inject_language_in_payload(gem_payload)
        self.assertTrue(changed_g)
        self.assertEqual(out_g['systemInstruction']['parts'][0]['text'], expected)

        # User content or non-distillation system prompt must never be altered
        untouched = {
            'messages': [
                {'role': 'system', 'content': 'Answer the search question concisely.'},
                {'role': 'user', 'content': sample},
            ]
        }
        out_u, changed_u = inject_language_in_payload(untouched)
        self.assertFalse(changed_u)
        self.assertEqual(out_u, untouched)

    async def test_capacity_transports_and_policy_wire_litellm_clients(self):
        sample = 'You receive a numbered list of lessons proposed from one coding session.'
        sync_rec = _RecordingSyncTransport()
        sync_client = httpx.Client(transport=CapacityTransport(None, sync_rec, request_rewriter=rewrite_llm_request))
        sync_client.post('http://127.0.0.1:9999/v1/chat/completions', json={
            'messages': [{'role': 'system', 'content': sample}, {'role': 'user', 'content': 'u'}],
        })
        sent_body = json.loads(sync_rec.requests[0].content)
        self.assertIn(LANGUAGE_MARKER, sent_body['messages'][0]['content'])
        self.assertEqual(int(sync_rec.requests[0].headers['content-length']), len(sync_rec.requests[0].content))
        sync_client.close()

        async_rec = _RecordingAsyncTransport()
        async_client = httpx.AsyncClient(transport=CapacityAsyncTransport(None, async_rec, request_rewriter=rewrite_llm_request))
        await async_client.post('http://127.0.0.1:9999/v1/messages', json={'system': sample, 'messages': []})
        sent_async = json.loads(async_rec.requests[0].content)
        self.assertIn(LANGUAGE_MARKER, sent_async['system'])
        await async_client.aclose()

        async with generation_http_policy(endpoint=None, request_rewriter=rewrite_llm_request):
            self.assertIsInstance(litellm.aclient_session._transport, CapacityAsyncTransport)
            self.assertIsInstance(litellm.client_session._transport, CapacityTransport)
            self.assertIs(litellm.aclient_session._transport.request_rewriter, rewrite_llm_request)


if __name__ == '__main__':
    unittest.main()
