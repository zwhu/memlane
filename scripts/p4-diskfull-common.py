"""Owned APFS attachment and exclusive evidence files for disk fault drivers."""
import json
import os
import plistlib
import re
import subprocess
import time
from pathlib import Path


class Evidence:
    def __init__(self, path):
        self.path = Path(path)
        self.fd = os.open(self.path, os.O_CREAT | os.O_EXCL | os.O_RDWR | os.O_NOFOLLOW, 0o600)

    def write(self, value):
        assert os.path.samestat(os.fstat(self.fd), self.path.lstat()), 'evidence identity changed'
        assert os.fstat(self.fd).st_nlink == 1, 'evidence link count changed'
        data = (json.dumps(value, ensure_ascii=False, indent=2) + '\n').encode()
        os.lseek(self.fd, 0, os.SEEK_SET)
        os.ftruncate(self.fd, 0)
        view = memoryview(data)
        while view:
            count = os.write(self.fd, view)
            assert count > 0, 'short evidence write'
            view = view[count:]
        os.fsync(self.fd)

    def close(self):
        os.close(self.fd)


class OwnedImage:
    def __init__(self, image, mountpoint):
        self.image = Path(image).resolve()
        self.mountpoint = Path(mountpoint).resolve()
        self.attempted = False
        self.detach_retries = []

    def entities(self):
        result = subprocess.run(['hdiutil', 'info', '-plist'], capture_output=True, check=True, timeout=15)
        images = plistlib.loads(result.stdout).get('images', [])
        owned = [i for i in images if Path(i.get('image-path', '/')).resolve() == self.image]
        assert len(owned) <= 1, 'ambiguous owned image mapping'
        return owned[0]['system-entities'] if owned else []

    def attach(self):
        assert not self.entities(), 'owned image is already attached'
        assert self.mountpoint.stat().st_dev == self.mountpoint.parent.stat().st_dev, 'mountpoint already occupied'
        # Even a failed/timed-out attach may create devices. detach() always
        # queries the exact image again rather than relying on this return value.
        self.attempted = True
        result = subprocess.run(['hdiutil', 'attach', '-nobrowse', '-mountpoint', str(self.mountpoint), '-plist', str(self.image)], capture_output=True, timeout=60)
        entities = self.entities()
        assert result.returncode == 0, result.stderr.decode(errors='replace')
        expected = [e for e in entities if Path(e.get('mount-point', '/')).resolve() == self.mountpoint]
        assert len(expected) == 1, 'owned image mountpoint mismatch'
        device = expected[0]['dev-entry']
        assert re.fullmatch(r'/dev/disk[0-9]+s[0-9]+', device), 'unexpected owned volume device'
        assert self.mountpoint.stat().st_dev != self.mountpoint.parent.stat().st_dev, 'image is not mounted'
        return device

    def detach(self):
        for attempt in range(6):
            # A busy device may be detached and its disk number reused between
            # attempts. Resolve this image again before every destructive call.
            entities = self.entities()
            if not entities:
                assert self.mountpoint.stat().st_dev == self.mountpoint.parent.stat().st_dev, 'unidentified occupied mountpoint'
                return
            assert self.attempted, 'attachment existed before this operation; cleanup not authorized'
            physical = [e['dev-entry'] for e in entities if re.fullmatch(r'/dev/disk[0-9]+', e.get('dev-entry', ''))]
            assert physical, 'no verified owned device to detach'
            device = physical[0]
            result = subprocess.run(['hdiutil', 'detach', device], capture_output=True, text=True, timeout=5)
            if result.returncode == 0:
                assert not self.entities(), 'owned image remains attached'
                assert self.mountpoint.stat().st_dev == self.mountpoint.parent.stat().st_dev, 'mountpoint remains occupied'
                return
            self.detach_retries.append({'device': device, 'exit_code': result.returncode, 'stderr': result.stderr})
            if attempt == 5:
                raise RuntimeError(result.stderr)
            time.sleep(.5)
