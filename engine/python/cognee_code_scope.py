"""Read-only readiness for a separately authorized official code dataset."""
from uuid import UUID


def install(app, instance):
    from fastapi import Depends, HTTPException
    from cognee.modules.users.methods import get_authenticated_user

    @app.get('/api/v1/memlane/code/scope')
    async def scope(dataset: UUID, expected_instance: UUID,
                    user=Depends(get_authenticated_user)):
        if str(expected_instance) != instance:
            raise HTTPException(409, 'Cognee service identity changed')
        if user is None:
            raise HTTPException(403, 'Official user required')
        from cognee.modules.data.methods import get_authorized_existing_datasets, get_dataset_data
        from cognee.context_global_variables import set_database_global_context_variables
        from cognee.infrastructure.databases.graph import get_graph_engine
        try:
            rows = await get_authorized_existing_datasets([dataset], 'write', user, strict=True)
            if len(rows) != 1 or rows[0].id != dataset:
                raise PermissionError()
            data = await get_dataset_data(dataset, limit=1)
            if not isinstance(data, list) or len(data) > 1:
                raise ValueError()
            async with set_database_global_context_variables(dataset, rows[0].owner_id):
                graph = await get_graph_engine()
                counts = await graph.get_graph_counts()
            if len(counts) != 2 or any(type(n) is not int or n < 0 for n in counts):
                raise ValueError()
        except Exception:
            # Unknown counts are never interpreted as an empty dataset.
            raise HTTPException(409, 'Official code dataset could not be verified') from None
        return {'instance': instance, 'dataset': str(dataset), 'user': str(user.id),
                'empty': not data and counts == (0, 0)}
