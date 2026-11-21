"""Operation journal and orchestration state machine for user profiles."""
import asyncio
import copy
import hashlib
import json
from pathlib import Path
import time

from _runtime_common import private_write
from project_policy import PolicyError

from .constants import MANUAL_SOURCES, valid_body
from .documents import ProfileDocuments
from .errors import ProfileAgentFailure
from .providers import GENERATE_KINDS, NAMES
from .runner import generate


class UserProfiles:
    """Manage profile sources, generation/import lifecycle, and Cognee persistence."""

    def __init__(self, registry, connections, documents=None, runner=generate):
        self.registry, self.connections = registry, connections
        self.documents, self.runner = documents or ProfileDocuments(registry.official), runner
        self.path = registry.path.parent / 'user-profiles.json'
        self.state = json.loads(self.path.read_text()) if self.path.exists() else {'profiles': {}}
        self.tasks = {}
        self.lock = asyncio.Lock()
        self.current = {}
        for row in self.state['profiles'].values():
            if row['status'] in ('queued', 'generating', 'saving'):
                row.update(status='unknown', error_code='profile_interrupted')
        self.persist()

    def persist(self):
        """Persist operation journal atomically with private file permissions."""
        private_write(self.path, json.dumps(self.state).encode())

    def sources(self):
        """Return active coding Agent connections paired with manual AI sources."""
        rows = self.connections.view()['connections'] if self.connections else []
        rows = [r for r in rows if r.get('instance_id') not in self.registry.state.get('agent_retirements', {})]
        for identifier, name in MANUAL_SOURCES.items():
            rows.append({'instance_id': identifier, 'kind': 'manual', 'name': name, 'status': 'manual'})
        self.current = {r['instance_id']: r for r in rows}
        return rows

    @staticmethod
    def eligible(source):
        # Generating a portrait disables shared tools/hooks. It does not depend
        # on receiving a new capture session or installing a Cognee integration.
        return (
            source.get('kind') in GENERATE_KINDS
            and source.get('detected') is True
            and not source.get('environment_issue')
        )

    def view(self):
        """Build UI directory payload combining live connection metadata and stored state."""
        from .constants import profile_prompt

        result = []
        for source in self.sources():
            identifier = source['instance_id']
            stored = copy.deepcopy(self.state['profiles'].get(identifier, {}))
            stored.pop('pending_file', None)
            stored.pop('content_hash', None)
            result.append({
                **stored,
                'instance_id': identifier,
                'kind': source['kind'],
                'name': source.get('name') or NAMES.get(source['kind'], source['kind']),
                'can_generate': self.eligible(source),
                'status': stored.get('status', 'manual'),
                'connection_status': source['status'],
                'save_unconfirmed': (
                    stored.get('status') == 'unknown'
                    and bool(self.state['profiles'].get(identifier, {}).get('pending_file'))
                ),
            })
        return {'profiles': result, 'prompt': profile_prompt()}

    def source(self, identifier):
        """Look up one active profile source by instance ID."""
        self.sources()
        if identifier not in self.current:
            raise PolicyError('profile_source_unavailable', 404)
        return self.current[identifier]

    async def start(self, identifier, operation, *, body=None):
        """Start an idempotent automatic generation or manual import operation."""
        if not isinstance(operation, str) or not 8 <= len(operation) <= 128:
            raise PolicyError('invalid_profile_operation')
        if body is not None:
            body = valid_body(body)
        async with self.lock:
            source = self.source(identifier)
            old = self.state['profiles'].get(identifier, {})
            if operation == old.get('operation_id'):
                return self.view()
            if old.get('status') in ('queued', 'generating', 'saving') or (
                old.get('status') == 'unknown' and (body is None or old.get('pending_file'))
            ):
                raise PolicyError('profile_operation_requires_review', 409)
            if body is None and not self.eligible(source):
                raise PolicyError('profile_automatic_unavailable', 409)
            if old.get('operation_id'):
                # Preserve prior results and failure evidence when explicitly retried.
                self.state.setdefault('attempts', {})[old['operation_id']] = copy.deepcopy(old)
            row = {
                'instance_id': identifier,
                'kind': source['kind'],
                'operation_id': operation,
                'method': 'manual' if body is not None else 'agent',
                'status': 'queued',
                'started_at': time.time(),
                'previous': old.get('document') or old.get('previous'),
            }
            # Preserve the currently readable version while refreshing.
            if old.get('document'):
                row['document'] = old['document']
                row['updated_at'] = old.get('updated_at')
            self.state['profiles'][identifier] = row
            self.persist()
            adapter = self.connections.adapters.get(identifier) if self.connections else None
            task = asyncio.create_task(self.run(row, source, adapter, body))
            self.tasks[identifier] = task

            def finished(done):
                if self.tasks.get(identifier) is done:
                    self.tasks.pop(identifier, None)

            task.add_done_callback(finished)
            return self.view()

    async def run(self, row, source, adapter, body):
        """Execute serialized generation/import and verify official Cognee persistence."""
        try:
            # A single worker across agents prevents competing model launches.
            async with self.gate:
                if body is None:
                    row['status'] = 'generating'
                    self.persist()

                    def progress(value):
                        row.update(progress=value)
                        self.persist()

                    body = await self.runner(adapter, {**source, '_progress': progress})
                # Verify the same instance is still usable before publishing.
                current = self.source(row['instance_id'])
                if row['method'] == 'agent' and not self.eligible(current):
                    raise PolicyError('profile_source_changed', 409)
                pending = (
                    self.path.parent
                    / 'profile-pending'
                    / (hashlib.sha256(row['operation_id'].encode()).hexdigest() + '.md')
                )
                # Official ingestion deduplicates identical bytes in a dataset.
                # A source/operation marker keeps independent portraits distinct.
                body = (
                    '<!-- memlane-user-profile '
                    + json.dumps({'source': row['instance_id'], 'operation': row['operation_id']}, ensure_ascii=True)
                    + ' -->\n\n'
                    + valid_body(body)
                )
                private_write(pending, body.encode())
                row.update(
                    status='saving',
                    pending_file=str(pending),
                    content_hash=hashlib.md5(body.encode(), usedforsecurity=False).hexdigest(),
                )
                self.persist()
                dataset = await self.documents.dataset()
                row.update(dataset_id=dataset, status='saving')
                self.persist()
                document = await self.documents.save(dataset, body, row)
                row.update(document={'dataset_id': dataset, 'data_id': document}, status='ready', updated_at=time.time())
                row.pop('error_code', None)
                self.persist()
                pending.unlink(missing_ok=True)
                row.pop('pending_file', None)
                self.persist()
        except asyncio.CancelledError:
            cancelled = row.get('cancel_requested') and row.get('status') != 'saving'
            row.update(
                status='cancelled' if cancelled else 'unknown',
                error_code='profile_cancelled' if cancelled else 'profile_interrupted',
            )
            self.persist()
            raise
        except Exception as error:
            row.update(
                status='unknown' if row.get('status') == 'saving' else 'failed',
                error_code=error.code if isinstance(error, PolicyError) else 'profile_result_unconfirmed',
            )
            if isinstance(error, ProfileAgentFailure):
                row.update(exit_code=error.returncode, failure_reason=error.diagnostic)
                if error.detail:
                    row['error_detail'] = error.detail
            elif isinstance(error, asyncio.TimeoutError):
                row.update(error_code='profile_generation_timeout')
            self.persist()

    async def read(self, identifier):
        """Read the confirmed official Cognee portrait document for a source."""
        self.source(identifier)
        document = self.state['profiles'].get(identifier, {}).get('document')
        if not document:
            raise PolicyError('profile_document_unavailable', 404)
        return {'body': await self.documents.read(document['dataset_id'], document['data_id']), 'document': document}

    async def reconcile(self, identifier):
        """Re-verify an interrupted save against official Cognee by operation metadata."""
        self.source(identifier)
        row = self.state['profiles'].get(identifier, {})
        if row.get('status') != 'unknown' or not row.get('content_hash'):
            raise PolicyError('profile_operation_requires_review', 409)
        if not row.get('dataset_id'):
            raise PolicyError('profile_dataset_unconfirmed', 409)
        document = await self.documents.find(row['dataset_id'], row['content_hash'], row['operation_id'])
        row.update(document={'dataset_id': row['dataset_id'], 'data_id': document}, status='ready', updated_at=time.time())
        row.pop('error_code', None)
        self.persist()
        if row.get('pending_file'):
            Path(row['pending_file']).unlink(missing_ok=True)
            row.pop('pending_file', None)
            self.persist()
        return self.view()

    async def cancel(self, identifier, operation):
        """Cancel an in-flight queued or generating operation."""
        self.source(identifier)
        row = self.state['profiles'].get(identifier, {})
        task = self.tasks.get(identifier)
        if row.get('operation_id') != operation or row.get('status') not in ('queued', 'generating') or not task:
            raise PolicyError('profile_cancel_unavailable', 409)
        row['cancel_requested'] = True
        task.cancel()
        await asyncio.gather(task, return_exceptions=True)
        # A task cancelled before entering run has no exception handler yet.
        if row['status'] == 'queued':
            row.update(status='cancelled', error_code='profile_cancelled')
            self.persist()
        return self.view()

    async def close(self):
        """Cancel all running profile tasks when shutting down the control server."""
        tasks = list(self.tasks.values())
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)

    @property
    def gate(self):
        """Serialize automatic profile generation across hosts."""
        if not hasattr(self, '_gate'):
            self._gate = asyncio.Semaphore(1)
        return self._gate
