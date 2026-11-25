import asyncio
import json
import unittest

import httpx

from engines.cognee.language_egress import (
    DISTILLATION_PROMPT_SIGNATURES, LANGUAGE_MARKER, current_output_language, inject_language_in_payload,
    is_distillation_prompt, knowledge_language_instructions, rewrite_llm_request, scoped_output_language,
    set_default_language,
)


class LanguageTests(unittest.IsolatedAsyncioTestCase):
    async def test_default_and_concurrent_scopes(self):
        self.addCleanup(set_default_language, 'zh-CN')
        self.assertEqual(current_output_language(), 'zh-CN')
        instruction = knowledge_language_instructions()
        for phrase in ('Simplified Chinese', 'JSON field names', 'enum values', 'state keys',
                       'entity identifiers', 'file paths', 'commands', 'function names and code',
                       'Do not add a translation-only lesson'):
            self.assertIn(phrase, instruction)
        set_default_language('fr')
        async def observe(language):
            with scoped_output_language(language):
                await asyncio.sleep(0)
                return knowledge_language_instructions()
        spanish, passthrough = await asyncio.gather(observe('es'), observe('passthrough'))
        self.assertIn(f'{LANGUAGE_MARKER}es)', spanish)
        self.assertIn('JSON field names', spanish)
        self.assertEqual(passthrough, '')
        self.assertEqual(knowledge_language_instructions('none'), '')
        self.assertEqual(current_output_language(), 'fr')

    def test_matching_and_rewriting_supported_provider_shapes(self):
        prompt = DISTILLATION_PROMPT_SIGNATURES[0]
        for signature in DISTILLATION_PROMPT_SIGNATURES:
            self.assertTrue(is_distillation_prompt(signature))
        self.assertFalse(is_distillation_prompt('Explain the test output'))
        for payload in (
            {'messages': [{'role': 'system', 'content': prompt}]},
            {'system': [{'type': 'text', 'text': prompt}]},
            {'systemInstruction': {'parts': [{'text': prompt}]}},
            {'system_instruction': {'parts': [{'text': prompt}]}},
            {'instructions': prompt, 'input': [{'role': 'developer', 'content': [{'text': prompt}]}]},
        ):
            with self.subTest(shape=list(payload)):
                request = httpx.Request('POST', 'http://localhost/v1', json=payload)
                response = rewrite_llm_request(request)
                rewritten = json.loads(response.content)
                self.assertIn('MEMLANE DEFAULT LANGUAGE (zh-CN)', response.content.decode())
                self.assertEqual(int(response.headers['content-length']), len(response.content))
                self.assertIs(rewrite_llm_request(response), response)
                self.assertEqual(inject_language_in_payload(rewritten), (rewritten, False))
                self.assertNotIn(LANGUAGE_MARKER, request.content.decode())
        request = httpx.Request('POST', 'http://localhost/v1', json={
            'messages': [{'role': 'user', 'content': prompt}]})
        self.assertIs(rewrite_llm_request(request), request)
