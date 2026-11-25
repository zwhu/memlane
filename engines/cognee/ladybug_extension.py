"""Prepare the offline Ladybug JSON extension shipped in the pinned Cognee wheel."""

import hashlib
from importlib.metadata import distribution, version
import os
from pathlib import Path
import platform
import stat
import sys
import tempfile

from . import private_write


LADYBUG_VERSION = '0.19.0'
PLATFORM = 'osx_arm64'
BINARY = 'libjson.lbug_extension'
SIZE = 630440
SHA256 = 'e6482a025de31d662ee4b32a39c991706b8cc3c2ec49673ef5d8ce45cc23ce7a'


def verified_binary(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size != SIZE:
            raise RuntimeError('ladybug_json_extension_invalid_file')
        data = stream.read(SIZE + 1)
    if hashlib.sha256(data).hexdigest() != SHA256:
        raise RuntimeError('ladybug_json_extension_digest_mismatch')
    return data


def prepare_json_extension(home):
    if sys.platform != 'darwin' or platform.machine() != 'arm64' or version('ladybug') != LADYBUG_VERSION:
        raise RuntimeError('ladybug_json_extension_unsupported_runtime')
    home = Path(home)
    relative = Path('.lbdb', 'extension', LADYBUG_VERSION, PLATFORM, 'json')
    directory = home
    for part in relative.parts:
        directory /= part
        directory.mkdir(mode=0o700, exist_ok=True)
        if directory.is_symlink() or not directory.is_dir():
            raise RuntimeError('ladybug_json_extension_invalid_directory')
    target = directory / BINARY
    try:
        verified_binary(target)
    except FileNotFoundError:
        source = distribution('cognee').locate_file(
            f'cognee_db_workers/ladybug_extensions/v{LADYBUG_VERSION}/{PLATFORM}/{BINARY}')
        private_write(target, verified_binary(source))


def probe_json_extension():
    import ladybug

    with tempfile.TemporaryDirectory(prefix='memlane-ladybug-') as temporary:
        database = ladybug.Database(str(Path(temporary) / 'probe'), buffer_pool_size=32 * 1024**2)
        try:
            database.init_database()
            connection = ladybug.Connection(database)
            try:
                connection.execute('LOAD EXTENSION JSON;').close()
                result = connection.execute("RETURN json('{}');")
                try:
                    if result.get_next() != ['{}']:
                        raise RuntimeError('ladybug_json_extension_probe_result')
                finally:
                    result.close()
            finally:
                connection.close()
        finally:
            database.close()
