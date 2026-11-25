"""Evidence for the selected local Ollama Schema interface, never model quality.

Only the explicit validation operation calls a model. Startup uses the latest
successful profile-bound receipt. No Cognee/SDK source is patched.
"""
import copy
import hashlib
import json
import secrets
from urllib.parse import urlparse

CONTRACT = 'ollama-native-nonce-v1'


class NativeSchemaUnconfirmed(Exception):
    pass


def local_destination(model, endpoint):
    if not isinstance(model, str) or not model.startswith('ollama_chat/') or not isinstance(endpoint, str):
        return False
    try:
        parsed = urlparse(endpoint)
        parsed.port
        return (parsed.scheme in ('http', 'https') and parsed.hostname in ('127.0.0.1', '::1', 'localhost')
                and not (parsed.username or parsed.password or parsed.query or parsed.fragment)
                and parsed.path in ('', '/'))
    except ValueError:
        return False


def receipt_for(model, endpoint):
    return {'contract': CONTRACT, 'status': 'verified', 'model': model,
            'endpoint_sha256': hashlib.sha256(endpoint.encode()).hexdigest()}


def evidence_matches(evidence, model, endpoint):
    return local_destination(model, endpoint) and evidence == receipt_for(model, endpoint)


async def verify_native_schema(client, complete):
    """The nonce is present only in response_format, not the plain-text prompt."""
    if not local_destination(client.model, client.endpoint):
        raise NativeSchemaUnconfirmed()
    nonce = secrets.token_hex(16)
    schema = {'type': 'object', 'properties': {
        'proof': {'type': 'string', 'enum': [nonce]}, 'absent': {'type': 'null'}},
        'required': ['proof', 'absent'], 'additionalProperties': False}
    response = await complete(model=client.model, api_base=client.endpoint, api_key=client.api_key,
        messages=[{'role': 'user', 'content': 'Return exactly the plain text MEMLANE-SCHEMA-CHECK.'}],
        response_format={'type': 'json_schema', 'json_schema': {
            'name': 'MemlaneNativeSchemaCheck', 'strict': True, 'schema': schema}},
        num_retries=0)
    try:
        choice = response.choices[0]
        if (choice.finish_reason != 'stop' or getattr(choice.message, 'refusal', None)
                or json.loads(choice.message.content) != {'proof': nonce, 'absent': None}):
            raise NativeSchemaUnconfirmed()
    except (ValueError, TypeError, AttributeError, IndexError):
        raise NativeSchemaUnconfirmed() from None
    return receipt_for(client.model, client.endpoint)


def register_verified_schema(evidence, model, endpoint):
    """Call only with live probe evidence or a passed immutable-profile receipt."""
    if not evidence_matches(evidence, model, endpoint):
        raise NativeSchemaUnconfirmed()
    import litellm
    # Runtime metadata lookups may also materialize the unqualified model or
    # either Ollama prefix. Restore the whole selected alias family after a
    # validation, including the SDK caches derived from those entries.
    bare = model.split('/', 1)[1] if model.startswith(('ollama/', 'ollama_chat/')) else model
    aliases = {model, bare, 'ollama/' + bare, 'ollama_chat/' + bare}
    previous = {key: copy.deepcopy(litellm.model_cost[key]) for key in aliases if key in litellm.model_cost}
    metadata = copy.deepcopy(previous.get(model, {}))
    metadata.update(supports_response_schema=True, litellm_provider='ollama_chat', mode='chat')

    def restore():
        for key in aliases:
            if key in previous:
                litellm.model_cost[key] = copy.deepcopy(previous[key])
            else:
                litellm.model_cost.pop(key, None)
        # The fixed SDK explicitly requires invalidation after direct catalog
        # changes; otherwise its normalized lookup can retain the proof.
        from litellm.utils import _invalidate_model_cost_lowercase_map
        _invalidate_model_cost_lowercase_map()

    try:
        litellm.register_model({model: metadata}, persist_across_reloads=False)
    except BaseException:
        restore()
        raise
    return restore
