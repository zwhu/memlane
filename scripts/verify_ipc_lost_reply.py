"""仅对隔离包的一次真实 IPC 回执丢失做故障注入；不替换业务结果。"""
import hashlib
import json
import os
import socket
import threading
import uuid


class LostIPCReply:
    def __init__(self, path, operation):
        self.path = str(path)
        self.relay = self.path + '.relay-' + uuid.uuid4().hex[:8]
        self.operation = operation
        self.dropped = threading.Event()
        self.stopped = threading.Event()
        self.evidence = {}
        self.workers = []
        self.lock = threading.Lock()

    def __enter__(self):
        os.rename(self.path, self.relay)
        try:
            self.listener = socket.socket(socket.AF_UNIX)
            self.listener.bind(self.path)
            self.listener.listen()
            self.listener.settimeout(.2)
            self.thread = threading.Thread(target=self.run, daemon=True)
            self.thread.start()
            return self
        except BaseException:
            if os.path.exists(self.path): os.unlink(self.path)
            os.rename(self.relay, self.path)
            raise

    def run(self):
        while not self.stopped.is_set():
            try:
                client, _ = self.listener.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            worker = threading.Thread(target=self.forward, args=(client,), daemon=True)
            self.workers.append(worker)
            worker.start()

    def forward(self, client):
        try:
            with client:
                client.settimeout(20)
                with client.makefile('rb') as incoming:
                    request = incoming.readline()
                parsed = json.loads(request)
                with socket.socket(socket.AF_UNIX) as upstream:
                    upstream.settimeout(20)
                    upstream.connect(self.relay)
                    upstream.sendall(request)
                    with upstream.makefile('rb') as response:
                        reply = response.readline()
                with self.lock:
                    if parsed.get('op') == self.operation and not self.dropped.is_set():
                        result = json.loads(reply)
                        assert result.get('ok'), result
                        self.evidence = dict(op=self.operation, actual_committed_response=True,
                                             request_sha256=hashlib.sha256(request).hexdigest(),
                                             response_sha256=hashlib.sha256(reply).hexdigest(),
                                             request=parsed, response=result)
                        self.dropped.set()
                        return
                client.sendall(reply)
        except (OSError, ValueError):
            return

    def __exit__(self, *args):
        self.stopped.set()
        self.listener.close()
        self.thread.join(timeout=2)
        for worker in self.workers: worker.join(timeout=22)
        if any(worker.is_alive() for worker in self.workers):
            raise AssertionError('隔离 IPC 中继尚未结束')
        if os.path.exists(self.path): os.unlink(self.path)
        os.rename(self.relay, self.path)
