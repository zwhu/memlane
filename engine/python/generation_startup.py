"""Startup-only candidate application and one confirmed recovery attempt.

Callers must hold the existing store/runtime ownership lock. Callbacks own the
process handles and must confirm their exit before returning from cleanup.
"""
import copy
import json
from pathlib import Path

from project_policy import PolicyError
from generation_config import validation_passed


def candidate(store):
    reference = store.state.get('next')
    if reference is None:
        return None
    row = store.resolve(reference)
    from agent_bridge_runtime import bridge_selection_authorized
    if not bridge_selection_authorized(store.state, row) and not validation_passed(store.state['validations'], row['digest']):
        raise PolicyError('generation_validation_required', 409)
    store.native_schema_evidence(row)
    return copy.deepcopy(reference)


def interrupted_applied_candidate(store):
    """Recover only a matching durable application whose management reply was lost."""
    pending = {key: row for key, row in store.state.get('handoffs', {}).items()
               if row.get('status') in ('queued', 'preparing', 'stopping', 'applying', 'reconciling', 'unknown')}
    if not pending:
        return None
    path = Path(store.path).with_name('generation-handoff-runtime.json')
    if not path.exists():
        return None  # The request may never have reached the service.
    if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o077 or path.stat().st_size > 8 * 1024 * 1024:
        raise PolicyError('private_generation_handoff_state_required', 503)
    rows = json.loads(path.read_text())
    recovered = []
    for operation, row in pending.items():
        receipt = rows.get(operation, {})
        if receipt.get('status') != 'applied':
            continue
        reference = row['reference']
        if (receipt.get('reference') != reference or receipt.get('digest') != row['digest']
                or store.resolve(reference)['digest'] != row['digest']):
            raise PolicyError('generation_handoff_recovery_mismatch', 503)
        if row.get('active_committed') and store.state.get('active') == reference:
            continue
        recovered.append(reference)
    if len(recovered) > 1:
        raise PolicyError('generation_handoff_recovery_ambiguous', 503)
    return copy.deepcopy(recovered[0]) if recovered else None


def start_generation(store, launch, cleanup):
    """launch(reference, digest) returns only after health + applied digest match.

    cleanup() returning True proves the failed candidate's complete owned
    process/resource set exited. Unknown cleanup halts; it cannot start old.
    """
    active = copy.deepcopy(store.state.get('active'))
    recovered = interrupted_applied_candidate(store)
    if recovered and store.state.get('next') not in (None, recovered):
        raise PolicyError('generation_handoff_recovery_conflicts_with_next', 503)
    try:
        selected = candidate(store)
    except PolicyError as error:
        store.state['next_error'] = error.code
        store.save()
        selected = None
    reference = recovered or selected or active
    if reference is None:
        raise PolicyError('active_generation_required', 503)
    digest = store.resolve(reference)['digest']
    try:
        result = launch(reference, digest)
    except Exception:
        # Do not expose SDK/launcher exception text, which may carry credentials.
        store.state['startup_error'] = 'candidate_start_failed' if selected else 'active_start_failed'
        store.save()
        if cleanup() is not True:
            raise PolicyError('generation_cleanup_unconfirmed', 503) from None
        if recovered or active is None or not selected or reference == active:
            raise PolicyError('generation_start_failed', 503) from None
        # Exactly one attempt at the original revision. No content/task replay.
        try:
            result = launch(active, store.resolve(active)['digest'])
        except Exception:
            store.state['startup_error'] = 'previous_generation_start_failed'
            store.save()
            if cleanup() is not True:
                raise PolicyError('generation_cleanup_unconfirmed', 503) from None
            raise PolicyError('generation_recovery_failed', 503) from None
        return result
    if recovered or selected:
        if recovered:
            row = store.resolve(reference)
            if not validation_passed(store.state['validations'], row['digest']):
                from agent_bridge_runtime import mark_connected_bridge_selection
                mark_connected_bridge_selection(store.state, row)
        store.state.update(active=reference, next=None)
        store.state.pop('next_error', None)
        store.state.pop('startup_error', None)
        store.state['revision'] += 1
        store.save()
    return result
