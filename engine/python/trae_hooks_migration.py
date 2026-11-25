"""Preview and apply a confirmed TRAE CN beta.2 hook configuration repair."""
import copy
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import tempfile
import time

from agent_connections import (
    UPGRADE_FIELDS, pending_trae_repair, persist_operation, replay_operation, safe_file, sha, verify_hook_artifact,
)
from project_control import loopback_origin, private_write
from project_installation import plan_trae_ide_installation, prepare
from project_policy import PolicyError, mark_connection_mutation
from trae_ide_connection import (
    EVENTS, UNSUPPORTED_BETA2_EVENTS, hook_commands, is_memlane_hook_group, loader, parse_config, proposal, verify_hooks,
)


def _source(config):
    return config.get('project_control', {}).get('plugin_dir') or config.get('plugin_dir')


def _orphan_group(group, event, root):
    commands = list(hook_commands(group))
    if len(commands) != 1 or not is_memlane_hook_group(group):
        return False
    args = commands[0]
    script = Path(args[2])
    return (script.is_relative_to(root / 'managed-marketplaces') and '..' not in script.parts
            and len(script.relative_to(root / 'managed-marketplaces').parts) == 6
            and len(args) == 4 and args[3] == event
            and group == {'hooks': [{'type': 'command', 'command': group['hooks'][0]['command'], 'timeout': 25}]})


def inspect_trae_hooks_repair(registry, adapter, config):
    return _plan(registry, adapter, config)[0]


def _plan(registry, adapter, config):
    record = registry.state.get('connections', {}).get(adapter.id, {})
    agent = registry.state.get('agents', {}).get(adapter.id, {})
    active = bool(record.get('activated'))
    owned = record.get('trae_ide_hooks') or {}
    staged = record.get('trae_ide_next_hooks') or {}
    unsupported = set(UNSUPPORTED_BETA2_EVENTS)
    needed = any(isinstance(groups, dict) and unsupported.intersection(groups) for groups in (owned, staged))
    result = dict(revision=registry.state['revision'], needs_hook_repair=needed, repairable=False,
                  mode='none', repair_reason='', repair_summary='',
                  target=str(adapter.target), baseline_sha256='', proposed_sha256='', reviewed_sha='',
                  removed_events=[], retained_events=[], updated_events=[], preserved_third_party_groups=0,
                  backup_plan='', roll_forward_artifact=False, blockers=[])
    raw = proposed = replacement = planned = None
    try:
        if record.get('trae_ide_repair_journal') is not None:
            needed = True
            raise PolicyError('connection_result_requires_review', 409)
        raw = safe_file(adapter.target)
        result['baseline_sha256'] = sha(raw)
        try:
            settings = parse_config(raw)
        except PolicyError:
            # Detect broken beta.2 files by their parsed keys even when the version/shape is invalid.
            try:
                parsed = json.loads(raw.decode('utf-8'))
                hooks = parsed.get('hooks') if isinstance(parsed, dict) else None
                needed |= isinstance(hooks, dict) and bool(unsupported.intersection(hooks))
            except (ValueError, RecursionError):
                pass
            raise
        hooks = settings['hooks']
        if any(not isinstance(groups, dict) or any(not isinstance(group, dict) for group in groups.values())
               for groups in (owned, staged)):
            raise PolicyError('trae_ide_hook_changed_requires_review', 409)
        old = {}
        known_scripts = {args[2] for group in (*owned.values(), *staged.values()) for args in hook_commands(group)}
        for event in UNSUPPORTED_BETA2_EVENTS:
            if event in hooks:
                needed |= not hooks[event] or any(
                    group == owned.get(event) or group == staged.get(event)
                    or is_memlane_hook_group(group, known_scripts=known_scripts) for group in hooks[event])
        if not needed:
            if unsupported.intersection(hooks):
                raise PolicyError('trae_ide_third_party_unsupported_hook_requires_review', 409)
            return result, raw, proposed, replacement, planned
        if record.get('status') in ('preparation_unknown', 'activation_unknown', 'staging_unknown'):
            raise PolicyError('connection_result_requires_review', 409)
        for event, groups in hooks.items():
            for group in groups:
                if group == owned.get(event) or (not active and (
                        group == staged.get(event) or _orphan_group(group, event, registry.path.parent))):
                    if event in old:
                        raise PolicyError('trae_ide_hook_changed_requires_review', 409)
                    old[event] = group
                elif is_memlane_hook_group(group, known_scripts=known_scripts):
                    raise PolicyError('trae_ide_hook_changed_requires_review', 409)
                elif event in unsupported:
                    raise PolicyError('trae_ide_third_party_unsupported_hook_requires_review', 409)
                else:
                    result['preserved_third_party_groups'] += 1
        if active:
            installation = agent.get('installation')
            if not isinstance(installation, dict) or not owned:
                raise PolicyError('connection_artifact_changed', 409)
            verify_hook_artifact({**installation, 'host_kind': adapter.kind, 'instance_id': adapter.id},
                                 str(adapter.profile))
            verify_hooks(raw, owned, Path(installation['plugin_dir']) / 'scripts/trae_ide_hook.py')
            python = config.get('python') or next(hook_commands(owned['Stop']))[0]
            source = _source(config)
            if source and (Path(source) / '.memlane-project-access.json').is_file():
                planned = plan_trae_ide_installation(source, registry.path.parent, adapter.id,
                                                     python=python)
            result['roll_forward_artifact'] = bool(planned and planned['plugin_dir'] != installation['plugin_dir'])
            replacement = loader(planned or installation, python)
        emptied = [event for event in UNSUPPORTED_BETA2_EVENTS if event in hooks and not hooks[event]]
        for event in emptied:
            del hooks[event]
        proposed = proposal(json.dumps(settings), old, replacement)
        result.update(repairable=True, proposed_sha256=sha(proposed), reviewed_sha=sha(proposed),
                      removed_events=[event for event in UNSUPPORTED_BETA2_EVENTS if event in old or event in emptied],
                      retained_events=list(parse_config(proposed)['hooks']),
                      updated_events=[event for event in EVENTS if replacement and old.get(event) != replacement[event]],
                      backup_plan='hooks.json.memlane-repair-backup-<UTC timestamp>-<baseline SHA prefix>')
    except PolicyError as error:
        result['blockers'].append({'code': error.code, 'status': error.status, 'phase': 'pre_mutation'})
    except (OSError, KeyError, TypeError, ValueError, AttributeError):
        result['blockers'].append({'code': 'trae_ide_configuration_requires_review', 'status': 409,
                                   'phase': 'pre_mutation'})
    if needed:
        result.update(needs_hook_repair=True, mode='active_migration' if active else 'orphan_cleanup',
                      repair_reason='trae_ide_beta2_unsupported_events',
                      repair_summary='将 TRAE CN 连接迁移到四个受支持事件' if active else '清理旧版 Memlane Hooks')
    return result, raw, proposed, replacement, planned


def _backup(target, raw, baseline):
    stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    path = target.with_name(f'{target.name}.memlane-repair-backup-{stamp}-{baseline[:8]}')
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    except FileExistsError:
        fd, name = tempfile.mkstemp(prefix=path.name + '-', dir=path.parent)
        path = Path(name)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(raw)
        stream.flush()
        os.fsync(stream.fileno())
    directory = os.open(target.parent, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)
    return path


def _disk_registry(registry):
    try:
        disk = json.loads(registry.path.read_bytes())
    except (OSError, ValueError):
        return {}
    return disk if isinstance(disk, dict) else {}


def _repair_committed_on_disk(registry, identity):
    disk = _disk_registry(registry)
    return (disk.get('revision') == registry.state['revision']
            and (disk.get('connections') or {}).get(identity, {}).get('trae_ide_repair_journal') is None)


def _repair_receipt(identity, reviewed_sha):
    action = 'repair-trae-hooks'
    return dict(signature=sha(json.dumps([identity, action, reviewed_sha]).encode()), status='unknown',
                action=action, instance_id=identity, reviewed_sha=reviewed_sha)


def _finish_repair(registry, adapter, journal):
    state = registry.state
    identity, operation_id = adapter.id, journal['operation_id']
    records, operations = state['connections'], state['connection_operations']
    old_record, old_receipt = records[identity], operations.get(operation_id)
    record = copy.deepcopy(old_record)
    agent = state['agents'].get(identity)
    old_installation = agent.get('installation') if agent else None
    receipt = _repair_receipt(identity, journal['target_proposal_sha256'])
    if old_receipt is not None:
        receipt.update({key: value for key, value in old_receipt.items()
                        if key not in ('repair_journal', 'phase', 'error_code', 'outcome', 'result')})
    live = safe_file(adapter.target)
    live_sha = sha(live)
    applied = live_sha == journal['target_proposal_sha256']
    active = applied and journal['mode'] == 'active_migration'
    if active:
        installation = journal['target_installation']
        if not agent or not isinstance(installation, dict):
            raise PolicyError('connection_artifact_changed', 409)
        verify_hook_artifact({**installation, 'host_kind': adapter.kind, 'instance_id': identity}, str(adapter.profile))
        hooks = journal['target_hooks']
        verify_hooks(live, hooks, Path(installation['plugin_dir']) / 'scripts/trae_ide_hook.py')
        digest = journal['target_proposed_sha256']
        if digest != sha((installation['manifest_sha256'] + live_sha).encode()):
            raise PolicyError('connection_artifact_changed', 409)
        record.update(trae_ide_hooks=hooks, trae_ide_next_hooks=hooks, activated=True,
                      trae_ide_proposal_sha256=live_sha, proposed_sha256=digest,
                      preview={'baseline_sha256': journal['baseline_sha256'],
                               'proposed_sha256': digest, 'target': str(adapter.target)},
                      status='configured_pending_new_session', activated_at=time.time(),
                      sessions_before_activation=[key for key in state['sessions'] if json.loads(key)[0] == identity],
                      environment_receipt_required=True)
        for key in (*UPGRADE_FIELDS, 'upgraded_at'):
            record.pop(key, None)
    elif applied:
        # A discarded beta.2 preparation cannot subsequently be activated.
        if any(event in (record.get(key) or {}) for event in UNSUPPORTED_BETA2_EVENTS
               for key in ('trae_ide_hooks', 'trae_ide_next_hooks')):
            for key in (*UPGRADE_FIELDS, 'trae_ide_hooks', 'trae_ide_next_hooks',
                        'trae_ide_proposal_sha256', 'proposed_sha256', 'preview'):
                record.pop(key, None)
            record['status'] = 'not_connected'
    elif live_sha != journal['baseline_sha256']:
        raise PolicyError('trae_ide_hook_changed_requires_review', 409, phase='post_mutation')

    record.pop('trae_ide_repair_journal')
    revision = state['revision']
    mark_connection_mutation()
    try:
        records[identity] = record
        if active:
            agent['installation'] = installation
        state['revision'] = revision + 1
        row = adapter.view()
        result = {'revision': state['revision'], 'connection': row}
        if applied:
            result.update(repaired=True, mode=journal['mode'], backup_path=journal['backup_path'],
                          operation_id=operation_id, reviewed_sha=live_sha, proposed_sha256=live_sha,
                          summary=journal['summary'], removed_events=journal['removed_events'],
                          retained_events=journal['retained_events'])
        else:
            receipt.update(outcome='rejected', phase='pre_mutation', error_code='trae_ide_repair_did_not_apply')
        receipt.update(status='complete', result=copy.deepcopy(result))
        operations[operation_id] = receipt
        registry.save()
    except BaseException:
        if not _repair_committed_on_disk(registry, identity):
            state['revision'] = revision
            records[identity] = old_record
            if old_receipt is None:
                operations.pop(operation_id, None)
            else:
                operations[operation_id] = old_receipt
            if active:
                agent['installation'] = old_installation
        raise
    return result


def recover_trae_hooks_repair(registry, adapter):
    journal = registry.state['connections'][adapter.id]['trae_ide_repair_journal']
    return _finish_repair(registry, adapter, journal)['connection']


def apply_trae_hooks_repair(registry, adapter, config, *, expected_revision, operation_id, reviewed_sha):
    from agent_connections import validate_operation_id

    validate_operation_id(operation_id)
    r = registry
    receipt = _repair_receipt(adapter.id, reviewed_sha)
    operations = r.state.setdefault('connection_operations', {})
    records = r.state.setdefault('connections', {})
    if pending := pending_trae_repair(r.state, operation_id):
        identity, journal = pending
        # A journal can precede the SQLite write or outlive its terminal receipt.
        existing = operations.get(operation_id) or _repair_receipt(identity, journal['target_proposal_sha256'])
        return replay_operation({**existing, 'status': 'unknown', 'phase': 'post_mutation'}, receipt['signature'])
    if operation_id in operations:
        return replay_operation(operations[operation_id], receipt['signature'])
    try:
        r.check_revision(expected_revision)
        plan, raw, proposed, next_hooks, planned = _plan(r, adapter, config)
        if not plan['needs_hook_repair']:
            raise PolicyError('trae_ide_hook_repair_not_needed', 409)
        if not plan['repairable']:
            raise PolicyError(plan['blockers'][0]['code'], 409)
        if not isinstance(reviewed_sha, str) or reviewed_sha != plan['reviewed_sha']:
            raise PolicyError('trae_ide_repair_preview_changed', 409)
        baseline = plan['baseline_sha256']
        if sha(safe_file(adapter.target)) != baseline:
            raise PolicyError('trae_ide_repair_preview_changed', 409)
    except PolicyError as error:
        error.phase = 'pre_mutation'
        receipt.update(status='complete', outcome='rejected', phase=error.phase, error_code=error.code,
                       result={'revision': r.state['revision']})
        persist_operation(r, operation_id, receipt)
        raise

    installation = r.state['agents'][adapter.id]['installation'] if plan['mode'] == 'active_migration' else None
    proposed_sha = sha(proposed)
    intended = planned or installation
    digest = sha((intended['manifest_sha256'] + proposed_sha).encode()) if intended else proposed_sha
    repair_journal = dict(operation_id=operation_id, mode=plan['mode'], baseline_sha256=baseline,
                          target_proposal_sha256=proposed_sha, target_proposed_sha256=digest,
                          target_hooks=next_hooks, target_installation=copy.deepcopy(installation), backup_path='',
                          removed_events=plan['removed_events'], retained_events=plan['retained_events'],
                          summary=plan['repair_summary'])
    old_record = records.get(adapter.id)
    record = {**(old_record or {}), 'trae_ide_repair_journal': copy.deepcopy(repair_journal)}
    records[adapter.id] = record
    try:
        r.save()
    except BaseException as failure:
        disk_record = (_disk_registry(r).get('connections') or {}).get(adapter.id, {})
        durable = disk_record.get('trae_ide_repair_journal') == repair_journal
        if not durable:
            if old_record is None:
                records.pop(adapter.id)
            else:
                records[adapter.id] = old_record
        if isinstance(failure, (OSError, ValueError)):
            phase = 'post_mutation' if durable else 'pre_mutation'
            raise PolicyError('trae_ide_hook_repair_result_unknown', 503, phase=phase) from failure
        raise

    try:
        receipt.update(phase='post_mutation', repair_journal=copy.deepcopy(repair_journal))
        persist_operation(r, operation_id, receipt)
        if installation is not None:
            agent = r.state['agents'][adapter.id]
            if plan['roll_forward_artifact']:
                control_url = config.get('project_control', {}).get('url') or config['project_control_url']
                origin = loopback_origin(control_url)
                installation = prepare(_source(config), r.path.parent, adapter.id, agent, origin + '/cognee',
                                       control_url, installation['home'], host_kind=adapter.kind,
                                       python=next(hook_commands(next_hooks['Stop']))[0])
                if installation['plugin_dir'] != planned['plugin_dir']:
                    raise PolicyError('trae_ide_repair_preview_changed', 409)
                marker = Path(installation['plugin_dir']) / '.memlane-installation.json'
                data = json.loads(safe_file(marker))
                data.update(runtime_profile=str(adapter.profile), hook_origin=origin,
                            hook_token_file=agent['control_token_file'])
                private_write(marker, json.dumps(data).encode())
            verify_hook_artifact({**installation, 'host_kind': adapter.kind, 'instance_id': adapter.id},
                                 str(adapter.profile))
            script = Path(installation['plugin_dir']) / 'scripts/trae_ide_hook.py'
            verify_hooks(proposed, next_hooks, script)
        backup_path = _backup(adapter.target, raw, baseline)
        digest = sha((installation['manifest_sha256'] + proposed_sha).encode()) if installation else proposed_sha
        repair_journal.update(target_proposed_sha256=digest, target_installation=copy.deepcopy(installation),
                              backup_path=str(backup_path))
        receipt['repair_journal'] = copy.deepcopy(repair_journal)
        record['trae_ide_repair_journal'] = copy.deepcopy(repair_journal)
        r.save()
        if sha(safe_file(adapter.target)) != baseline:
            raise PolicyError('trae_ide_repair_preview_changed', 409)
        private_write(adapter.target, proposed)
        if safe_file(adapter.target) != proposed:
            raise PolicyError('host_configuration_changed', 409)
        return _finish_repair(r, adapter, repair_journal)
    except (PolicyError, OSError, ValueError, KeyError) as failure:
        error = failure if isinstance(failure, PolicyError) else PolicyError('trae_ide_hook_repair_result_unknown', 503)
        error.phase = 'post_mutation'
        if not (records[adapter.id].get('trae_ide_repair_journal') is None
                and operations.get(operation_id, {}).get('status') == 'complete'
                and _repair_committed_on_disk(r, adapter.id)):
            receipt.update(phase=error.phase, error_code=error.code)
            persist_operation(r, operation_id, receipt)
        raise error
