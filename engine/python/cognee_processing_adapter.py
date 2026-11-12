"""Memlane's per-context, schema-only Cognee adapter, without model fallbacks.

Installed once in the official service. Other model aliases retain the original
SDK client. Task capability tokens remain in memory and never enter error text.
"""
import json
from urllib.parse import urlsplit

ALIAS = 'openai/memlane-processing'

# Only fixed owner diagnostics cross the SDK's persisted exception boundary.
_HTTP_STATUSES = frozenset((400, 401, 403, 404, 408, 409, 413, 422, 429, 500, 502, 503, 504))
class _SafeAdapterFailure(Exception):
    def __init__(self, code):
        permitted = {'material_model_http_' + str(status) for status in _HTTP_STATUSES}
        permitted.update(('material_model_http_unknown', 'material_model_response_unbounded',
                          'material_model_response_invalid', 'material_model_incomplete'))
        super().__init__(code if code in permitted else 'material_model_unknown')


class ProcessingAdapter:
    model = ALIAS
    max_completion_tokens = 8192

    def __init__(self, endpoint, token):
        parsed = urlsplit(endpoint)
        if parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or not parsed.port or parsed.path != '/v1' or parsed.query or parsed.fragment or parsed.username:
            raise ValueError('private_owner_gateway_required')
        if not token or len(token) > 256 or any(c.isspace() for c in token):
            raise ValueError('private_owner_gateway_token_required')
        self._endpoint, self._token = endpoint, token

    async def acreate_structured_output(self, text_input, system_prompt, response_model, **kwargs):
        # Refuse semantic options rather than silently ignoring caller intent.
        if kwargs or not isinstance(text_input, str) or not isinstance(system_prompt, str):
            raise ValueError('unsupported_material_model_arguments')
        if len((text_input + system_prompt).encode()) > 1024 * 1024:
            raise ValueError('material_prompt_unbounded')
        # The official connection probe and several SDK tasks request str.
        # Keep the private gateway schema-only while returning the SDK's
        # requested string, with no unvalidated plain-completion fallback.
        string_output = response_model is str
        if string_output:
            from pydantic import BaseModel, ConfigDict, StrictStr
            class StringOutput(BaseModel):
                model_config = ConfigDict(extra='forbid')
                text: StrictStr
            response_model = StringOutput
            system_prompt += '\nReturn the requested text in the JSON object field "text".'
        try:
            schema = response_model.model_json_schema()
            schema_size = len(json.dumps(schema).encode())
        except Exception:
            raise ValueError('material_model_request_schema_invalid') from None
        if schema_size > 65536:
            raise ValueError('material_schema_unbounded')
        payload = {'model': 'memlane-processing', 'stream': False,
                   'messages': [{'role': 'system', 'content': system_prompt}, {'role': 'user', 'content': text_input}],
                   'max_completion_tokens': 8192,
                   'response_format': {'type': 'json_schema', 'json_schema': {'name': 'material', 'schema': schema}}}
        raw = json.dumps(payload, ensure_ascii=False).encode()
        if len(raw) > 2 * 1024 * 1024:
            raise ValueError('material_model_request_unbounded')
        stage = "transport"
        try:
            import httpx
            async with httpx.AsyncClient(trust_env=False, follow_redirects=False, timeout=httpx.Timeout(120, connect=5)) as client:
                async with client.stream('POST', self._endpoint + '/chat/completions', content=raw,
                                         headers={'Authorization': 'Bearer ' + self._token, 'Content-Type': 'application/json'}) as response:
                    if response.status_code != 200:
                        raise _SafeAdapterFailure('material_model_http_' + (str(response.status_code) if response.status_code in _HTTP_STATUSES else 'unknown'))
                    stage = 'response_read'
                    data = bytearray()
                    async for chunk in response.aiter_bytes():
                        data.extend(chunk)
                        if len(data) > 2 * 1024 * 1024:
                            raise _SafeAdapterFailure('material_model_response_unbounded')
            stage = 'response_json'
            decoded = json.loads(data)
            stage = 'response_envelope'
            if not isinstance(decoded, dict):
                raise _SafeAdapterFailure('material_model_response_invalid')
            choices = decoded.get('choices', [])
            if not isinstance(choices, list) or len(choices) != 1 or not isinstance(choices[0], dict):
                raise _SafeAdapterFailure('material_model_response_invalid')
            if choices[0].get('finish_reason') != 'stop':
                raise _SafeAdapterFailure('material_model_incomplete')
            message = choices[0].get('message', {})
            if not isinstance(message, dict):
                raise _SafeAdapterFailure('material_model_response_invalid')
            if message.get('tool_calls') or message.get('refusal') or not isinstance(message.get('content'), str):
                raise _SafeAdapterFailure('material_model_response_invalid')
            stage = 'response_schema'
            validated = response_model.model_validate_json(message['content'])
            return validated.text if string_output else validated
        except _SafeAdapterFailure as failure:
            raise ValueError(str(failure)) from None
        except Exception as failure:
            # Official pipeline persists exception reprs. Never let transport
            # request/header/provider details reach that persistence boundary.
            code = {'response_read': 'material_model_response_read_unknown',
                    'response_json': 'material_model_response_json_invalid',
                    'response_envelope': 'material_model_response_invalid',
                    'response_schema': 'material_model_response_schema_invalid'}.get(stage, 'material_model_unknown')
            # Never stringify transport exceptions: they can contain request URLs,
            # headers, provider responses and task credentials.
            if 'httpx' in locals():
                if isinstance(failure, httpx.ConnectTimeout):
                    code = 'material_model_timeout_connect'
                elif isinstance(failure, httpx.ReadTimeout):
                    code = 'material_model_timeout_read'
                elif isinstance(failure, httpx.TimeoutException):
                    code = 'material_model_timeout_unknown'
                elif isinstance(failure, httpx.ConnectError):
                    code = 'material_model_transport_connect'
                elif isinstance(failure, httpx.TransportError):
                    code = 'material_model_transport_unknown'
            raise ValueError(code) from None


def install():
    from cognee.infrastructure.llm.structured_output_framework.litellm_native import get_native_client as sdk
    if getattr(sdk.get_native_client, '_memlane_processing_dispatch', False):
        return
    original = sdk.get_native_client

    def dispatched(raise_api_key_error=True):
        from cognee.infrastructure.llm.config import get_llm_context_config
        config = get_llm_context_config()
        if config.llm_model == ALIAS:
            if config.llm_provider != 'custom':
                raise ValueError('private_owner_gateway_required')
            return ProcessingAdapter(config.llm_endpoint, config.llm_api_key)
        return original(raise_api_key_error=raise_api_key_error)

    dispatched._memlane_processing_dispatch = True
    sdk.get_native_client = dispatched
