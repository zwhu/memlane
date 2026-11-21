"""One validation-owned Agent endpoint, independent of the active service."""
import math
import secrets
import socket
from pathlib import Path
from types import SimpleNamespace

from aiohttp import web

from codex_completion_endpoint import create_app, terminate
from generation_config import compile_environment
from process_groups import group_exists
from _runtime_common import private_write


from agent_bridge_runtime import (resolve_bridge_runtime, check_bridge_runtime, bridge_choices,
                                  bridge_runtime_fingerprint, bridge_selection_authorized,
                                  require_bridge_startup, require_bridge_validation)


class ValidationBridge:
    def __init__(self, runtime, profile, root, changed, *, concurrency=1, timeout=90):
        try:
            idle_timeout = float(timeout)
        except (TypeError, ValueError):
            raise ValueError('timeout must be a positive finite number') from None
        if not math.isfinite(idle_timeout) or idle_timeout <= 0:
            raise ValueError('timeout must be a positive finite number')
        self.selected = check_bridge_runtime(runtime, profile)
        self.runtime, self.profile, self.root = runtime, profile, Path(root)
        self.processes = {}
        self.changed = changed
        self.concurrency = concurrency
        self.timeout = idle_timeout
        self.runner, self.socket = None, None

    async def start(self):
        token_file = self.root / 'completion-token'
        private_write(token_file, secrets.token_urlsafe(48).encode())
        args = SimpleNamespace(token_file=token_file, model=self.profile['model'],
                               codex_executable=self.selected['executable'], agent_executable=self.selected['executable'],
                               auth_home=self.selected['home'], bridge_kind=self.selected['kind'],
                               bridge_provider=self.profile.get('bridge_provider'), timeout=self.timeout,
                               max_concurrency=self.concurrency, max_inflight=self.concurrency,
                               workspace_root=self.root, diagnostics_dir=self.root / 'diagnostics',
                               owned_processes=self.processes, ownership_changed=self.changed)
        if self.selected['kind'] == 'pi':
            from pi_completion import execute
            args.execute_request = execute
        elif self.selected['kind'] == 'antigravity':
            from antigravity_completion import execute
            args.execute_request = execute
        self.runner = web.AppRunner(create_app(args), handle_signals=False, handler_cancellation=True,
                                    shutdown_timeout=15, access_log=None)
        await self.runner.setup()
        self.socket = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.socket.bind(('127.0.0.1', 0))
        self.socket.listen(16)
        self.socket.setblocking(False)
        url = 'http://127.0.0.1:' + str(self.socket.getsockname()[1])
        await web.SockSite(self.runner, self.socket).start()
        return compile_environment(self.profile, {'completion_url': url, 'completion_token_file': str(token_file)})

    async def close(self):
        try:
            if self.runner:
                await self.runner.cleanup()
                self.runner = None
        finally:
            if self.socket:
                self.socket.close()
                self.socket = None
        # HTTP shutdown awaits request cancellation. Retain and check any group
        # whose handler could not confirm exit; never infer from socket closure.
        for pid, process in list(self.processes.items()):
            if group_exists(pid):
                await terminate(process)
            else:
                await process.wait()
            self.processes.pop(pid)
            self.changed()
        if self.processes:
            raise RuntimeError('validation_bridge_cleanup_unconfirmed')
