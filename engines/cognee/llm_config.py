"""Pure, explicit-only configuration at the pinned Cognee LLM boundary."""
import hashlib
import json


def native_llm_kwargs(environment):
    fields = {
        'LLM_PROVIDER': 'llm_provider', 'LLM_MODEL': 'llm_model',
        'LLM_ENDPOINT': 'llm_endpoint', 'LLM_API_KEY': 'llm_api_key',
        'LLM_TEMPERATURE': 'llm_temperature',
        'LLM_MAX_COMPLETION_TOKENS': 'llm_max_completion_tokens',
        'LLM_ARGS': 'llm_args', 'STRUCTURED_OUTPUT_FRAMEWORK': 'structured_output_framework',
    }
    result = {'_env_file': None, '_env_prefix': 'MEMLANE_HANDOFF_ISOLATED_'}
    for key, field in fields.items():
        value = environment.get(key)
        if value is not None:
            result[field] = json.loads(value) if key == 'LLM_ARGS' and isinstance(value, str) else value
    return result

def native_llm_configuration(environment):
    from cognee.infrastructure.llm.config import LLMConfig
    return LLMConfig(**native_llm_kwargs(environment))


def environment_from_generation_config(config):
    explicit_environment = config.get('environment')
    if explicit_environment is not None:
        if not isinstance(explicit_environment, dict):
            raise ValueError('invalid_generation_environment')
        env = dict(explicit_environment)
    else:
        fields = {
            'provider': 'LLM_PROVIDER', 'model': 'LLM_MODEL', 'endpoint': 'LLM_ENDPOINT',
            'api_key': 'LLM_API_KEY', 'temperature': 'LLM_TEMPERATURE',
            'max_output_tokens': 'LLM_MAX_COMPLETION_TOKENS', 'llm_args': 'LLM_ARGS',
            'structured_output_framework': 'STRUCTURED_OUTPUT_FRAMEWORK',
            'ollama_schema_evidence': 'MEMLANE_OLLAMA_SCHEMA_EVIDENCE', 'chunks_per_batch': 'CHUNKS_PER_BATCH',
        }
        env = {key: config[field] for field, key in fields.items() if config.get(field) is not None}
        if config.get('mode') == 'agent_bridge' and 'provider' not in config:
            env['LLM_PROVIDER'] = 'openai'
        tokens = config.get('max_output_tokens')
        if tokens is not None:
            # The native adapter forwards llm_args, not its stored completion cap.
            args = env.get('LLM_ARGS')
            if isinstance(args, str):
                args = json.loads(args)
            env['LLM_ARGS'] = {**(args if args is not None else {}), 'max_tokens': int(tokens)}
    if env.get('LLM_PROVIDER') == 'ollama':
        env.setdefault('LLM_API_KEY', 'ollama')
        if env.get('LLM_ENDPOINT'):
            env.setdefault('OLLAMA_API_BASE', env['LLM_ENDPOINT'])
    return env


def compute_model_digest(config):
    encoded = json.dumps(config, sort_keys=True, ensure_ascii=False, separators=(',', ':'), allow_nan=False)
    return hashlib.sha256(encoded.encode()).hexdigest()
