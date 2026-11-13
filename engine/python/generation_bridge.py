"""One validation-owned Codex endpoint, independent of the active service."""
import secrets
import socket
from pathlib import Path
from types import SimpleNamespace

from aiohttp import web

from codex_completion_endpoint import create_app, terminate
from generation_config import compile_environment
from process_groups import group_exists
from project_control import private_write
from project_policy import PolicyError


def check_bridge_runtime(runtime):
    try:
        executable = Path(runtime['codex_executable'])
        home = Path(runtime['auth_home'])
        auth = home / 'auth.json'
        if not executable.is_absolute() or not executable.is_file() or not home.is_absolute() or not home.is_dir():
            raise ValueError()
        if auth.is_symlink() or not auth.is_file() or auth.stat().st_mode & 0o077:
            raise ValueError()
    except (KeyError, OSError, ValueError, TypeError):
        raise PolicyError('generation_bridge_authentication_required', 409) from None


class ValidationBridge:
    def __init__(self, runtime, profile, root, changed):
        check_bridge_runtime(runtime)
        self.runtime, self.profile, self.root = runtime, profile, Path(root)
        self.processes = {}
        self.changed = changed
        self.runner, self.socket = None, None

    async def start(self):
        token_file = self.root / 'completion-token'
        private_write(token_file, secrets.token_urlsafe(48).encode())
        args = SimpleNamespace(token_file=token_file, model=self.profile['model'],
                               codex_executable=Path(self.runtime['codex_executable']),
                               auth_home=Path(self.runtime['auth_home']), timeout=90,
                               max_concurrency=1, max_inflight=1,
                               workspace_root=self.root, diagnostics_dir=self.root / 'diagnostics',
                               owned_processes=self.processes, ownership_changed=self.changed)
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
