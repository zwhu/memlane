"""Installation and project metadata only; collection stays in official hooks."""
import hashlib
import json
import os
from pathlib import Path
import tempfile
import time
import uuid


def _hook_error_state_dir():
    try:
        from _plugin_common import _PLUGIN_DIR
        return Path(_PLUGIN_DIR)
    except Exception:
        marker = Path(__file__).resolve().parents[1] / '.memlane-installation.json'
        binding = json.loads(marker.read_text(encoding='utf-8'))
        instance_id = str(binding['instance_id'])
        return Path.home() / '.cognee-plugin' / ('antigravity-' + instance_id)


def record_hook_error(script, payload, error):
    """Best-effort diagnostic record for silent hook exits; never writes payloads or secrets."""
    try:
        state_dir = _hook_error_state_dir()
        if state_dir.is_symlink():
            return
        state_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chmod(state_dir, 0o700)
        target = state_dir / 'last-hook-error.json'
        if target.is_symlink():
            return
        entries = []
        if target.exists() and target.is_file() and target.stat().st_size <= 16384:
            try:
                loaded = json.loads(target.read_text(encoding='utf-8'))
                if isinstance(loaded, dict) and isinstance(loaded.get('entries'), list):
                    entries = [item for item in loaded['entries'] if isinstance(item, dict)][-4:]
            except (OSError, ValueError, TypeError):
                entries = []
        error_type = type(error).__name__[:80]
        entry = {
            'at': time.time(),
            'script': str(script or '')[:80],
            'error_type': error_type,
            'reason': (str(error)[:120] if error_type == 'NotReady' else error_type),
            'payload_keys': sorted(str(k)[:80] for k in payload.keys())[:32] if isinstance(payload, dict) else [],
        }
        entries.append(entry)
        fd, temporary = tempfile.mkstemp(dir=state_dir, prefix='.hook-error-')
        try:
            os.chmod(temporary, 0o600)
            with os.fdopen(fd, 'w', encoding='utf-8') as stream:
                json.dump({'entries': entries}, stream)
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temporary, target)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
    except Exception:
        pass


def prepare_payload(payload, script):
    from _env_file import load_env_file
    load_env_file()
    from _memlane_policy import bind, NotReady
    from _plugin_common import set_session_key, ensure_launch_record, mark_server_ready
    marker = Path(__file__).resolve().parents[1] / '.memlane-installation.json'
    binding = json.loads(marker.read_text())
    activated_at = binding.get('activated_at', 0)
    if type(activated_at) not in (int, float) or activated_at <= 0:
        raise NotReady('installation_not_activated')
    native = str(uuid.UUID(payload.get('conversationId', payload.get('session_id', ''))))
    transcript = Path(payload.get('transcriptPath', payload.get('transcript_path', '')))
    if (not transcript.is_absolute() or any(p.is_symlink() for p in (transcript, *transcript.parents))
            or native not in transcript.parts or transcript.name not in ('transcript.jsonl', 'transcript_full.jsonl')):
        raise NotReady('native_transcript_identity_required')
    # Only new conversations are admitted. No resumed old conversation or
    # partial old turn is silently imported when the plugin is installed.
    if transcript.exists():
        info = transcript.stat()
        created = getattr(info, 'st_birthtime', info.st_ctime)
        if created < activated_at:
            raise NotReady('existing_conversation_not_captured')
    profile = Path(binding['runtime_profile'])
    os.environ['MEMLANE_ANTIGRAVITY_PROFILE'] = str(profile)
    os.environ['COGNEE_SESSION_PREFIX'] = 'antigravity'
    key = 'antigravity-' + hashlib.sha256((binding['instance_id'] + '\0' + native).encode()).hexdigest()
    set_session_key(key)
    workspace = payload.get('workspacePaths', [])
    cwd = workspace[0] if isinstance(workspace, list) and workspace else payload.get('cwd')
    if not cwd:
        raise NotReady('host_workspace_required')
    dataset = bind(cwd, key)
    session, _ = ensure_launch_record(key, cwd, dataset=dataset)
    from _project_memory import begin, prepare
    begin(dataset, session, cwd)
    prepare(dataset, session)
    mark_server_ready(os.environ['COGNEE_BASE_URL'])
    result = dict(payload)
    result.update(conversationId=key, session_id=key)
    return result
