"""Parent lifetime guard shared by every launcher-owned service."""
import os
import signal
import threading


def watch_owner(parent=None):
    parent = os.getppid() if parent is None else parent
    stopped = threading.Event()

    def watch():
        while not stopped.wait(.25):
            if os.getppid() != parent:
                os.kill(os.getpid(), signal.SIGTERM)
                return

    threading.Thread(target=watch, name='memlane-owner-watch', daemon=True).start()
    return stopped
