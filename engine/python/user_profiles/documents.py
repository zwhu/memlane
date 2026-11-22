"""Official Cognee document persistence for user profiles."""
import hashlib
import json
import uuid

import aiohttp

from _runtime_common import read_secret
from project_policy import PolicyError

from .constants import LIMIT


class ProfileDocuments:
    """Store, verify, and retrieve user profile documents in the official Cognee store."""

    def __init__(self, official, *, official_data=None):
        self.official = official
        self.official_data = official_data

    async def dataset(self):
        """Return the dataset ID owning user profile documents."""
        return (await self.official.owned_dataset('ml-user-profiles'))['id']

    async def find(self, dataset, digest, operation):
        """Confirm a persisted document exists by content hash and operation ID."""
        rows = await self.official.request('GET', f'/api/v1/datasets/{dataset}/data?content_hash={digest}&limit=100')
        matches = [
            r
            for r in rows
            if (r.get('externalMetadata') or {}).get('profile_operation') == operation
            and r.get('datasetId') == dataset
            and r.get('contentHash') == digest
        ]
        if len(matches) != 1:
            raise PolicyError('profile_save_unconfirmed', 503)
        return str(uuid.UUID(matches[0]['id']))

    async def save(self, dataset, body, row):
        """Upload a profile document to Cognee and confirm its stored document ID."""
        form = aiohttp.FormData()
        metadata = [{
            'memlane_kind': 'user_profile',
            'profile_source': row['instance_id'],
            'profile_operation': row['operation_id'],
            'profile_method': row['method'],
            'generated_at': row['started_at'],
        }]
        for key, value in {
            'raw_data': body,
            'datasetId': dataset,
            'run_in_background': 'false',
            'external_metadata': json.dumps(metadata),
        }.items():
            form.add_field(key, value)
        official = self.official
        async with official.session.post(
            official.url + '/api/v1/add',
            data=form,
            headers={'X-Api-Key': read_secret(official.token_file)},
            allow_redirects=False,
            timeout=aiohttp.ClientTimeout(total=90),
        ) as response:
            # The ingestion receipt alone does not prove the document identity.
            if response.status != 200:
                raise PolicyError('profile_save_unconfirmed', 503)
            await response.content.read(64 * 1024)
        digest = hashlib.md5(body.encode(), usedforsecurity=False).hexdigest()
        return await self.find(dataset, digest, row['operation_id'])

    async def read(self, dataset, document):
        """Fetch raw profile content via safe local fast-path (H8) with official /raw fallback."""
        text = None
        if self.official_data:
            from local_body_reader import LocalPathSecurityError, read_document_body_with_fallback
            try:
                result = await read_document_body_with_fallback(
                    self.official_data,
                    str(dataset),
                    str(document),
                    official=self.official,
                    max_bytes=LIMIT + 4096,
                )
                if result.get('truncated'):
                    raise PolicyError('profile_document_too_large', 503)
                text = result['body']
            except LocalPathSecurityError:
                raise PolicyError('profile_document_unavailable', 503) from None
            except PolicyError as err:
                if err.code == 'profile_document_too_large' or err.status in (401, 403):
                    raise
                text = None

        if text is None:
            official = self.official
            async with official.session.get(
                official.url + f'/api/v1/datasets/{dataset}/data/{document}/raw',
                headers={'X-Api-Key': read_secret(official.token_file)},
                allow_redirects=False,
                timeout=aiohttp.ClientTimeout(total=15),
            ) as response:
                if response.status == 401:
                    raise PolicyError('official_unauthorized', 401)
                if response.status == 403:
                    raise PolicyError('dataset_access_denied', 403)
                if response.status != 200:
                    raise PolicyError('profile_document_unavailable', 503)
                body = bytearray()
                async for chunk in response.content.iter_chunked(16384):
                    body.extend(chunk)
                    if len(body) > LIMIT + 4096:
                        raise PolicyError('profile_document_too_large', 503)
                text = bytes(body).decode('utf-8')
        if text.startswith('<!-- memlane-user-profile '):
            _, separator, rest = text.partition('-->\n\n')
            if separator:
                text = rest
        return text
