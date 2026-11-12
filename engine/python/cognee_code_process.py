"""Owned subprocess lifecycle for the unchanged official Enola pipeline.

No fact parsing or graph writes live here. Only the private code owner may
provide this runtime; public HTTP/SDK callers cannot select a binary or path.
"""
import asyncio
from dataclasses import dataclass
import hashlib
import json
import math
import os
from pathlib import Path
import signal
import stat
import sys


class CodeProcessError(RuntimeError):
    pass


@dataclass(frozen=True)
class EnolaRuntime:
    binary: str
    sha256: str
    scratch: str
    deny_reads: tuple[str, ...]

    def command(self, repository, command, cwd, timeout, stdin_data):
        if sys.platform != 'darwin':
            raise CodeProcessError('code_process_platform_unsupported')
        if type(timeout) not in (int, float) or not math.isfinite(timeout) or not 0 < timeout <= 600:
            raise CodeProcessError('code_process_timeout_invalid')
        if stdin_data is not None or list(command) != [self.binary, '--generate', repository] or os.fspath(cwd) != repository:
            raise CodeProcessError('code_process_command_denied')
        root, repo, binary = Path(self.scratch), Path(repository), Path(self.binary)
        if any(not p.is_absolute() or p.resolve() != p for p in (root, repo, binary)) or repo.parent != root:
            raise CodeProcessError('code_process_paths_invalid')
        for p in (root, repo):
            info = p.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
                raise CodeProcessError('code_process_paths_invalid')
        if (repo / '.enola').exists() or (repo / '.enola').is_symlink():
            raise CodeProcessError('code_process_output_exists')
        if not self.deny_reads or type(self.deny_reads) is not tuple:
            raise CodeProcessError('code_process_read_boundary_required')
        denied = []
        for path in self.deny_reads:
            p = Path(path)
            if not p.is_absolute() or p.resolve() != p or p == root or p in root.parents or root in p.parents:
                raise CodeProcessError('code_process_read_boundary_invalid')
            denied.append(str(p))
        fd = os.open(binary, os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, 'rb') as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_size > 256 * 1024 * 1024 or info.st_mode & 0o022 or not info.st_mode & 0o111:
                raise CodeProcessError('code_process_binary_unverified')
            digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        if len(self.sha256) != 64 or digest != self.sha256:
            raise CodeProcessError('code_process_binary_unverified')
        home, temporary = root / 'home', root / 'tmp'
        for p in (home, temporary):
            p.mkdir(mode=0o700, exist_ok=False)
        quote = json.dumps
        profile = '(version 1)(allow default)(deny network*)(deny file-write*)'
        profile += '(allow file-write* (subpath ' + quote(str(root)) + '))(allow file-write* (literal "/dev/null"))'
        for path in denied:
            profile += '(deny file-read* (subpath ' + quote(path) + '))'
        # No service credentials, model settings, project environment or
        # ambient user HOME reach the extraction process.
        env = {'PATH': '/usr/bin:/bin', 'HOME': str(home), 'TMPDIR': str(temporary),
               'ENOLA_NO_UPDATE_CHECK': '1', 'ENOLA_NO_PROMPTS': '1'}
        return ['/usr/bin/sandbox-exec', '-p', profile, *command], env


async def run_enola(command, cwd, timeout, subject, stdin_data=None):
    from cognee_code_guard import require
    value = await require(repository=os.fspath(cwd))
    if not isinstance(value.runtime, EnolaRuntime):
        raise CodeProcessError('code_process_runtime_required')
    # No arbitrary command construction by the page, hook or SDK options.
    arguments, env = value.runtime.command(value.repository, command, cwd, timeout, stdin_data)
    await require(repository=value.repository)
    process = None
    readers = []
    waiter = None
    byte_count = 0

    async def drain(stream):
        nonlocal byte_count
        while chunk := await stream.read(8192):
            byte_count += len(chunk)
            if byte_count > 1024 * 1024:
                raise CodeProcessError('code_process_output_limit')

    async def reap():
        # Kill the whole owned group, including descendants retaining pipes,
        # before waiting for the direct child. Never infer exit from a timeout.
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        # A failed output reader may leave its StreamReader paused at the
        # buffer limit. Process.wait can then wait forever for pipe closure
        # even after the child was killed. Drain the remaining killed-child
        # bytes without retaining them before asserting a reaped process.
        for task in readers:
            if not task.done():
                task.cancel()
        await asyncio.gather(*readers, return_exceptions=True)
        async def discard(stream):
            while await stream.read(8192):
                pass
        try:
            await asyncio.wait_for(asyncio.gather(process.wait(), discard(process.stdout), discard(process.stderr)), 5)
        except BaseException:
            raise CodeProcessError('code_process_reap_unconfirmed') from None

    try:
        spawning = asyncio.create_task(asyncio.create_subprocess_exec(*arguments, cwd=str(cwd), env=env,
            stdin=asyncio.subprocess.DEVNULL, stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE, start_new_session=True))
        try:
            process = await asyncio.shield(spawning)
        except BaseException:
            # Cancellation during spawn must still acquire the actual handle
            # before entering the reap path; a cancelled await is not no-child.
            process = await spawning
            raise
        readers = [asyncio.create_task(drain(process.stdout)), asyncio.create_task(drain(process.stderr))]
        waiter = asyncio.create_task(process.wait())
        deadline = asyncio.get_running_loop().time() + timeout
        pending = set([waiter, *readers])
        while pending:
            remaining = deadline - asyncio.get_running_loop().time()
            if remaining <= 0:
                raise CodeProcessError('code_process_timeout')
            done, pending = await asyncio.wait(pending, timeout=min(0.1, remaining), return_when=asyncio.FIRST_COMPLETED)
            for task in done:
                task.result()
            await require(repository=value.repository)
        if process.returncode != 0:
            raise CodeProcessError('code_process_failed')
    finally:
        if process is not None:
            try:
                await reap()
            finally:
                for task in [*readers, waiter]:
                    if task is not None and not task.done():
                        task.cancel()
                await asyncio.gather(*readers, *([waiter] if waiter is not None else []), return_exceptions=True)
