"""Private Cognee runner and isolated installation entry points."""
import errno
import os
from pathlib import Path
import signal
import stat
import tempfile
import threading

ENGINE_NAME = 'cognee'
ENGINE_VERSION = '1.6.7'
COMPONENT_MANIFEST = Path(__file__).resolve().parents[2] / 'engine' / 'coding-memory-components.json'


class PolicyError(Exception):
    def __init__(self, code: str, status: int = 400):
        super().__init__(code)
        self.code = code
        self.status = status


def private_write(path, data):
    path = Path(path).absolute()
    parent = path.parent
    if parent.is_symlink() or path.is_symlink():
        raise OSError(errno.ELOOP, 'private write cannot traverse symlinks', path)
    parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    directory = os.open(parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        if stat.S_IMODE(os.fstat(directory).st_mode) != 0o700:
            raise PermissionError('private destination requires a 0700 directory')
        fd, temporary = tempfile.mkstemp(dir=parent)
        try:
            with os.fdopen(fd, 'wb') as stream:
                os.fchmod(stream.fileno(), 0o600)
                stream.write(data)
                stream.flush()
                os.fsync(stream.fileno())
            if path.is_symlink():
                raise OSError(errno.ELOOP, 'private destination is a symlink', path)
            os.replace(Path(temporary).name, path.name, src_dir_fd=directory, dst_dir_fd=directory)
            os.fsync(directory)
        finally:
            Path(temporary).unlink(missing_ok=True)
    finally:
        os.close(directory)


def read_secret(path):
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(fd, encoding='utf-8') as stream:
            mode = os.fstat(stream.fileno()).st_mode
            if not stat.S_ISREG(mode) or mode & 0o077:
                raise PolicyError('private_credential_required', 503)
            value = stream.read().strip()
    except OSError as error:
        code = 'private_credential_required' if error.errno == errno.ELOOP else 'credential_unavailable'
        raise PolicyError(code, 503) from None
    except UnicodeError:
        raise PolicyError('credential_unavailable', 503) from None
    if not value:
        raise PolicyError('credential_unavailable', 503)
    return value


def watch_owner(parent=None):
    parent = os.getppid() if parent is None else parent
    stopped = threading.Event()

    def watch():
        while not stopped.wait(0.25):
            if os.getppid() != parent:
                os.kill(os.getpid(), signal.SIGTERM)
                return

    threading.Thread(target=watch, name='memlane-owner-watch', daemon=True).start()
    return stopped
