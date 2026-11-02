#!/usr/bin/env python3
"""Real core loopback acceptance: HTTP CONNECT, SOCKS5, DNS and outage recovery.

TUN mode requires a new Linux network namespace with loopback enabled, launched
by CI via unshare. No production configuration or controller is accessed.
"""
import argparse
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import platform
import secrets
import select
import socketserver
import socket
import subprocess
import tempfile
import threading
import time
import urllib.request


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--core", required=True, type=Path)
    p.add_argument("--topo", required=True, type=Path)
    p.add_argument("--output", required=True, type=Path)
    p.add_argument("--tun", action="store_true")
    a = p.parse_args()
    if a.tun and (platform.system() != "Linux" or os.readlink('/proc/self/ns/net') == os.readlink('/proc/1/ns/net')):
        p.error("TUN acceptance requires a separate Linux network namespace")
    core, topo = a.core.resolve(), a.topo.resolve()
    report = {"schema": 1, "platform": platform.system(), "machine": platform.machine(), "tun": a.tun,
              "core_sha256": hashlib.sha256(core.read_bytes()).hexdigest(), "topo_sha256": hashlib.sha256(topo.read_bytes()).hexdigest(), "cases": []}
    class Handler(BaseHTTPRequestHandler):
        healthy = True
        def do_GET(self):
            self.send_response(200 if self.healthy else 503)
            self.end_headers()
            self.wfile.write(b'acceptance')
        def log_message(self, *args):
            pass
    class Bridge(socketserver.BaseRequestHandler):
        def handle(self):
            header = b""
            self.request.settimeout(5)
            while not header.endswith(b"\r\n\r\n") and len(header) < 8192:
                part = self.request.recv(1)
                if not part: return
                header += part
            if not header.startswith(b"CONNECT 198.19.0.2:"): return
            with socket.create_connection(("127.0.0.1", server.server_port), timeout=5) as upstream:
                self.request.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
                for _ in range(100):
                    ready, _, _ = select.select([self.request, upstream], [], [], 5)
                    if not ready: return
                    for src in ready:
                        data = src.recv(65536)
                        if not data: return
                        (upstream if src is self.request else self.request).sendall(data)
    bridge = socketserver.ThreadingTCPServer(("127.0.0.1", 0), Bridge)
    bridge.daemon_threads = True
    bridge_thread = threading.Thread(target=bridge.serve_forever, daemon=True)
    bridge_thread.start()
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    def port():
        with socket.socket() as s:
            s.bind(("127.0.0.1", 0))
            return s.getsockname()[1]
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    proc = None
    try:
        with tempfile.TemporaryDirectory(prefix="topophany-network-") as directory:
            root = Path(directory)
            ctl, mixed, dns = port(), port(), port()
            token = secrets.token_hex(24)
            cfg = {"external-controller": f"127.0.0.1:{ctl}", "secret": token, "mixed-port": mixed,
                   "bind-address": "127.0.0.1", "allow-lan": False, "mode": "rule", "log-level": "silent",
                   "tun": {"enable": a.tun, "device": "topo-accept", "stack": "gvisor", "auto-route": True, "auto-detect-interface": False, "route-address": ["198.19.0.2/32"]},
                   "hosts": {"acceptance.test": "127.0.0.1"},
                   "dns": {"enable": True, "listen": f"127.0.0.1:{dns}", "use-hosts": True, "enhanced-mode": "redir-host", "fake-ip-range": "198.18.0.1/16", "nameserver": ["127.0.0.1:9"]},
                   "rules": ["MATCH,DIRECT"]}
            if a.tun:
                cfg["proxies"] = [{"name": "bridge", "type": "http", "server": "127.0.0.1", "port": bridge.server_address[1]}]
                cfg["rules"].insert(0, "IP-CIDR,198.19.0.2/32,bridge,no-resolve")
            (root / "core.json").write_text(json.dumps(cfg))
            with (root / "core.log").open("wb") as log:
                proc = subprocess.Popen([str(core), "-d", str(root), "-f", str(root / "core.json")], stdout=log, stderr=subprocess.STDOUT)
                deadline = time.monotonic() + 20
                while True:
                    try:
                        req = urllib.request.Request(f"http://127.0.0.1:{ctl}/version", headers={"Authorization": "Bearer " + token})
                        with opener.open(req, timeout=1) as r:
                            report["version"] = json.load(r)
                        break
                    except Exception:
                        if proc.poll() is not None or time.monotonic() > deadline:
                            raise RuntimeError("isolated core startup failed")
                        time.sleep(.1)
                base = {"schema": 1, "project": "network-acceptance", "observers": [{"id": "local", "name": "Local", "context": "isolated acceptance", "stale_seconds": 180}]}
                def run(name, kind, target, expected, proxy=None, **extra):
                    check = {"id": "test", "name": name, "observer": "local", "kind": kind, "target": target, "interval_seconds": 60, "timeout_seconds": 5, "stale_seconds": 180, **extra}
                    topology = {**base, "checks": [check]}
                    if proxy:
                        topology["paths"] = [{"id": "core", "name": "Core", "observer": "local", "proxies": [{"kind": proxy, "address": f"127.0.0.1:{mixed}"}]}]
                        check["path"] = "core"
                    (root / "topology.json").write_text(json.dumps(topology))
                    result = subprocess.run([str(topo), "once", "--config", str(root / "topology.json"), "--observer", "local"], capture_output=True, text=True, timeout=15, env=dict(os.environ, TOPO_ACCEPT_CORE_TOKEN=token))
                    if result.returncode:
                        raise RuntimeError("acceptance topology invocation failed")
                    obs = json.loads(result.stdout)
                    if isinstance(obs, list): obs = obs[0]
                    ok = obs["outcome"] == expected and token not in result.stdout
                    report["cases"].append({"name": name, "outcome": obs["outcome"], "code": obs.get("code", ""), "pass": ok})
                target = f"http://127.0.0.1:{server.server_port}/health"
                for proxy in ("socks5", "http-connect"):
                    run(proxy, "http", target, "ok", proxy, http={"status": [200]})
                    Handler.healthy = False
                    run(proxy + "-origin-failure", "http", target, "fail", proxy, http={"status": [200]})
                    Handler.healthy = True
                    run(proxy + "-recovered", "http", target, "ok", proxy, http={"status": [200]})
                run("dns", "dns", f"127.0.0.1:{dns}", "ok", dns={"transport": "udp", "name": "acceptance.test", "type": "A", "expected_ips": ["127.0.0.1"]})
                run("runtime", "mihomo", f"http://127.0.0.1:{ctl}", "ok", adapter={"expected_mode": "rule", "expected_tun": a.tun, "token_env": "TOPO_ACCEPT_CORE_TOKEN"})
                if a.tun:
                    run("tun-capture", "http", f"http://198.19.0.2:{server.server_port}/health", "ok", http={"status": [200]})
                proc.terminate(); proc.wait(timeout=5); proc = None
                run("stopped-core", "http", target, "fail", "socks5", http={"status": [200]})
        report["pass"] = all(c["pass"] for c in report["cases"])
    except Exception as e:
        report.update(error=type(e).__name__ + ": " + str(e))
        report["pass"] = False
    finally:
        if proc is not None:
            proc.terminate()
            try: proc.wait(timeout=5)
            except subprocess.TimeoutExpired: proc.kill(); proc.wait()
        bridge.shutdown(); bridge.server_close(); bridge_thread.join()
        server.shutdown(); server.server_close(); thread.join()
    a.output.parent.mkdir(parents=True, exist_ok=True)
    a.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report))
    return 0 if report["pass"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
