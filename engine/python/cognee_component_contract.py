"""Public build provenance; no runtime state or model credentials."""
import json
from pathlib import Path

SERVICE_COMMIT = 'ec6f40859e5f2df9e7862dde27d580ac3268ed43'
PLUGIN_COMMIT = 'a176248914fdb32a78354854b300174203b15a5c'
COMPONENT_VERSION = '1.6.7-memlane.14-coding-ec6f408'


def coding_contract(base):
    path = Path(base) / 'memlane-coding-memory.json'
    if not path.exists():
        return None
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 4096:
        raise ValueError('coding_memory_component_contract_invalid')
    value = json.loads(path.read_text())
    expected = {'protocol': 1, 'service_commit': SERVICE_COMMIT,
        'service_archive_sha256': '6e11047ef690efedac06653c67a11bea0c0e0a8c6af0c89dad634574f4bc01bc',
        'sdk_version': '1.6.7', 'component_version': COMPONENT_VERSION,
        'plugin_commit': PLUGIN_COMMIT, 'plugin_version': '1.8.0'}
    if value != expected:
        raise ValueError('coding_memory_component_contract_invalid')
    return value
