"""Startup-only candidate application and one confirmed recovery attempt.

Callers must hold the existing store/runtime ownership lock. Callbacks own the
process handles and must confirm their exit before returning from cleanup.
"""
import copy

from project_policy import PolicyError
from generation_config import validation_passed


def candidate(store):
    reference = store.state.get('next')
    if reference is None:
        return None
    row = store.resolve(reference)
    if not validation_passed(store.state['validations'], row['digest']):
        raise PolicyError('generation_validation_required', 409)
    return copy.deepcopy(reference)


def start_generation(store, launch, cleanup):
    """launch(reference, digest) returns only after health + applied digest match.

    cleanup() returning True proves the failed candidate's complete owned
    process/resource set exited. Unknown cleanup halts; it cannot start old.
    """
    active = copy.deepcopy(store.state.get('active'))
    if active is None:
        raise PolicyError('active_generation_required', 503)
    try:
        selected = candidate(store)
    except PolicyError as error:
        store.state['next_error'] = error.code
        store.save()
        selected = None
    reference = selected or active
    digest = store.resolve(reference)['digest']
    try:
        result = launch(reference, digest)
    except Exception:
        # Do not expose SDK/launcher exception text, which may carry credentials.
        store.state['startup_error'] = 'candidate_start_failed' if selected else 'active_start_failed'
        store.save()
        if cleanup() is not True:
            raise PolicyError('generation_cleanup_unconfirmed', 503) from None
        if not selected or reference == active:
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
    if selected:
        store.state.update(active=selected, next=None)
        store.state.pop('next_error', None)
        store.state.pop('startup_error', None)
        store.state['revision'] += 1
        store.save()
    return result
