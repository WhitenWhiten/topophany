#!/usr/bin/env python3
"""Linux process/PTY acceptance, using only temporary files and loopback sockets.
No production hosts, system services, network routes or proxy policies are touched.
Usage: python3 scripts/smoke.py --binary /absolute/path/to/topo --output report.json
"""
import argparse, contextlib, http.server, json, os, pathlib, platform, re
import select, signal, socket, struct, subprocess, tempfile, threading, time
import urllib.request

from terminal_screen import TerminalScreen


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    binary = str(pathlib.Path(args.binary).resolve())
    output = pathlib.Path(args.output).resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    # XDG_CONFIG_HOME isolates the saved login on Linux only. On macOS Go
    # uses the real user's Library, and the final RSS check also requires /proc.
    # Refuse this harness before creating processes or touching credentials.
    if platform.system() != 'Linux':
        report = {'schema': 1, 'kind': 'real Linux processes + PTY, loopback only',
                  'status': 'skipped', 'checks': [], 'reason': 'Linux-only harness; no processes or credentials touched'}
        output.write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(report))
        return 0
    import fcntl, pty, termios
    results, processes, streams = [], [], []
    start = time.monotonic()
    report = {'schema': 1, 'kind': 'real Linux processes + PTY, loopback only', 'checks': results}

    def passed(name, **extra):
        results.append({'name': name, 'status': 'pass', **extra})

    def wait_for(fn, seconds=8):
        deadline = time.monotonic() + seconds
        last = None
        while time.monotonic() < deadline:
            try:
                value = fn()
                if value:
                    return value
            except (OSError, ValueError, AssertionError) as exc:
                last = type(exc).__name__
            time.sleep(.08)
        raise AssertionError('condition timed out' + (': ' + last if last else ''))

    def stop(p):
        if p.poll() is None:
            p.send_signal(signal.SIGTERM)
            try:
                p.wait(timeout=8)
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait()
                raise AssertionError('process failed graceful shutdown')
        assert p.returncode == 0, f'process exit {p.returncode}'

    def free_port():
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            return sock.getsockname()[1]

    class Target(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            body = b'{"status":"ok"}'
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        def log_message(self, *unused):
            pass

    target = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Target)
    thread = threading.Thread(target=target.serve_forever, daemon=True)
    thread.start()
    master = slave = None
    try:
        with tempfile.TemporaryDirectory(prefix='topophany-smoke-') as root_str:
            root = pathlib.Path(root_str)
            env = dict(os.environ, XDG_CONFIG_HOME=str(root/'client-config'), NO_COLOR='1')
            def run(*argv, input=None):
                return subprocess.run([binary, *argv], input=input, text=True,
                                      capture_output=True, env=env, timeout=12, check=True)
            run('init', '--dir', str(root/'demo'))
            base = root/'demo'
            config = json.loads((base/'topology.json').read_text())
            config['endpoints'][0]['address'] = f'http://127.0.0.1:{target.server_port}/healthz'
            config['checks'][0].update(interval_seconds=1, stale_seconds=3)
            (base/'topology.json').write_text(json.dumps(config))
            urls = [f'http://127.0.0.1:{free_port()}' for _ in range(2)]
            assert urls[0] != urls[1]
            viewer = json.loads((base/'credentials.json').read_text())['profiles']['demo']['servers'][0]['token']
            creds = json.loads((base/'worker-credentials.json').read_text())
            worker_token = creds['profiles']['demo']['servers'][0]['token']
            creds['profiles']['demo']['servers'] = [{'url': u, 'token': worker_token} for u in urls]
            (base/'worker-credentials.json').write_text(json.dumps(creds))
            os.chmod(base/'worker-credentials.json', 0o600)

            def spawn(name, *argv):
                log = open(root/(name+'.log'), 'wb')
                streams.append(log)
                p = subprocess.Popen([binary, *argv], stdout=log, stderr=log, env=env)
                processes.append(p)
                return p
            def serve(index):
                return spawn(f'collector-{index}', 'serve', '--config', str(base/'topology.json'),
                             '--auth', str(base/'auth.json'), '--data', str(root/f'collector-{index}'),
                             '--listen', urls[index].removeprefix('http://'), '--instance', f'collector-{index}')
            collectors = [serve(i) for i in range(2)]
            for url in urls:
                wait_for(lambda u=url: urllib.request.urlopen(u+'/healthz', timeout=1).status == 200)
            passed('two independently stored collector processes start')
            worker_args = ('worker', '--config', str(base/'topology.json'), '--credentials', str(base/'worker-credentials.json'),
                           '--observer', 'demo', '--data', str(root/'worker'))
            worker = spawn('worker', *worker_args)

            def snap(url):
                req = urllib.request.Request(url+'/v1/snapshot', headers={'Authorization': 'Bearer '+viewer})
                with urllib.request.urlopen(req, timeout=1) as resp:
                    return json.load(resp)
            for url in urls:
                wait_for(lambda u=url: snap(u)['checks'][0]['health'] == 'up')
            passed('one worker delivers real HTTP results to both collectors')
            result = run('login', '--server', urls[0], '--project', config['project'], '--token-stdin', input=viewer+'\n')
            assert viewer not in result.stdout and viewer not in result.stderr
            default_path = pathlib.Path(env['XDG_CONFIG_HOME'])/'topophany'/'credentials.json'
            saved = json.loads(default_path.read_text())
            saved['profiles']['default']['servers'].append({'url': urls[1], 'token': viewer})
            default_path.write_text(json.dumps(saved)); os.chmod(default_path, 0o600)
            assert json.loads(run('--json').stdout)['instance'] == 'collector-0'
            assert default_path.stat().st_mode & 0o077 == 0
            passed('verified login, private saved credentials and flag-free default connection')

            master, slave = pty.openpty()
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 34, 140, 0, 0))
            old_term = termios.tcgetattr(slave)
            tty = subprocess.Popen([binary], stdin=slave, stdout=slave, stderr=slave, env=env)
            processes.append(tty)
            captured = bytearray()
            screen = TerminalScreen(140, 34)
            def read_terminal():
                chunk = os.read(master, 65536)
                captured.extend(chunk)
                screen.feed(chunk)

            def drain_until(needle, seconds=8):
                deadline = time.monotonic() + seconds
                while time.monotonic() < deadline:
                    if select.select([master], [], [], .1)[0]:
                        read_terminal()
                    if needle.decode() in screen.text():
                        return True
                    if tty.poll() is not None:
                        raise AssertionError('TUI exited unexpectedly')
                raise AssertionError('TUI did not render expected state: ' + needle.decode())
            drain_until(b'collector-0')
            assert b'\x1b[?1049h' in captured and b'TOPOPHANY' in captured
            for key, text in [('2', b'[2 Topology]'), ('3', b'[3 Servers]'), ('4', b'[4 Services]'), ('5', b'[5 Paths]'),
                              ('6', b'[6 Checks]'), ('7', b'[7 Incidents]'), ('8', b'[8 Observers]'), ('9', b'[9 About]'), ('1', b'[1 Overview]')]:
                os.write(master, key.encode()); drain_until(text, 3)
            os.write(master, b'?'); drain_until(b'appearance of a place', 5)
            os.write(master, b'3e'); drain_until(b'topo edit --config', 3)
            os.write(master, b'\x1b'); time.sleep(.2)
            os.write(master, b'\x1b[<0;2;2M'); drain_until(b'[1 Overview]', 3)
            os.write(master, b'v'); drain_until(b'View: demo', 4)
            os.write(master, b'\x1b[200~qrv\x1b[201~'); time.sleep(.2)
            assert tty.poll() is None
            passed('real PTY: alternate screen, all nine tabs, About, read-only edit hint, mouse, observer switching, safe paste')
            stop(collectors[0])
            os.write(master, b'r'); drain_until(b'collector-1', 8)
            assert json.loads(run('--json').stdout)['instance'] == 'collector-1'
            passed('primary collector loss: live TUI and default JSON client fail over to matching backup')
            stop(worker)
            wait_for(lambda: snap(urls[1])['checks'][0]['health'] == 'unknown', 7)
            now = snap(urls[1])
            assert now['checks'][0]['freshness'] == 'stale'
            assert now['services'][0]['health'] == 'unknown'
            passed('worker stopped: old results become stale/unknown, not remote-host outage')
            os.write(master, b'q')
            deadline = time.monotonic() + 4
            while tty.poll() is None and time.monotonic() < deadline:
                # A PTY master must consume output, just as a real terminal does.
                # Waiting without draining can fill the kernel's PTY buffer.
                if select.select([master], [], [], .1)[0]:
                    read_terminal()
            tty.wait(timeout=1)
            while select.select([master], [], [], .1)[0]:
                read_terminal()
            assert tty.returncode == 0 and b'\x1b[?1049l' in captured and b'\x1b[?25h' in captured
            restored = termios.tcgetattr(slave)
            assert restored == old_term, 'terminal attributes not restored'
            passed('TUI q exits cleanly and restores terminal modes/cursor/alternate screen')
            draft = root/'edited.json'
            editor = subprocess.Popen([binary, 'edit', '--config', str(draft), '--project', 'smoke'], stdin=slave, stdout=slave, stderr=slave, env=env)
            processes.append(editor); tty = editor
            drain_until(b'LOCAL DRAFT', 4)
            os.write(master, b'an'); drain_until(b'New node', 3)
            os.write(master, b'edge\tEdge box\x13'); drain_until(b'Added node edge', 3)
            os.write(master, b'\x13'); drain_until(b'revision', 3)
            os.write(master, b'q'); editor.wait(timeout=4)
            saved_draft = json.loads(draft.read_text())
            assert editor.returncode == 0 and saved_draft['nodes'][0]['id'] == 'edge' and draft.stat().st_mode & 0o077 == 0
            assert run('validate', '--config', str(draft)).returncode == 0
            passed('topo edit: keyboard add + validated atomic private save of a new local topology')
            transcript = re.sub(rb'\x1b\[[0-9;?]*[A-Za-z]', b'', bytes(captured)).decode('utf-8', errors='replace')
            (output.parent/'terminal-transcript-tail.txt').write_text('\n'.join(transcript.splitlines()[-100:])+'\n')
            assert viewer not in transcript and worker_token not in transcript
            collectors[0] = serve(0)
            wait_for(lambda: snap(urls[0])['checks'][0]['freshness'] == 'stale')
            passed('collector restart recovers durable historical evidence without refreshing sample time')
            # Measurements are small-fixture observations, not capacity claims.
            memory = {}
            for idx, p in enumerate(collectors):
                status = pathlib.Path(f'/proc/{p.pid}/status').read_text()
                match = re.search(r'^VmRSS:\s+(\d+)\s+kB', status, re.M)
                if match:
                    memory[f'collector-{idx}'] = int(match.group(1))
            report['demo_rss_kib'] = memory
            for p in collectors: stop(p)
            passed('collectors release exclusive journals on graceful shutdown')
        report['status'] = 'pass'
    except Exception as exc:
        report['status'] = 'fail'
        # No raw subprocess output or credential-bearing arguments in reports.
        report['error'] = f'{type(exc).__name__}: {str(exc)[:200]}'
    finally:
        for p in processes:
            if p.poll() is None:
                p.terminate()
                try: p.wait(timeout=4)
                except subprocess.TimeoutExpired: p.kill(); p.wait()
        for stream in streams: stream.close()
        for fd in (master, slave):
            if fd is not None:
                with contextlib.suppress(OSError): os.close(fd)
        target.shutdown(); target.server_close(); thread.join(timeout=2)
        report['duration_seconds'] = round(time.monotonic()-start, 3)
        output.write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
        print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0 if report['status'] == 'pass' else 1

if __name__ == '__main__':
    raise SystemExit(main())
