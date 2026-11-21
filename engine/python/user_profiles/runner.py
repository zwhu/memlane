"""Subprocess execution and output collection for user profile exports."""
import asyncio
import contextlib
import os
from pathlib import Path
import signal
import tempfile
import time

from project_policy import PolicyError

from .constants import LIMIT, profile_prompt
from .providers import get_provider


_ISOLATED_ENV_PREFIXES = (
    'COGNEE_',
    'MEMLANE_PROJECT_',
    'LLM_',
    'OPENAI_',
    'ANTHROPIC_',
    'GEMINI_',
    'OLLAMA_',
    'FALLBACK_',
)


def build_clean_env() -> dict[str, str]:
    """Return an isolated subprocess env copy without Memlane/Cognee/LLM overrides."""
    env = dict(os.environ, MEMLANE_PROCESSING_SESSION='1')
    # The generator must not inherit the engine's bridge/provider configuration.
    for key in list(env):
        if key.upper().startswith(_ISOLATED_ENV_PREFIXES):
            env.pop(key, None)
    return env


def command_for(adapter, row, work):
    """Build fixed read-only CLI invocation and isolated environment for a provider."""
    kind = row['kind']
    executable = str(getattr(adapter, 'executable', '') or getattr(adapter, 'config', {}).get('codex_executable', ''))
    provider = get_provider(kind)
    if provider is None or not provider.can_generate or not executable or not Path(executable).is_file():
        raise PolicyError('profile_automatic_unavailable', 409)
    spec = provider.build_command(executable, row, work, profile_prompt(), build_clean_env())
    return spec.args, spec.env, spec.output_file


async def bounded_stderr(stream, activity=None):
    """Drain stderr while retaining a bounded tail and recording activity timestamps."""
    tail = bytearray()
    while chunk := await stream.read(16384):
        tail.extend(chunk)
        if activity:
            activity()
        del tail[:-16384]
    return bytes(tail)


async def generate(adapter, row):
    """Execute the configured agent CLI in a temporary isolated directory and parse output."""
    provider = get_provider(row['kind'])
    if provider is None or not provider.can_generate:
        raise PolicyError('profile_automatic_unavailable', 409)
    with tempfile.TemporaryDirectory(prefix='memlane-user-profile-') as directory:
        work = Path(directory)
        args, env, output = command_for(adapter, row, work)
        process = await asyncio.create_subprocess_exec(
            *args,
            cwd=work,
            env=env,
            stdin=asyncio.subprocess.DEVNULL,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            start_new_session=True,
        )
        started = time.monotonic()
        last_activity = started
        progress = row.get('_progress')

        def activity():
            nonlocal last_activity
            last_activity = time.monotonic()

        stderr_task = asyncio.create_task(bounded_stderr(process.stderr, activity))

        async def collect():
            data = bytearray()
            while chunk := await process.stdout.read(16384):
                data.extend(chunk)
                activity()
                if len(data) > LIMIT:
                    raise PolicyError('profile_output_too_large', 503)
            await process.wait()
            if process.returncode:
                stderr = await stderr_task
                raise provider.parse_failure(process.returncode, bytes(data), stderr)
            return provider.parse_output(bytes(data), output)

        collection = asyncio.create_task(collect())
        exit_seen = None
        try:
            while not collection.done():
                await asyncio.wait({collection}, timeout=1)
                if progress:
                    progress({
                        'elapsed_seconds': round(time.monotonic() - started),
                        'idle_seconds': round(time.monotonic() - last_activity),
                        'process_state': 'running' if process.returncode is None else 'exited',
                    })
                if process.returncode is not None and not collection.done():
                    exit_seen = exit_seen or time.monotonic()
                if exit_seen is not None and time.monotonic() - exit_seen >= 5 and not collection.done():
                    from .errors import ProfileAgentFailure
                    raise ProfileAgentFailure(process.returncode, 'output_pipe_open', 'Agent 已退出，但输出通道仍未关闭')
            return await collection
        finally:
            collection.cancel()
            await asyncio.gather(collection, return_exceptions=True)
            # Own the process group, including any child still holding stdout.
            with contextlib.suppress(ProcessLookupError):
                os.killpg(process.pid, signal.SIGKILL)
            await process.wait()
            await stderr_task
