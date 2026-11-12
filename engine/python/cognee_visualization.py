"""Dataset-scoped access to the official visualization payload, without layout code.

The pinned public JSON path may resolve an absent dataset to None. Never pass
None into its database context: bind the exact existing authorized Dataset once
and use the same official fetch/preprocessor as that route.
"""
from uuid import UUID


async def read_visualization(dataset_id, user, max_nodes, seeds):
    from cognee.modules.data.methods import get_authorized_existing_datasets
    from cognee.api.v1.visualize.visualize import fetch_dataset_graph_data
    from cognee.modules.visualization.cognee_network_visualization import build_visualization_payload

    if user is None or not 1 <= max_nodes <= 1000 or len(seeds) > 16:
        raise ValueError('visualization_request_invalid')
    if any(not isinstance(seed, str) or not seed or len(seed) > 256 or
           any(char.isspace() or ord(char) < 32 for char in seed) for seed in seeds):
        raise ValueError('visualization_request_invalid')
    dataset_id = UUID(str(dataset_id))
    datasets = await get_authorized_existing_datasets([dataset_id], 'read', user, strict=True)
    if len(datasets) != 1 or datasets[0].id != dataset_id:
        raise PermissionError('visualization_scope_denied')
    dataset = datasets[0]
    graph_data = await fetch_dataset_graph_data(
        dataset, full=False, seed_node_ids=seeds or None,
        neighborhood_depth=2, neighborhood_seed_top_k=16 if seeds else 10,
        max_nodes=max_nodes)
    # Preserve every official field and classification. History is read through
    # its existing separately authorized entry, never added as a side effect.
    payload = build_visualization_payload(graph_data, search_events=[])
    fresh = await get_authorized_existing_datasets([dataset_id], 'read', user, strict=True)
    if len(fresh) != 1 or fresh[0].id != dataset.id or fresh[0].owner_id != dataset.owner_id:
        raise PermissionError('visualization_scope_denied')
    return payload


def install(app, instance):
    from fastapi import Depends, HTTPException, Query
    from cognee.modules.users.methods import get_authenticated_user

    @app.get('/api/v1/memlane/datasets/{dataset_id}/visualization-scope')
    async def scope(dataset_id: UUID, expected_instance: UUID,
                    user=Depends(get_authenticated_user)):
        if str(expected_instance) != instance:
            raise HTTPException(409, 'Cognee service identity changed')
        from cognee.modules.data.methods import get_authorized_existing_datasets
        try:
            rows = await get_authorized_existing_datasets([dataset_id], 'read', user, strict=True)
            if len(rows) != 1 or rows[0].id != dataset_id:
                raise PermissionError()
        except Exception:
            raise HTTPException(409, 'Authorized visualization scope unavailable') from None
        return {'instance': instance, 'dataset': str(dataset_id)}

    @app.get('/api/v1/memlane/datasets/{dataset_id}/visualization')
    async def visualization(dataset_id: UUID, expected_instance: UUID,
                            max_nodes: int = Query(1000, ge=1, le=1000),
                            seed_node_ids: list[str] = Query(default=[]),
                            user=Depends(get_authenticated_user)):
        if str(expected_instance) != instance:
            raise HTTPException(409, 'Cognee service identity changed')
        try:
            payload = await read_visualization(dataset_id, user, max_nodes, seed_node_ids)
        except ValueError:
            raise HTTPException(422, 'Bounded visualization request required') from None
        except Exception:
            # SDK exceptions can contain DB paths/material; none leave here.
            raise HTTPException(409, 'Authorized visualization could not be read') from None
        return {'instance': instance, 'dataset': str(dataset_id),
                'scope': 'bounded_subgraph', 'max_nodes': max_nodes, 'payload': payload}
