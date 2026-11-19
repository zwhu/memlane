"""Installation and project metadata only; collection stays in official hooks."""
import hashlib
import json
import os
from pathlib import Path
import uuid


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
            or native not in transcript.parts or transcript.name != 'transcript.jsonl'):
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
    os.environ['COGNEE_SESSION_PREFIX'] = 'codex'
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
