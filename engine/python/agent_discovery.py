"""Read-only discovery shared by capture connections and generation bridges."""
import os
from pathlib import Path
import shutil


DEFAULT_PROFILE_RELPATHS = {
    'codex': '.codex', 'pi': '.pi/agent', 'traex': '.trae', 'trae_ide': '.trae-cn',
    'claude_code': '.claude', 'cursor': '.cursor', 'antigravity': '.gemini/config',
    'opencode': '.config/opencode',
}

COMMANDS = {
    'codex': ('codex',), 'pi': ('pi',), 'traex': ('traecli', 'traex'), 'trae_ide': (),
    'claude_code': ('claude',), 'cursor': ('cursor',), 'opencode': ('opencode',),
    'antigravity': ('agy',),
}

APP_EXECUTABLES = {
    'codex': ('Codex.app/Contents/Resources/codex-cli/bin/codex',
              'ChatGPT.app/Contents/Resources/codex-cli/bin/codex'),
    'antigravity': ('Gemini.app/Contents/Helpers/agy',),
    'trae_ide': ('Trae CN.app/Contents/MacOS/Electron', 'TRAE CN.app/Contents/MacOS/Electron'),
    'cursor': ('Cursor.app/Contents/Resources/app/bin/cursor',),
}


def default_profile(kind, *, home=None, environ=None):
    home = Path.home() if home is None else Path(home)
    environ = os.environ if environ is None else environ
    if kind == 'opencode' and environ.get('XDG_CONFIG_HOME'):
        return str(Path(environ['XDG_CONFIG_HOME']) / 'opencode')
    return str(home / DEFAULT_PROFILE_RELPATHS[kind])


def detect_executable(kind, settings=None, legacy=None, *, home=None, which=None):
    """Resolve an executable file without running it."""
    configured = (settings or {}).get('executable') or legacy
    if configured:
        path = Path(configured)
        return str(path) if path.is_absolute() and path.is_file() and os.access(path, os.X_OK) else None
    home = Path.home() if home is None else Path(home)
    which = shutil.which if which is None else which
    commands = COMMANDS[kind]
    for command in commands:
        found = which(command)
        if found:
            path = Path(found)
            if path.is_file() and os.access(path, os.X_OK):
                return str(path)
    candidates = [base / command for base in (home / '.local/bin', home / '.bun/bin',
                  Path('/opt/homebrew/bin'), Path('/usr/local/bin')) for command in commands]
    candidates.extend(base / relative for relative in APP_EXECUTABLES.get(kind, ())
                      for base in (Path('/Applications'), home / 'Applications'))
    if kind == 'opencode':
        candidates.append(home / '.opencode/bin/opencode')
    return next((str(path) for path in candidates if path.is_file() and os.access(path, os.X_OK)), None)


def resolve_host_runtime(kind, runtime=None, *, home=None, which=None):
    runtime = runtime or {}
    settings = runtime.get('agent_hosts', {}).get(kind, {})
    executable = detect_executable(kind, settings, runtime.get(kind + '_executable'), home=home, which=which)
    directory = (settings.get('profile') or runtime.get('auth_home' if kind == 'codex' else kind + '_auth_home')
                 or default_profile(kind, home=home))
    return {'kind': kind, 'executable': executable, 'home': directory}
