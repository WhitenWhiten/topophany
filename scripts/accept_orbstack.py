#!/usr/bin/env python3
"""Run disposable real adapter services in OrbStack, with no macOS networking changes.

Requires Docker Compose, Go and an existing OrbStack Docker context. Downloads
pinned upstream artifacts, builds the production CLI for Linux, executes real
service/Agent acceptance, writes sanitized evidence, then removes only this
run's containers/network/volumes. All runtime networks are internal, no ports
are published, and no host network/PID namespace or Docker socket is mounted.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import io
import json
import os
from pathlib import Path
import re
import secrets
import signal
import subprocess
import sys
import tarfile
import tempfile
import urllib.request

REPO = Path(__file__).resolve().parents[1]
DEPLOY = REPO / 'deploy/adapter-lab'
WORKER_REPO = 'huilang-me/CF-Server-Monitor'
WORKER_COMMIT = '612027cbdb248bc2df7e69f9d01b5cefb4d14fa8'
AGENTS = (
    ('komari-monitor/komari-agent', '1.5.11', 'komari-agent-linux-', 'komari-agent'),
    ('huilang-me/cfsm-agent', 'v1.1.3', 'cf-probe-linux-', 'cf-probe'),
)


def run(args, **kwargs):
    return subprocess.run([str(x) for x in args], capture_output=True, text=True,
                          check=True, **kwargs).stdout


def download(url, limit):
    req = urllib.request.Request(url, headers={'User-Agent': 'Topophany-adapter-acceptance'})
    with urllib.request.urlopen(req, timeout=60) as response:
        data = response.read(limit + 1)
    if len(data) > limit:
        raise ValueError('upstream download exceeded budget')
    return data


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def network_state():
    """Read macOS configuration; ignore volatile route reference/timer fields."""
    route = run(['/sbin/route', '-n', 'get', 'default'])
    return {
        'route': {key: value.strip() for key, value in re.findall(
            r'^\s*(destination|mask|gateway|interface|flags):\s*(.+)$', route, re.M)},
        'dns': run(['/usr/sbin/scutil', '--dns']),
        'proxy': run(['/usr/sbin/scutil', '--proxy']),
        'interfaces': sorted(run(['/sbin/ifconfig', '-l']).split()),
    }


def prepare(root, arch):
    (root / 'bin').mkdir()
    (root / 'evidence').mkdir()
    source = root / 'upstream/cfsm'
    source.mkdir(parents=True)
    data = download(f'https://codeload.github.com/{WORKER_REPO}/tar.gz/{WORKER_COMMIT}', 16 << 20)
    with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as archive:
        prefix = archive.getmembers()[0].name.split('/')[0] + '/'
        for member in archive.getmembers():
            if not member.name.startswith(prefix):
                continue
            member.name = member.name[len(prefix):]
            if member.name:
                archive.extract(member, source, filter='data')
    provenance = {'worker_repository': WORKER_REPO, 'worker_commit': WORKER_COMMIT,
                  'worker_archive_sha256': hashlib.sha256(data).hexdigest(), 'agents': []}
    for repo, version, prefix, destination in AGENTS:
        metadata = json.loads(download(f'https://api.github.com/repos/{repo}/releases/tags/{version}', 2 << 20))
        asset = next(x for x in metadata['assets'] if x['name'] == prefix + arch)
        data = download(asset['browser_download_url'], 64 << 20)
        digest = 'sha256:' + hashlib.sha256(data).hexdigest()
        if asset.get('digest') != digest:
            raise ValueError('official release asset digest mismatch')
        target = root / 'bin' / destination
        target.write_bytes(data); target.chmod(0o700)
        provenance['agents'].append({'repository': repo, 'version': version,
                                     'asset': asset['name'], 'digest': digest})
    environment = dict(os.environ, CGO_ENABLED='0', GOOS='linux', GOARCH=arch)
    run(['go', 'build', '-trimpath', '-o', root / 'bin/topo', './cmd/topo'], cwd=REPO, env=environment)
    provenance['topo_sha256'] = sha256(root / 'bin/topo')
    provenance['git_head'] = run(['git', 'rev-parse', 'HEAD'], cwd=REPO).strip()
    provenance['working_tree_dirty'] = bool(run(['git', 'status', '--porcelain'], cwd=REPO).strip())
    provenance['harness_sha256'] = {str(p.relative_to(REPO)): sha256(p) for p in (
        Path(__file__).resolve(), REPO / 'scripts/accept_orbstack_container.py',
        DEPLOY / 'compose.yaml', DEPLOY / 'Runner.Dockerfile', DEPLOY / 'Worker.Dockerfile',
        DEPLOY / 'worker.mjs', REPO / 'scripts/accept_mihomo.py', REPO / 'scripts/accept_mihomo_network.py')}
    auth = {'secret': secrets.token_hex(32)}
    (root / 'auth.json').write_text(json.dumps(auth))
    (root / 'worker.env').write_text('LAB_SECRET=' + auth['secret'] + '\n')
    (root / 'headscale').mkdir()
    headscale = {
        'server_url': 'http://headscale:8080', 'listen_addr': '0.0.0.0:8080',
        'noise': {'private_key_path': '/var/lib/headscale/noise.key'},
        'prefixes': {'v4': '100.95.0.0/24', 'v6': 'fd7a:115c:a1e0:1234::/64', 'allocation': 'sequential'},
        'database': {'type': 'sqlite', 'sqlite': {'path': '/var/lib/headscale/db.sqlite', 'write_ahead_log': True}},
        'derp': {'server': {'enabled': False}, 'urls': [], 'paths': ['/etc/headscale/derp.yaml'],
                 'auto_update_enabled': False},
        'disable_check_updates': True,
        'dns': {'magic_dns': False, 'base_domain': 'lab.invalid', 'override_local_dns': False},
        'unix_socket': '/var/run/headscale/headscale.sock',
        'unix_socket_permission': '0770', 'logtail': {'enabled': False},
        'policy': {'mode': 'file', 'path': '/etc/headscale/policy.json'}, 'log': {'level': 'warn'},
    }
    (root / 'headscale/config.yaml').write_text(json.dumps(headscale, indent=2))
    (root / 'headscale/policy.json').write_text(json.dumps({
        'acls': [{'action': 'accept', 'src': ['*'], 'dst': ['*:*']}]}))
    (root / 'headscale/derp.yaml').write_text('''regions:
  999:
    regionid: 999
    regioncode: lab
    regionname: Isolated lab
    nodes:
      - name: lab-stun
        regionid: 999
        hostname: headscale
        stunonly: true
        stunport: 3478
''')
    return provenance


def audit_isolation(compose, environment):
    ids = run([*compose, 'ps', '-q'], env=environment).split()
    values = json.loads(run(['docker', '--context', 'orbstack', 'inspect', *ids]))
    audit = []
    for item in values:
        config = item['HostConfig']
        if (config['Privileged'] or config['NetworkMode'] == 'host' or config['PidMode'] == 'host'
                or config['PortBindings'] or any(
                    m['Source'] in ('/', '/proc', '/sys', '/dev') or 'docker.sock' in m['Source']
                    for m in item['Mounts'])):
            raise ValueError('lab isolation contract violated')
        audit.append({'service': item['Config']['Labels']['com.docker.compose.service'],
                      'privileged': False, 'network_mode': config['NetworkMode'],
                      'pid_mode': config['PidMode'], 'published_ports': [],
                      'cap_add': config['CapAdd'] or []})
    networks = {net['NetworkID'] for v in values for net in v['NetworkSettings']['Networks'].values()
                if net.get('NetworkID') and net['NetworkID'] != 'none'}
    for network in networks:
        value = json.loads(run(['docker', '--context', 'orbstack', 'network', 'inspect', network]))[0]
        if value['Name'] != 'none' and not value['Internal']:
            raise ValueError('lab network must be internal')
    return audit


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, help='Directory for sanitized evidence (default: work directory/evidence)')
    parser.add_argument('--adapters', nargs='+', choices=['beszel', 'komari', 'cfsm', 'mihomo',
                        'cloudflared', 'wireguard', 'tailscale'],
                        default=['beszel', 'komari', 'cfsm', 'mihomo', 'cloudflared', 'wireguard', 'tailscale'],
                        help='Optional subset; Tailscale also runs its WireGuard peer prerequisite')
    args = parser.parse_args()
    os.umask(0o077)
    work_parent = REPO / '.verify-build'
    work_parent.mkdir(exist_ok=True)
    root = Path(tempfile.mkdtemp(prefix='adapter-lab-', dir=work_parent)).resolve()
    project = 'topo-adapter-' + secrets.token_hex(4)
    environment = dict(os.environ, LAB_ROOT=str(root), LAB_DEPLOY=str(DEPLOY))
    compose = ['docker', '--context', 'orbstack', 'compose', '-f', str(DEPLOY / 'compose.yaml'), '-p', project]
    summary = {'schema': 1, 'passed': False, 'started_at': dt.datetime.now(dt.timezone.utc).isoformat(),
               'scope': 'Real upstream services and Agents in disposable OrbStack containers',
               'stages': [], 'limitations': [
                   'cloudflared has no external edge session: zero-session and stopped-listener branches only.',
                   'Cloudflare account APIs and CDN header platforms cannot be hosted locally; not certified by this run.',
                   'Tailscale uses a private Headscale control plane and direct container peers; public DERP/peer-relay paths are not certified.',
                   'Agent metrics describe the disposable OrbStack Linux environment, not production hosts.',
               ]}
    before = None
    created = False
    log = open(root / 'runner.log', 'a')
    peer = None
    def execute(*arguments, timeout=120, checked=True):
        result = subprocess.run([*compose, *map(str, arguments)], env=environment,
                                stdout=log, stderr=log, timeout=timeout)
        if checked and result.returncode:
            raise RuntimeError('compose command failed; inspect the private runner.log')
        return result.returncode
    def stage(name):
        print('Testing ' + name, flush=True)
        code = execute('exec', '-T', 'runner', 'python3', '/scripts/accept_orbstack_container.py', name,
                       timeout=200, checked=False)
        value = json.loads((root / 'evidence' / (name + '.json')).read_text())
        passed = code == 0 and value['passed']
        summary['stages'].append({'name': name, 'passed': passed, 'cases': len(value['cases'])})
        print(('PASS ' if passed else 'FAIL ') + name + (': ' + value['error'] if 'error' in value else ''), flush=True)
        if not passed:
            execute('logs', '--no-color', checked=False)
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(130))
    try:
        endpoint = json.loads(run(['docker', 'context', 'inspect', 'orbstack']))[0]['Endpoints']['docker']['Host']
        if '.orbstack/' not in endpoint:
            raise ValueError('orbstack context does not point to OrbStack')
        architecture = run(['docker', '--context', 'orbstack', 'info', '--format', '{{.Architecture}}']).strip()
        arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}[architecture]
        before = network_state()
        (root / 'host-network-before.json').write_text(json.dumps(before))
        print('Preparing pinned services and Linux CLI in ' + str(root), flush=True)
        summary['provenance'] = prepare(root, arch)
        execute('build', timeout=600)
        created = True
        execute('up', '-d')
        summary['container_isolation'] = audit_isolation(compose, environment)
        images = run([*compose, 'images', '-q'], env=environment).split()
        metadata = json.loads(run(['docker', '--context', 'orbstack', 'image', 'inspect', *sorted(set(images))]))
        summary['provenance']['images'] = [{'id': v['Id'], 'tags': v['RepoTags'], 'digests': v['RepoDigests']}
                                             for v in metadata]
        for name in (x for x in ('beszel', 'komari', 'cfsm') if x in args.adapters):
            stage(name)
        resource_services = [x for x in ('beszel', 'komari', 'cfsm') if x in args.adapters]
        if resource_services and all(x['passed'] for x in summary['stages']):
            execute('stop', *resource_services)
            stage('unavailable')
            execute('start', *resource_services)
            stage('recovered')
        for name in (x for x in ('mihomo', 'cloudflared') if x in args.adapters):
            stage(name)
        if 'wireguard' in args.adapters or 'tailscale' in args.adapters:
            user = json.loads(run([*compose, 'exec', '-T', 'headscale', 'headscale', 'users', 'create',
                               'lab', '--output', 'json'], env=environment))
            key = json.loads(run([*compose, 'exec', '-T', 'headscale', 'headscale', 'preauthkeys', 'create',
                              '--user', str(user['id']), '--reusable', '--expiration', '1h',
                              '--output', 'json'], env=environment))
            (root / 'tailscale-authkey').write_text(key['key'])
            peer = subprocess.Popen([*compose, 'exec', '-T', 'peer', 'python3',
                                 '/scripts/accept_orbstack_container.py', 'peer'],
                                env=environment, stdout=log, stderr=log)
            stage('wireguard')
            if 'tailscale' in args.adapters:
                stage('tailscale')
        if 'mihomo' in args.adapters:
            code = execute('exec', '-T', 'tun', 'python3', '/scripts/accept_mihomo_network.py',
                       '--core', '/usr/local/bin/mihomo', '--topo', '/lab/bin/topo',
                       '--output', '/lab/evidence/mihomo-tun.json', '--tun', timeout=120, checked=False)
            value = json.loads((root / 'evidence/mihomo-tun.json').read_text())
            summary['stages'].append({'name': 'mihomo-tun', 'passed': code == 0 and value['pass'],
                                  'cases': len(value['cases'])})
        summary['passed'] = all(x['passed'] for x in summary['stages'])
    except (Exception, KeyboardInterrupt) as exc:
        summary['error'] = type(exc).__name__ + ': ' + str(exc)[:500]
        print('Acceptance interrupted or failed: ' + type(exc).__name__, flush=True)
    finally:
        if created:
            try:
                execute('down', '--volumes', timeout=60)
                summary['containers_removed'] = not run([*compose, 'ps', '-aq'], env=environment).strip()
            except Exception:
                summary['containers_removed'] = False
                summary['passed'] = False
        if peer is not None:
            try:
                peer.wait(timeout=10)
            except subprocess.TimeoutExpired:
                peer.terminate()
                peer.wait(timeout=5)
                summary['passed'] = False
        if before is not None:
            after = network_state()
            (root / 'host-network-after.json').write_text(json.dumps(after))
            summary['macos_network_unchanged'] = {key: before[key] == after[key] for key in before}
            if not all(summary['macos_network_unchanged'].values()):
                summary['passed'] = False
        summary['finished_at'] = dt.datetime.now(dt.timezone.utc).isoformat()
        (root / 'evidence/summary.json').parent.mkdir(exist_ok=True)
        (root / 'evidence/summary.json').write_text(json.dumps(summary, indent=2) + '\n')
        log.close()
        if args.output:
            import shutil
            args.output.mkdir(parents=True, exist_ok=True)
            for path in (root / 'evidence').glob('*.json'):
                shutil.copyfile(path, args.output / path.name)
        print(json.dumps({'passed': summary['passed'], 'evidence': str(args.output or root / 'evidence'),
                          'private_work_directory': str(root)}), flush=True)
    return 0 if summary['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
