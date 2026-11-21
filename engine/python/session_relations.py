"""Read-only external subagent & parent-child session relation resolver.

Reads host-native read-only SQLite databases / session headers (Antigravity,
Codex, OpenCode, pi) and external persisted registry hints without touching
upstream Cognee sources or opening any host database for writing.
"""
import hashlib
import json
from pathlib import Path
import re
import sqlite3
import threading

from _runtime_common import parse_json as _parse_json

_CACHE_LOCK = threading.Lock()
_HOST_CACHE = {}
_MAX_CACHE_ENTRIES = 32
_MAX_ANCESTOR_HOPS = 16
_MAX_PI_FILES = 2000
_PI_HEADER_BYTES = 4096
_CODEX_STATE_RE = re.compile(r'^state_(\d+)\.sqlite$')
_CONTROL_CHARS_RE = re.compile(r'[\x00-\x1f\x7f]+')


def _clean_role(value):
    if not isinstance(value, str):
        return ''
    text = _CONTROL_CHARS_RE.sub(' ', value).strip()
    if len(text) > 64:
        text = text[:64].strip()
    return text


def _stat_signature(path: Path):
    parts = []
    for candidate in (path, path.parent / (path.name + '-wal')):
        try:
            st = candidate.stat()
            parts.append((candidate.name, st.st_mtime_ns, st.st_size))
        except OSError:
            parts.append((candidate.name, 0, 0))
    return tuple(parts)


def _open_readonly_sqlite(path: Path):
    if not path or not path.is_file():
        return None
    for uri in (f'file:{path}?mode=ro', f'file:{path}?mode=ro&immutable=1'):
        try:
            return sqlite3.connect(uri, uri=True, timeout=0.5)
        except sqlite3.OperationalError:
            continue
        except (sqlite3.Error, OSError, ValueError, TypeError):
            return None
    return None


def _table_columns(con, table_name):
    rows = con.execute(
        "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1",
        (table_name,),
    ).fetchone()
    if not rows:
        return set()
    return {
        str(col[1])
        for col in con.execute(f'PRAGMA table_info({table_name})').fetchall()
        if len(col) > 1
    }


def _cache_get(key):
    with _CACHE_LOCK:
        return _HOST_CACHE.get(key)


def _cache_set(key, value):
    with _CACHE_LOCK:
        if key not in _HOST_CACHE and len(_HOST_CACHE) >= _MAX_CACHE_ENTRIES:
            oldest = next(iter(_HOST_CACHE))
            _HOST_CACHE.pop(oldest, None)
        _HOST_CACHE[key] = value


def clear_relation_cache():
    with _CACHE_LOCK:
        _HOST_CACHE.clear()


def _registry_agents(registry):
    state = getattr(registry, 'state', None) if registry is not None else None
    if not isinstance(state, dict):
        return {}, {}, {}
    agents = state.get('agents')
    host_targets = state.get('host_targets')
    persisted = state.get('session_relations')
    return (
        agents if isinstance(agents, dict) else {},
        host_targets if isinstance(host_targets, dict) else {},
        persisted if isinstance(persisted, dict) else {},
    )


def _agent_kind(agent_row):
    if not isinstance(agent_row, dict):
        return ''
    kind = str(agent_row.get('kind') or '').strip().lower()
    if kind:
        return kind
    inst = agent_row.get('installation')
    if isinstance(inst, dict):
        return str(inst.get('host_kind') or '').strip().lower()
    return ''


def _collect_profiles(kind, agents, host_targets, cfg):
    profiles = []
    seen = set()

    def add(raw):
        if not isinstance(raw, (str, Path)):
            return
        text = str(raw).strip()
        if not text:
            return
        p = Path(text).expanduser()
        key = str(p)
        if key not in seen:
            seen.add(key)
            profiles.append(p)

    agent_hosts = cfg.get('agent_hosts') if isinstance(cfg.get('agent_hosts'), dict) else {}
    if isinstance(agent_hosts.get(kind), dict):
        add(agent_hosts[kind].get('profile'))
    add(host_targets.get(kind))
    for row in agents.values():
        if _agent_kind(row) != kind:
            continue
        add(row.get('host_profile'))
        add(row.get('profile'))
        inst = row.get('installation')
        if isinstance(inst, dict):
            add(inst.get('home'))
            add(inst.get('profile'))
    if kind == 'codex':
        add(cfg.get('auth_home'))
    return profiles


def _collect_instance_ids(kind, agents, cfg):
    ids = []
    seen = set()

    def add(raw):
        if isinstance(raw, str):
            val = raw.strip()
            if val and val not in seen:
                seen.add(val)
                ids.append(val)

    extra_key = f'{kind}_instance_ids'
    extra = cfg.get(extra_key)
    if isinstance(extra, (list, tuple, set)):
        for item in extra:
            add(item)
    for aid, row in agents.items():
        if _agent_kind(row) != kind:
            continue
        add(aid)
        inst = row.get('installation')
        if isinstance(inst, dict):
            add(inst.get('instance_id'))
    return tuple(sorted(ids))


def _load_antigravity_map(db_path: Path, instance_ids: tuple[str, ...]):
    sig = _stat_signature(db_path)
    cache_key = ('antigravity', str(db_path), sig, instance_ids)
    cached = _cache_get(cache_key)
    if cached is not None:
        return cached

    con = _open_readonly_sqlite(db_path)
    if con is None:
        return None
    try:
        sum_cols = _table_columns(con, 'conversation_summaries')
        par_cols = _table_columns(con, 'conversation_parents')
        raw_rows = {}
        if 'conversation_id' in sum_cols:
            select_cols = ['conversation_id']
            for optional in ('parent_conversation_id', 'agent_name', 'nesting_depth'):
                select_cols.append(optional if optional in sum_cols else f'NULL AS {optional}')
            for cid, pid, agent_name, depth in con.execute(
                f"SELECT {', '.join(select_cols)} FROM conversation_summaries"
            ).fetchall():
                c_text = str(cid or '').strip()
                if not c_text:
                    continue
                try:
                    depth_int = int(depth) if depth is not None else 0
                except (TypeError, ValueError):
                    depth_int = 0
                raw_rows[c_text] = {
                    'parent': str(pid or '').strip(),
                    'role': _clean_role(agent_name),
                    'depth': max(0, depth_int),
                }
        if 'conversation_id' in par_cols and 'parent_id' in par_cols:
            for cid, pid in con.execute(
                'SELECT conversation_id, parent_id FROM conversation_parents'
            ).fetchall():
                c_text = str(cid or '').strip()
                p_text = str(pid or '').strip()
                if not c_text or not p_text:
                    continue
                entry = raw_rows.setdefault(c_text, {'parent': '', 'role': '', 'depth': 0})
                if not entry['parent']:
                    entry['parent'] = p_text
    except (sqlite3.Error, OSError, ValueError, TypeError):
        return None
    finally:
        con.close()

    parent_map = {}
    info_map = {}

    def record_pair(child_sid, parent_sid, role, depth, is_sub):
        if parent_sid and child_sid not in parent_map:
            parent_map[child_sid] = parent_sid
        if not is_sub:
            return
        cur = info_map.get(child_sid)
        if cur is None:
            info_map[child_sid] = {'role': role, 'depth': depth}
        else:
            if not cur['role'] and role:
                cur['role'] = role
            if depth > cur['depth']:
                cur['depth'] = depth

    for cid, meta in raw_rows.items():
        pid = meta['parent']
        role = meta['role']
        depth = meta['depth']
        if depth <= 0 and pid:
            depth = 1
        is_sub = bool(pid or depth > 0 or role)
        for iid in instance_ids:
            ch = hashlib.sha256(f'{iid}\0{cid}'.encode('utf-8')).hexdigest()
            ph = hashlib.sha256(f'{iid}\0{pid}'.encode('utf-8')).hexdigest() if pid else ''
            for prefix in ('antigravity-', 'codex_antigravity-'):
                csid = prefix + ch
                psid = (prefix + ph) if ph else ''
                record_pair(csid, psid, role, depth, is_sub)
        if is_sub or pid:
            for prefix in ('antigravity-', 'codex_antigravity-', ''):
                csid = prefix + cid
                psid = (prefix + pid) if pid else ''
                record_pair(csid, psid, role, depth, is_sub)

    result = (parent_map, info_map)
    _cache_set(cache_key, result)
    return result


def _latest_codex_state_db(profile: Path):
    if not profile.is_dir():
        return None
    best = None
    best_score = None
    try:
        for candidate in profile.iterdir():
            if not candidate.is_file():
                continue
            m = _CODEX_STATE_RE.match(candidate.name)
            if not m:
                continue
            try:
                mtime = candidate.stat().st_mtime_ns
            except OSError:
                mtime = 0
            score = (int(m.group(1)), mtime)
            if best_score is None or score > best_score:
                best_score = score
                best = candidate
    except OSError:
        return None
    return best


def _load_codex_map(db_path: Path):
    sig = _stat_signature(db_path)
    cache_key = ('codex', str(db_path), sig)
    cached = _cache_get(cache_key)
    if cached is not None:
        return cached

    con = _open_readonly_sqlite(db_path)
    if con is None:
        return None
    try:
        edge_cols = _table_columns(con, 'thread_spawn_edges')
        thread_cols = _table_columns(con, 'threads')
        edges = {}
        if 'child_thread_id' in edge_cols and 'parent_thread_id' in edge_cols:
            for cid, pid in con.execute(
                'SELECT child_thread_id, parent_thread_id FROM thread_spawn_edges'
            ).fetchall():
                c_text = str(cid or '').strip()
                p_text = str(pid or '').strip()
                if c_text and p_text:
                    edges[c_text] = p_text

        threads_meta = {}
        if 'id' in thread_cols:
            want = ('id', 'source', 'agent_nickname', 'agent_role', 'attached_thread_parent_id')
            select_cols = [col if col in thread_cols else f'NULL AS {col}' for col in want]
            for tid, source_raw, nickname, role_col, attached_pid in con.execute(
                f"SELECT {', '.join(select_cols)} FROM threads"
            ).fetchall():
                t_text = str(tid or '').strip()
                if not t_text:
                    continue
                src_obj = _parse_json(source_raw) if isinstance(source_raw, str) and '{' in source_raw else source_raw
                subagent = src_obj.get('subagent') if isinstance(src_obj, dict) else None
                spawn = subagent.get('thread_spawn') if isinstance(subagent, dict) and isinstance(subagent.get('thread_spawn'), dict) else {}
                other = subagent.get('other') if isinstance(subagent, dict) and isinstance(subagent.get('other'), str) else ''

                pid = (
                    edges.get(t_text)
                    or str(spawn.get('parent_thread_id') or '').strip()
                    or str(attached_pid or '').strip()
                )
                role = ''
                for candidate_role in (role_col, spawn.get('agent_role'), nickname, spawn.get('agent_nickname'), other):
                    role = _clean_role(candidate_role)
                    if role:
                        break
                try:
                    depth = int(spawn.get('depth')) if spawn.get('depth') is not None else 0
                except (TypeError, ValueError):
                    depth = 0
                if depth <= 0 and (pid or isinstance(subagent, dict)):
                    depth = 1
                is_sub = bool(pid or isinstance(subagent, dict) or role or depth > 0)
                if is_sub or pid:
                    threads_meta[t_text] = {
                        'parent': pid,
                        'role': role,
                        'depth': max(0, depth),
                        'is_sub': is_sub,
                    }

        for cid, pid in edges.items():
            entry = threads_meta.setdefault(cid, {'parent': pid, 'role': '', 'depth': 1, 'is_sub': True})
            if not entry['parent']:
                entry['parent'] = pid
            if entry['depth'] <= 0:
                entry['depth'] = 1
            entry['is_sub'] = True
    except (sqlite3.Error, OSError, ValueError, TypeError):
        return None
    finally:
        con.close()

    parent_map = {}
    info_map = {}
    for tid, meta in threads_meta.items():
        pid = meta['parent']
        role = meta['role']
        depth = meta['depth']
        is_sub = meta['is_sub']
        for prefix in ('codex_', ''):
            csid = prefix + tid
            psid = (prefix + pid) if pid else ''
            if psid and csid not in parent_map:
                parent_map[csid] = psid
            if is_sub:
                info_map.setdefault(csid, {'role': role, 'depth': depth})

    result = (parent_map, info_map)
    _cache_set(cache_key, result)
    return result


def _load_opencode_map(db_path: Path, instance_ids: tuple[str, ...]):
    sig = _stat_signature(db_path)
    cache_key = ('opencode', str(db_path), sig, instance_ids)
    cached = _cache_get(cache_key)
    if cached is not None:
        return cached

    con = _open_readonly_sqlite(db_path)
    if con is None:
        return None
    try:
        cols = _table_columns(con, 'session')
        if 'id' not in cols or 'parent_id' not in cols:
            return None
        pairs = []
        for cid, pid in con.execute(
            "SELECT id, parent_id FROM session WHERE parent_id IS NOT NULL AND TRIM(parent_id) != ''"
        ).fetchall():
            c_text = str(cid or '').strip()
            p_text = str(pid or '').strip()
            if c_text and p_text and c_text != p_text:
                pairs.append((c_text, p_text))
    except (sqlite3.Error, OSError, ValueError, TypeError):
        return None
    finally:
        con.close()

    parent_map = {}
    info_map = {}
    for cid, pid in pairs:
        for iid in instance_ids:
            ch = hashlib.sha256(f'{iid}\0{cid}'.encode('utf-8')).hexdigest()
            ph = hashlib.sha256(f'{iid}\0{pid}'.encode('utf-8')).hexdigest()
            for prefix in ('opencode-', 'codex_opencode-'):
                csid = prefix + ch
                psid = prefix + ph
                parent_map.setdefault(csid, psid)
                info_map.setdefault(csid, {'role': 'subagent', 'depth': 1})
        parent_map.setdefault(cid, pid)
        info_map.setdefault(cid, {'role': 'subagent', 'depth': 1})

    result = (parent_map, info_map)
    _cache_set(cache_key, result)
    return result


def _load_pi_map(sessions_dir: Path, instance_ids: tuple[str, ...]):
    if not sessions_dir.is_dir():
        return None
    files = []
    max_mtime = 0
    try:
        for path in sessions_dir.rglob('*.jsonl'):
            if not path.is_file():
                continue
            files.append(path)
            try:
                mt = path.stat().st_mtime_ns
                if mt > max_mtime:
                    max_mtime = mt
            except OSError:
                pass
            if len(files) >= _MAX_PI_FILES:
                break
    except OSError:
        return None

    sig = (len(files), max_mtime)
    cache_key = ('pi', str(sessions_dir), sig, instance_ids)
    cached = _cache_get(cache_key)
    if cached is not None:
        return cached

    path_to_id = {}
    raw_pairs = []
    for path in files:
        try:
            with path.open('r', encoding='utf-8', errors='replace') as fh:
                first_line = fh.readline(_PI_HEADER_BYTES).strip()
        except OSError:
            continue
        if not first_line or not first_line.startswith('{'):
            continue
        header = _parse_json(first_line)
        if not isinstance(header, dict):
            continue
        sid = str(header.get('id') or '').strip()
        if not sid:
            continue
        try:
            resolved = str(path.resolve())
        except OSError:
            resolved = str(path)
        path_to_id[str(path)] = sid
        path_to_id[resolved] = sid
        path_to_id[path.name] = sid
        parent_raw = str(header.get('parentSession') or '').strip()
        if parent_raw:
            raw_pairs.append((sid, parent_raw))

    parent_map = {}
    info_map = {}
    for cid, parent_raw in raw_pairs:
        pid = path_to_id.get(parent_raw)
        if not pid and (parent_raw.endswith('.jsonl') or '/' in parent_raw or '\\' in parent_raw):
            pid = path_to_id.get(Path(parent_raw).name) or ''
        if not pid and '/' not in parent_raw and '\\' not in parent_raw:
            pid = parent_raw
        if not pid or pid == cid:
            continue
        for iid in instance_ids:
            ch = hashlib.sha256(f'{iid}\0{cid}'.encode('utf-8')).hexdigest()
            ph = hashlib.sha256(f'{iid}\0{pid}'.encode('utf-8')).hexdigest()
            for prefix in ('pi-', 'codex_pi-'):
                csid = prefix + ch
                psid = prefix + ph
                parent_map.setdefault(csid, psid)
                info_map.setdefault(csid, {'role': 'branch', 'depth': 1})
        parent_map.setdefault(cid, pid)
        info_map.setdefault(cid, {'role': 'branch', 'depth': 1})

    result = (parent_map, info_map)
    _cache_set(cache_key, result)
    return result


def _merge_loaded(loaded, parent_map, info_map):
    if not loaded:
        return
    p_map, i_map = loaded
    for k, v in p_map.items():
        if v and k not in parent_map:
            parent_map[k] = v
    for k, v in i_map.items():
        existing = info_map.get(k)
        if existing is None:
            info_map[k] = dict(v)
        else:
            if not existing.get('role') and v.get('role'):
                existing['role'] = v['role']
            if v.get('depth', 0) > existing.get('depth', 0):
                existing['depth'] = v['depth']


def _load_all_host_relations(sid_set, registry=None, codex_config=None):
    cfg = codex_config if isinstance(codex_config, dict) else {}
    agents, host_targets, persisted = _registry_agents(registry)
    agent_hosts = cfg.get('agent_hosts') if isinstance(cfg.get('agent_hosts'), dict) else {}

    parent_map = {}
    info_map = {}

    # 1. Antigravity
    if any('antigravity-' in sid.lower() or 'agy-' in sid.lower() for sid in sid_set):
        agy_dbs = []
        seen_paths = set()
        if isinstance(cfg.get('antigravity_summaries_db'), (str, Path)) and str(cfg['antigravity_summaries_db']).strip():
            p = Path(str(cfg['antigravity_summaries_db'])).expanduser()
            seen_paths.add(str(p))
            agy_dbs.append(p)
        for prof in _collect_profiles('antigravity', agents, host_targets, cfg):
            candidate = prof.parent / 'antigravity-cli' / 'conversation_summaries.db'
            if str(candidate) not in seen_paths:
                seen_paths.add(str(candidate))
                agy_dbs.append(candidate)
        agy_iids = _collect_instance_ids('antigravity', agents, cfg)
        for db_path in agy_dbs:
            _merge_loaded(_load_antigravity_map(db_path, agy_iids), parent_map, info_map)

    # 2. Codex
    needs_codex = any(
        sid.startswith('codex_')
        and not sid.lower().startswith(('codex_antigravity-', 'codex_opencode-', 'codex_pi-'))
        for sid in sid_set
    )
    if needs_codex:
        codex_dbs = []
        seen_paths = set()
        if isinstance(cfg.get('codex_state_db'), (str, Path)) and str(cfg['codex_state_db']).strip():
            p = Path(str(cfg['codex_state_db'])).expanduser()
            seen_paths.add(str(p))
            codex_dbs.append(p)
        for prof in _collect_profiles('codex', agents, host_targets, cfg):
            candidate = _latest_codex_state_db(prof)
            if candidate is not None and str(candidate) not in seen_paths:
                seen_paths.add(str(candidate))
                codex_dbs.append(candidate)
        for db_path in codex_dbs:
            _merge_loaded(_load_codex_map(db_path), parent_map, info_map)

    # 3. OpenCode (strictly gated on explicit OpenCode config or configured agent)
    has_opencode_cfg = (
        isinstance(cfg.get('opencode_db'), (str, Path))
        or isinstance(cfg.get('opencode_data_dir'), (str, Path))
        or 'opencode' in agent_hosts
        or 'opencode' in host_targets
        or any(_agent_kind(row) == 'opencode' for row in agents.values())
    )
    needs_opencode = has_opencode_cfg and any(
        'opencode-' in sid.lower() or sid.startswith('ses_') for sid in sid_set
    )
    if needs_opencode:
        oc_dbs = []
        seen_paths = set()
        if isinstance(cfg.get('opencode_db'), (str, Path)) and str(cfg['opencode_db']).strip():
            p = Path(str(cfg['opencode_db'])).expanduser()
            seen_paths.add(str(p))
            oc_dbs.append(p)
        elif isinstance(cfg.get('opencode_data_dir'), (str, Path)) and str(cfg['opencode_data_dir']).strip():
            p = Path(str(cfg['opencode_data_dir'])).expanduser() / 'opencode.db'
            seen_paths.add(str(p))
            oc_dbs.append(p)
        else:
            p = Path.home() / '.local' / 'share' / 'opencode' / 'opencode.db'
            seen_paths.add(str(p))
            oc_dbs.append(p)
        oc_iids = _collect_instance_ids('opencode', agents, cfg)
        for db_path in oc_dbs:
            _merge_loaded(_load_opencode_map(db_path, oc_iids), parent_map, info_map)

    # 4. pi (lazy bounded scan only when dataset has pi sessions and pi is configured)
    has_pi_cfg = (
        isinstance(cfg.get('pi_sessions_dir'), (str, Path))
        or 'pi' in agent_hosts
        or 'pi' in host_targets
        or any(_agent_kind(row) == 'pi' for row in agents.values())
    )
    if has_pi_cfg and any(sid.lower().startswith(('pi-', 'codex_pi-')) for sid in sid_set):
        pi_dirs = []
        seen_dirs = set()
        if isinstance(cfg.get('pi_sessions_dir'), (str, Path)) and str(cfg['pi_sessions_dir']).strip():
            d = Path(str(cfg['pi_sessions_dir'])).expanduser()
            seen_dirs.add(str(d))
            pi_dirs.append(d)
        for prof in _collect_profiles('pi', agents, host_targets, cfg):
            d = prof / 'sessions'
            if str(d) not in seen_dirs:
                seen_dirs.add(str(d))
                pi_dirs.append(d)
        pi_iids = _collect_instance_ids('pi', agents, cfg)
        for sessions_dir in pi_dirs:
            _merge_loaded(_load_pi_map(sessions_dir, pi_iids), parent_map, info_map)

    # 5. External registry persisted fallback/supplement
    for sid, rel in persisted.items():
        if not isinstance(sid, str) or sid not in sid_set or not isinstance(rel, dict):
            continue
        pid = str(rel.get('parent_session_id') or '').strip()
        role = _clean_role(rel.get('subagent_role'))
        try:
            depth = int(rel.get('nesting_depth') or 0)
        except (TypeError, ValueError):
            depth = 0
        is_sub = bool(rel.get('is_subagent') or pid or role or depth > 0)
        if pid and sid not in parent_map:
            parent_map[sid] = pid
        if is_sub:
            cur = info_map.get(sid)
            if cur is None:
                info_map[sid] = {'role': role, 'depth': max(0, depth)}
            else:
                if not cur.get('role') and role:
                    cur['role'] = role
                if depth > cur.get('depth', 0):
                    cur['depth'] = depth

    return parent_map, info_map


def resolve_session_relations(session_ids, registry=None, codex_config=None):
    """Resolve subagent & same-dataset parent relations for ``session_ids``.

    Returns ``{sid: {'is_subagent': True, ...}}`` containing only child/subagent
    entries. Ancestors outside ``session_ids`` are collapsed to the nearest
    ancestor inside ``session_ids`` or omitted completely so foreign project
    session IDs are never leaked.
    """
    sid_set = {str(s) for s in (session_ids or ()) if s}
    if not sid_set:
        return {}

    parent_map, info_map = _load_all_host_relations(sid_set, registry=registry, codex_config=codex_config)
    relations = {}
    for sid in sid_set:
        info = info_map.get(sid)
        has_parent = sid in parent_map
        if info is None and not has_parent:
            continue

        resolved_parent = ''
        visited = {sid}
        cur = parent_map.get(sid, '')
        hops = 0
        while cur and cur not in visited and hops < _MAX_ANCESTOR_HOPS:
            visited.add(cur)
            if cur in sid_set:
                resolved_parent = cur
                break
            cur = parent_map.get(cur, '')
            hops += 1

        entry = {'is_subagent': True}
        if resolved_parent:
            entry['parent_session_id'] = resolved_parent
        role = info.get('role', '') if info else ''
        if role:
            entry['subagent_role'] = role
        depth = int(info.get('depth', 0)) if info else 0
        if depth <= 0 and resolved_parent:
            depth = 1
        if depth > 0:
            entry['nesting_depth'] = depth
        relations[sid] = entry
    return relations


def descendant_ids_for_session(session_id, relations):
    """Return in-dataset child session IDs belonging to ``session_id``.

    For a top-level session (not itself in ``relations``), returns all
    descendants in this dataset whose ancestor chain rolls up to ``session_id``
    (matching UI root-grouping). For an intermediate subagent, returns its direct
    children in ``relations``.
    """
    sid = str(session_id or '')
    if not sid or not isinstance(relations, dict):
        return []
    if sid in relations:
        return [
            cid for cid, rel in relations.items()
            if cid != sid and isinstance(rel, dict) and rel.get('parent_session_id') == sid
        ]
    children = []
    for cid, rel in relations.items():
        if cid == sid or not isinstance(rel, dict):
            continue
        cur = rel.get('parent_session_id', '')
        visited = {cid}
        hops = 0
        while cur and cur not in visited and hops < _MAX_ANCESTOR_HOPS:
            if cur == sid:
                children.append(cid)
                break
            visited.add(cur)
            cur_rel = relations.get(cur)
            if not isinstance(cur_rel, dict):
                break
            cur = cur_rel.get('parent_session_id', '')
            hops += 1
    return children


def resolve_single_session_relation(session_id, dataset_session_ids, registry=None, codex_config=None):
    """Return ``(self_relation_or_none, descendant_child_ids_in_dataset)``."""
    sid = str(session_id or '')
    if not sid:
        return None, []
    ds_sids = {str(s) for s in (dataset_session_ids or ()) if s}
    ds_sids.add(sid)
    relations = resolve_session_relations(ds_sids, registry=registry, codex_config=codex_config)
    return relations.get(sid), descendant_ids_for_session(sid, relations)
