"""Minimal JSON-RPC client for the unbiased-app-engine conformance suite.

Spawns the SUPERVISOR (not the engine directly): every test therefore proves
the whole stack — key resolution, home materialization, exec — not just the
upstream binary. The transport is the documented stdio JSONL framing.
"""
import json
import os
import queue
import subprocess
import threading
import time

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_CMD = os.environ.get(
    "ENGINE_CMD", os.path.join(REPO_ROOT, "bin", "unbiased-app-engine")
)


class Client:
    def __init__(self, cmd=None):
        self.p = subprocess.Popen(
            [cmd or DEFAULT_CMD],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
        )
        self.q = queue.Queue()
        self.rid = 0
        threading.Thread(target=self._reader, daemon=True).start()

    def _reader(self):
        for line in self.p.stdout:
            line = line.strip()
            if line:
                try:
                    self.q.put(json.loads(line))
                except json.JSONDecodeError:
                    pass
        self.q.put(None)

    def close(self):
        self.p.terminate()

    def send(self, msg):
        self.p.stdin.write(json.dumps(msg) + "\n")
        self.p.stdin.flush()

    def request(self, method, params=None):
        self.rid += 1
        self.send({"method": method, "id": self.rid, "params": params or {}})
        return self.rid

    def next_msg(self, timeout=90):
        return self.q.get(timeout=timeout)

    def wait_response(self, rid, timeout=90, on_notification=None, on_server_request=None):
        deadline = time.time() + timeout
        while time.time() < deadline:
            msg = self.next_msg(timeout=deadline - time.time())
            if msg is None:
                raise RuntimeError("engine exited")
            if msg.get("id") == rid and ("result" in msg or "error" in msg):
                if "error" in msg:
                    raise RuntimeError(f"rpc error: {msg['error']}")
                return msg["result"]
            self._dispatch(msg, on_notification, on_server_request)
        raise TimeoutError(f"no response for request {rid}")

    def pump(self, until, timeout=120, on_notification=None, on_server_request=None):
        deadline = time.time() + timeout
        while time.time() < deadline:
            msg = self.next_msg(timeout=deadline - time.time())
            if msg is None:
                raise RuntimeError("engine exited")
            if msg.get("method") == until and "id" not in msg:
                return msg["params"]
            self._dispatch(msg, on_notification, on_server_request)
        raise TimeoutError(f"never saw {until}")

    def _dispatch(self, msg, on_notification, on_server_request):
        if "method" in msg and "id" in msg:  # server-initiated request
            if on_server_request:
                on_server_request(msg)
        elif "method" in msg:  # notification
            if on_notification:
                on_notification(msg)

    def handshake(self):
        rid = self.request(
            "initialize",
            {"clientInfo": {"name": "unbiased_conformance", "title": "Unbiased Conformance", "version": "0.1.0"}},
        )
        result = self.wait_response(rid)
        self.send({"method": "initialized"})
        return result
