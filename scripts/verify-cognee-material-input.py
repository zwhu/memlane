"""Installed SDK literal material storage contract; no model or graph calls."""
import asyncio
import importlib
import json
import os
from pathlib import Path
import sys
import tempfile
from unittest.mock import patch
from uuid import uuid4

if os.environ.get('MEMLANE_DISK_TASK') != '1':
    raise SystemExit('managed_task_required')
with tempfile.TemporaryDirectory(prefix='memlane-material-input-') as folder:
    base = Path(folder)
    for name in ('home', 'system', 'data', 'cache', 'logs'):
        (base / name).mkdir()
    os.environ.update(HOME=str(base/'home'), SYSTEM_ROOT_DIRECTORY=str(base/'system'), DATA_ROOT_DIRECTORY=str(base/'data'),
                      CACHE_ROOT_DIRECTORY=str(base/'cache'), LOGS_ROOT_DIRECTORY=str(base/'logs'), COGNEE_LOGS_DIR=str(base/'logs'),
                      PYTHON_DOTENV_DISABLED='1', HF_HUB_OFFLINE='1', TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false')
    sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'engine/python'))
    import cognee_submission
    storage = importlib.import_module('cognee.tasks.ingestion.save_data_item_to_storage')
    from cognee.infrastructure.files.utils.open_data_file import open_data_file
    local = base/'must-not-be-read.txt'
    local.write_text('This is not the submitted material.')
    async def prove():
        for text in ('https://must-remain-literal.invalid/private', str(local), 'Exact first line\nExact final line'):
            identity=uuid4(); metadata={'synthetic':'exact'}
            item=cognee_submission.material_data_item({'text':text},identity,metadata)
            assert item.data_id == identity and item.external_metadata == metadata
            with patch.object(storage,'fetch_page_content',side_effect=AssertionError('outbound fetch forbidden')), patch.object(storage,'require_server_path_access',side_effect=AssertionError('local path interpretation forbidden')):
                saved=await storage.save_data_item_to_storage_detailed(item)
            async with open_data_file(saved.file_path,mode='r',encoding='utf-8') as f:
                assert f.read() == text, 'literal body changed'
    asyncio.run(prove())
    print(json.dumps({'status':'passed','installed_sdk_literal_storage':True,'cases':3,'model_called':False,'full_graph':False}))
