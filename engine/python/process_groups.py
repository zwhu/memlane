"""Confirm a process group from the kernel, including macOS exit races."""
import os
import subprocess


def group_exists(pid):
    try:
        os.killpg(pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        rows = subprocess.check_output(['ps', '-axo', 'pgid='], text=True)
        return any(row.strip() == str(pid) for row in rows.splitlines())
