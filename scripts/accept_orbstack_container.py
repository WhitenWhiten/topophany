#!/usr/bin/env python3
"""Real-service acceptance inside the isolated OrbStack lab, never on macOS.

Invoked by accept_orbstack.py. Processes, keys and network interfaces created
here belong only to the disposable runner container. No synthetic HTTP
responses replace vendor APIs. Saved evidence contains adapter observations
and the specific metric values used for independent comparisons.
"""
from __future__ import annotations

import argparse
import base64
import datetime as dt
import hashlib
import json
import math
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = Path('/lab')
TOPO = ROOT / 'bin/topo'
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))
RESULTS: list[dict] = []
SECRETS: list[str] = []
PROCESSES: list[subprocess.Popen] = []


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def request(origin, path, *, method='GET', data=None, token='', bearer=True):
    headers = {}
    if token:
        headers['Authorization'] = ('Bearer ' if bearer else '') + token
    if data is not None:
        headers['Content-Type'] = 'application/json'
    req = urllib.request.Request(origin + path, headers=headers, method=method,
                                 data=None if data is None else json.dumps(data).encode())
    try:
        response = OPENER.open(req, timeout=5)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read(2 * 1024 * 1024 + 1)
        check(len(raw) <= 2 * 1024 * 1024, 'upstream response budget')
        try:
            value = json.loads(raw)
        except json.JSONDecodeError:
            value = None
        return response.status, value


def ok(origin, path, **kwargs):
    status, value = request(origin, path, **kwargs)
    detail = str(value.get('error', value.get('message', '')))[:120] if isinstance(value, dict) else ''
    for token in SECRETS:
        detail = detail.replace(token, '[redacted]')
    check(200 <= status < 300, f'{path.split("?")[0]} returned HTTP {status}: {detail}')
    return value


def wait(fn, label, timeout=45):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            value = fn()
            if value:
                return value
        except (OSError, urllib.error.URLError, AssertionError):
            pass
        time.sleep(.5)
    raise AssertionError(label + ' timed out')


def command(args, *, env=None, timeout=30, stdin=None):
    result = subprocess.run([str(x) for x in args], capture_output=True, text=True,
                            timeout=timeout, input=stdin, env=env)
    diagnostic = result.stderr.strip()[:1000]
    for value in SECRETS:
        diagnostic = diagnostic.replace(value, '[redacted]')
    check(result.returncode == 0,
          f'{Path(str(args[0])).name} failed ({result.returncode}): {diagnostic}')
    return result.stdout


def start(args, name, *, env=None):
    log = open(ROOT / f'{name}.log', 'ab')
    process = subprocess.Popen([str(x) for x in args], stdout=log, stderr=log, env=env)
    log.close()
    PROCESSES.append(process)
    return process


def stop(process):
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


def secret(value):
    SECRETS.append(value)
    return value


def record(name, observation=None, **evidence):
    value = {'name': name, 'passed': True, **evidence}
    if observation is not None:
        value['observation'] = observation
    RESULTS.append(value)
    print('PASS ' + name, flush=True)


def once(checks, *, sources=None, token='', observer='lab', paths=None):
    config = {
        'schema': 1, 'project': 'isolated-adapter-lab',
        'nodes': [{'id': 'host', 'name': 'Disposable container', 'kind': 'host'}],
        'observers': [{'id': observer, 'node': 'host', 'context': 'OrbStack container only'}],
        'checks': [],
    }
    for index, item in enumerate(checks):
        config['checks'].append({
            'id': f'check-{index}', 'name': 'Acceptance', 'observer': observer,
            'interval_seconds': 30, 'timeout_seconds': 8, 'stale_seconds': 90, **item,
        })
    if sources:
        config['sources'] = sources
    if paths:
        config['paths'] = paths
    cfg = Path(f'/tmp/adapter-lab-{os.getpid()}.json')
    cfg.write_text(json.dumps(config))
    environment = dict(os.environ, TOPO_LAB_TOKEN=token)
    raw = command([TOPO, 'once', '--config', cfg, '--observer', observer],
                  env=environment, timeout=40)
    check(not any(s in raw for s in SECRETS if s), 'credential in CLI output')
    observations = [json.loads(line) for line in raw.splitlines() if line.strip()]
    check(len(observations) == len(checks), 'missing CLI observation')
    return observations


def runtime(kind, adapter, *, target='', outcome='ok', code=None):
    obs = once([{'kind': kind, 'adapter': adapter, **({'target': target} if target else {})}])[0]
    check(obs['outcome'] == outcome, f'{kind}: expected {outcome}, got {obs.get("outcome")}/{obs.get("code")}')
    if code is not None:
        check(obs.get('code') == code, f'{kind}: expected code {code}, got {obs.get("code")}')
    check(obs.get('connectivity') is False and obs.get('evidence') == 'reported',
          f'{kind}: runtime statement became connectivity evidence')
    return obs


def external(adapter, origin, object_id, token='', *, status=False, outcome='ok', code=None):
    source = {
        'id': 'vendor', 'adapter': adapter, 'observer': 'lab', 'target': origin,
        'interval_seconds': 30, 'timeout_seconds': 8, 'stale_seconds': 180,
        'max_response_bytes': 1048576, 'max_pages': 2, 'max_objects': 1,
        'max_report_bytes': 65536,
    }
    if token:
        source['credential_env'] = 'TOPO_LAB_TOKEN'
    check_item = {'kind': 'external', 'stale_seconds': 180, 'external': {
        'source': 'vendor', 'object': object_id, 'report': 'status' if status else 'resource',
        'node': 'host', 'max_age_seconds': 180,
        **({} if status else {'primary_disk': 'root' if adapter == 'beszel' else 'aggregate'}),
    }}
    obs = once([check_item], sources=[source], token=token)[0]
    check(obs['outcome'] == outcome,
          f'{adapter}: expected {outcome}, got {obs.get("outcome")}/{obs.get("code")}')
    if code:
        check(obs.get('code') == code, f'{adapter}: expected code {code}, got {obs.get("code")}')
    check(obs.get('connectivity') is False and obs.get('evidence') == 'reported',
          f'{adapter}: source statement became connectivity evidence')
    if outcome == 'error':
        check(not obs.get('external', {}).get('report'), 'error leaked a resource report')
    else:
        check(obs['external']['contact'] is True, 'successful API contact missing')
    return obs


def report(obs):
    value = obs['external']['report']
    check(value['time_basis'] == 'upstream_sample', 'sample time basis')
    return value


def save_source(adapter, origin, object_id, token, rep, **extra):
    path = ROOT / ('credentials-' + adapter + '.json')
    path.write_text(json.dumps({'adapter': adapter, 'origin': origin, 'object': object_id,
                               'token': token, 'sample_id': rep['sample_id'],
                               'observed_at': rep['observed_at'], **extra}))
    path.chmod(0o600)


def near(actual, expected, label):
    check(actual is not None and math.isclose(actual, expected, abs_tol=1e-6, rel_tol=1e-12),
          label + ' mapping differs from the vendor response')


def compare_extra(resource, raw, kind):
    if kind == 'beszel':
        net = raw.get('b')
        if net:
            expected = (net[1], net[0])
        else:
            expected = tuple(raw[k] * 2**20 if k in raw else None for k in ('nr', 'ns'))
        load = raw.get('la')
        swap = raw.get('swap') or ([round(raw['s'] * 2**30), round(raw.get('su', 0) * 2**30)]
                                   if 's' in raw else [None, None])
    elif kind == 'komari':
        expected = (raw['network'].get('down', 0), raw['network'].get('up', 0))
        load = [raw.get('load', {}).get(k, 0) for k in ('load1', 'load5', 'load15')]
        swap = [raw['swap'].get(k, 0) for k in ('total', 'used')]
    else:
        expected = (float(raw['net_in_speed']), float(raw['net_out_speed']))
        load = [float(v) for v in raw['load_avg'].split()]
        swap = [round(float(raw[k]) * 2**20) for k in ('swap_total', 'swap_used')]
    network = resource.get('network', {})
    for key, value in zip(('rx_bytes_per_second', 'tx_bytes_per_second'), expected):
        if value is None:
            check(key not in network, kind + ' invented an unavailable network rate')
        else:
            near(network.get(key), value, kind + ' ' + key)
    if load:
        for key, value in zip(('avg_1m', 'avg_5m', 'avg_15m'), load):
            near(resource.get('load', {}).get(key), value, kind + ' ' + key)
    for key, value in zip(('total_bytes', 'used_bytes'), swap):
        check(resource.get('swap', {}).get(key) == value, kind + ' swap byte mapping')


def beszel():
    origin = 'http://127.0.0.1:8090'
    wait(lambda: request(origin, '/api/health')[0] == 200, 'Beszel Hub')
    password = secret('Aa1-' + secrets.token_urlsafe(24))
    email = 'lab@example.invalid'
    ok(origin, '/api/beszel/create-user', method='POST', data={'email': email, 'password': password})
    auth = ok(origin, '/api/collections/users/auth-with-password', method='POST',
              data={'identity': email, 'password': password})
    token = secret(auth['token'])
    info = ok(origin, '/api/beszel/info', token=token, bearer=False)
    system = ok(origin, '/api/collections/systems/records', method='POST',
                token=token, bearer=False,
                data={'name': 'Real container Agent', 'host': '127.0.0.1', 'port': '45876',
                      'users': [auth['record']['id']]})
    object_id = system['id']
    agent = start(['beszel-agent', '-l', ':45876', '-k', info['key']], 'beszel-agent',
                  env=dict(os.environ, SKIP_GPU='true', SMART_INTERVAL='0'))
    query = urllib.parse.urlencode({
        'filter': f'system = "{object_id}" && type = "1m"', 'sort': '-created,-id',
        'page': 1, 'perPage': 1, 'skipTotal': 'true', 'fields': 'id,system,type,created,stats',
    })
    def latest():
        values = ok(origin, '/api/collections/system_stats/records?' + query,
                    token=token, bearer=False)['items']
        return values[0] if values else None
    wait(latest, 'real Beszel Agent telemetry', timeout=100)
    # Pause ingestion, retaining the actual Agent-collected immutable record.
    ok(origin, f'/api/collections/systems/records/{object_id}', method='PATCH',
       token=token, bearer=False, data={'status': 'paused'})
    stop(agent)
    raw = latest()
    obs = external('beszel', origin, object_id, token)
    rep, stats = report(obs), raw['stats']
    check(rep['sample_id'] == raw['id'], 'Beszel record identity')
    check(dt.datetime.fromisoformat(rep['observed_at'].replace('Z', '+00:00')) ==
          dt.datetime.fromisoformat(raw['created'].replace('Z', '+00:00')), 'Beszel record time')
    res = rep['resource']
    near(res['cpu']['percent'], stats['cpu'], 'Beszel CPU')
    near(res['memory']['percent'], stats['mp'], 'Beszel RAM percent')
    for key, old, index in [('total_bytes', 'm', 0), ('used_bytes', 'mu', 1)]:
        expected = stats['mem'][index] if 'mem' in stats else math.floor(stats[old] * 2**30 + .5)
        check(res['memory'][key] == expected, 'Beszel RAM bytes')
    disk = next(v for v in res['disks'] if v['id'] == 'root')
    near(disk['percent'], stats['dp'], 'Beszel disk percent')
    compare_extra(res, stats, 'beszel')
    save_source('beszel', origin, object_id, token, rep)
    record('beszel-real-agent-metrics', obs, upstream_metrics={k: stats[k] for k in (
        'cpu', 'm', 'mu', 'mp', 'mem', 'd', 'du', 'dp', 'b', 'nr', 'ns',
        's', 'su', 'swap', 'la') if k in stats})
    repeat = report(external('beszel', origin, object_id, token))
    check((repeat['sample_id'], repeat['observed_at']) == (rep['sample_id'], rep['observed_at']),
          'Beszel repeat extended freshness')
    record('beszel-repeat-preserves-sample')
    paused = external('beszel', origin, object_id, token, status=True)
    check(paused['external']['report']['status']['status'] == 'unknown', 'Beszel paused status')
    record('beszel-paused-status', paused)
    record('beszel-invalid-token', external('beszel', origin, object_id,
                                          'invalid-lab-token', outcome='error', code='external_object_missing'))


def komari():
    origin = 'http://127.0.0.1:25774'
    wait(lambda: request(origin, '/api/install/status')[0] == 200, 'Komari Server')
    password = secret('Aa1-' + secrets.token_urlsafe(24))
    key = secret(secrets.token_hex(32))
    ok(origin, '/api/install/complete', method='POST', data={
        'username': 'lab', 'password': password, 'sitename': 'Isolated lab',
        'metric_dsn': 'file:/app/data/metrics.db?mode=rwc',
    })
    login = wait(lambda: ok(origin, '/api/login', method='POST',
                           data={'username': 'lab', 'password': password}), 'Komari router reload')
    session = secret(login['data']['set-cookie']['session_token'])
    req = urllib.request.Request(origin + '/api/admin/settings/', method='POST',
        headers={'Cookie': 'session_token=' + session, 'Content-Type': 'application/json'},
        data=json.dumps({'api_key': key, 'private_site': True, 'geo_ip_enabled': False,
                         'geo_ip_provider': 'empty', 'notification_enabled': False}).encode())
    with OPENER.open(req, timeout=5) as response:
        check(response.status == 200, 'Komari settings')
    node = ok(origin, '/api/admin/client/add', method='POST', token=key,
              data={'name': 'Real container Agent'})
    object_id, agent_token = node['uuid'], secret(node['token'])
    agent = start([ROOT / 'bin/komari-agent', '-e', origin, '-t', agent_token,
                   '--disable-auto-update', '--disable-web-ssh', '--get-ip-addr-from-nic',
                   '-i', '1'], 'komari-agent')
    def latest():
        samples = ok(origin, '/api/recent/' + object_id, token=key)['data']
        return samples[-1] if samples else None
    wait(latest, 'real Komari Agent telemetry', timeout=60)
    stop(agent)
    time.sleep(1)
    raw = latest()
    obs = external('komari', origin, object_id, key)
    rep, res = report(obs), report(obs)['resource']
    check(rep['observed_at'] == raw['updated_at'], 'Komari nanosecond receive time')
    near(res['cpu']['percent'], raw['cpu'].get('usage', 0), 'Komari CPU')
    for key_name in ('total', 'used'):
        check(res['memory'][key_name + '_bytes'] == raw['ram'][key_name], 'Komari RAM bytes')
        check(res['disks'][0][key_name + '_bytes'] == raw['disk'][key_name], 'Komari disk bytes')
    compare_extra(res, raw, 'komari')
    save_source('komari', origin, object_id, key, rep, agent_token=agent_token)
    record('komari-real-agent-metrics', obs, upstream_metrics={
        k: raw[k] for k in ('cpu', 'ram', 'disk', 'network', 'swap', 'load', 'updated_at') if k in raw})
    repeat = report(external('komari', origin, object_id, key))
    check((repeat['sample_id'], repeat['observed_at']) == (rep['sample_id'], rep['observed_at']),
          'Komari repeat extended freshness')
    record('komari-repeat-preserves-sample')
    record('komari-invalid-token', external('komari', origin, object_id,
                                          'invalid-lab-token', outcome='error', code='komari_auth'))
    ok(origin, '/api/admin/settings/', method='POST', token=key, data={'private_site': False})
    record('komari-public-anonymous', external('komari', origin, object_id))
    ok(origin, f'/api/admin/client/{object_id}/edit', method='POST', token=key, data={'hidden': True})
    record('komari-hidden-authenticated', external('komari', origin, object_id, key))
    record('komari-hidden-anonymous-denied', external('komari', origin, object_id,
                                                   outcome='error', code='komari_http'))


def cfsm():
    origin = 'http://127.0.0.1:8787'
    auth = json.loads((ROOT / 'auth.json').read_text())
    secret(auth['secret'])
    wait(lambda: request(origin, '/api/config')[0] == 200, 'workerd CF-Server-Monitor')
    def login():
        return ok(origin, '/admin/api', method='POST', data={
            'action': 'login', 'username': 'lab', 'password': auth['secret']}).get('token')
    token = secret(wait(login, 'Worker bootstrap'))
    node = ok(origin, '/admin/api', method='POST', token=token,
              data={'action': 'add', 'name': 'Real container Agent'})
    object_id = node['id']
    ok(origin, '/admin/api', method='POST', token=token,
       data={'action': 'edit', 'id': object_id, 'name': 'Real container Agent',
             'report_interval': 30, 'collect_interval': 1, 'reset_day': 1,
             'connection_mode': 'http', 'ping_mode': 'tcp',
             'auto_update': '0', 'custom_ct': '127.0.0.1:25774', 'custom_cu': '127.0.0.1:25774',
             'custom_cm': '127.0.0.1:25774', 'custom_bd': '127.0.0.1:25774'})
    cfg = Path('/tmp/cfsm-agent.conf')
    cfg.write_text('\n'.join(f'{k}="{v}"' for k, v in {
        'SERVER_ID': object_id, 'SECRET': auth['secret'], 'WORKER_URL': origin + '/update',
        'AUTO_UPDATE': 0, 'REPORT_INTERVAL': 30, 'COLLECT_INTERVAL': 1,
        'CONNECTION_MODE': 'http', 'PING_MODE': 'tcp', 'UA': 'cfsm',
        'CT_NODE': '127.0.0.1:25774', 'CU_NODE': '127.0.0.1:25774',
        'CM_NODE': '127.0.0.1:25774', 'BD_NODE': '127.0.0.1:25774',
    }.items()) + '\n')
    cfg.chmod(0o600)
    agent = start([ROOT / 'bin/cf-probe', 'run', '-config', cfg], 'cfsm-agent')
    def latest():
        value = ok(origin, '/api/server?id=' + object_id)
        return value if value.get('last_updated', 0) > 0 else None
    wait(latest, 'real CFSM Agent persisted telemetry', timeout=90)
    stop(agent)
    time.sleep(1)
    raw = latest()
    obs = external('cf-server-monitor', origin, object_id)
    rep, res = report(obs), report(obs)['resource']
    at = dt.datetime.fromisoformat(rep['observed_at'].replace('Z', '+00:00'))
    check(round(at.timestamp() * 1000) == raw['last_updated'], 'CFSM upstream sample time')
    near(res['cpu']['percent'], raw['cpu'], 'CFSM CPU')
    for target, prefix in ((res['memory'], 'ram'), (res['disks'][0], 'disk')):
        for name in ('total', 'used'):
            check(target[name + '_bytes'] == math.floor(raw[prefix + '_' + name] * 2**20 + .5),
                  'CFSM MiB byte mapping')
    compare_extra(res, raw, 'cfsm')
    save_source('cf-server-monitor', origin, object_id, token, rep)
    record('cfsm-real-worker-and-agent', obs, upstream_metrics={k: raw[k] for k in (
        'cpu', 'cpu_cores', 'ram_total', 'ram_used', 'disk_total', 'disk_used',
        'net_in_speed', 'net_out_speed', 'swap_total', 'swap_used', 'load_avg', 'last_updated') if k in raw})
    repeat = report(external('cf-server-monitor', origin, object_id))
    check((repeat['sample_id'], repeat['observed_at']) == (rep['sample_id'], rep['observed_at']),
          'CFSM repeat extended freshness')
    record('cfsm-repeat-preserves-sample')
    ok(origin, '/admin/api', method='POST', token=token,
       data={'action': 'save_settings', 'settings': {
           'is_public': 'false', 'username': 'lab', 'password': auth['secret']}})
    record('cfsm-private-authenticated', external('cf-server-monitor', origin, object_id, token))
    record('cfsm-private-anonymous-denied', external('cf-server-monitor', origin, object_id,
                                                 outcome='error', code='cfsm_auth'))
    bad_jwt = token.rsplit('.', 1)[0] + '.' + base64.b64encode(bytes(32)).decode().rstrip('=')
    record('cfsm-invalid-token', external('cf-server-monitor', origin, object_id,
                                        bad_jwt, outcome='error', code='cfsm_auth'))


def unavailable():
    sources = list(ROOT.glob('credentials-*.json'))
    check(sources, 'no real resource source was prepared')
    for path in sources:
        saved = json.loads(path.read_text())
        token = secret(saved['token'])
        obs = external(saved['adapter'], saved['origin'], saved['object'], token,
                       outcome='error', code='transport_error')
        check(obs['external']['contact'] is False, 'stopped source refreshed contact')
        record(saved['adapter'] + '-stopped-service', obs)


def recovered():
    for path in ROOT.glob('credentials-*.json'):
        saved = json.loads(path.read_text())
        adapter, origin, object_id = saved['adapter'], saved['origin'], saved['object']
        token = secret(saved['token'])
        wait(lambda: request(origin, '/api/health' if adapter == 'beszel' else
                             '/api/version' if adapter == 'komari' else '/api/config')[0] == 200,
             adapter + ' restart')
        if adapter == 'komari':
            empty = external(adapter, origin, object_id, token, outcome='error', code='external_object_missing')
            check(empty['external']['contact'] is True, 'empty cache lost successful source contact')
            record('komari-restart-clears-recent-window', empty)
            agent_token = secret(saved['agent_token'])
            process = start([ROOT / 'bin/komari-agent', '-e', origin, '-t', agent_token,
                             '--disable-auto-update', '--disable-web-ssh', '--get-ip-addr-from-nic', '-i', '1'],
                            'komari-agent-recovery')
            wait(lambda: ok(origin, '/api/recent/' + object_id, token=token)['data'],
                 'new Komari sample after restart')
            stop(process)
        obs = external(adapter, origin, object_id, token)
        rep = report(obs)
        if adapter == 'komari':
            check(rep['sample_id'] != saved['sample_id'], 'new Agent sample reused old identity')
        else:
            check((rep['sample_id'], rep['observed_at']) == (saved['sample_id'], saved['observed_at']),
                  adapter + ' restart refreshed persisted sample time')
        record(adapter + '-recovered-service', obs)


def mihomo():
    for name, script, extra in (
        ('controller-capacity', 'accept_mihomo.py', []),
        ('network', 'accept_mihomo_network.py', []),
    ):
        output = ROOT / 'evidence' / ('mihomo-' + name + '.json')
        args = ['python3', '/scripts/' + script, '--core', '/usr/local/bin/mihomo',
                '--topo', TOPO, '--output', output, *extra]
        command(args, timeout=100)
        value = json.loads(output.read_text())
        check(value.get('pass') is True, 'Mihomo harness failed')
        record('mihomo-' + name, checks=value['cases'])


def wireguard_setup(side, peer_public, endpoint):
    interface = 'wg-lab'
    private = secret(command(['wg', 'genkey']).strip())
    public = command(['wg', 'pubkey'], stdin=private).strip()
    keyfile = Path('/tmp/wg-lab.key')
    keyfile.write_text(private + '\n'); keyfile.chmod(0o600)
    command(['ip', 'link', 'add', interface, 'type', 'wireguard'])
    command(['wg', 'set', interface, 'private-key', keyfile, 'listen-port', '51820'])
    command(['ip', 'address', 'add', f'10.233.71.{side}/24', 'dev', interface])
    command(['ip', 'link', 'set', interface, 'up'])
    (ROOT / f'wg-public-{side}').write_text(public)
    return public


def wireguard():
    wait(lambda: (ROOT / 'wg-public-2').exists(), 'WireGuard peer key')
    peer_public = (ROOT / 'wg-public-2').read_text().strip()
    my_public = wireguard_setup(1, '', '')
    (ROOT / 'wg-public-1').write_text(my_public)
    command(['wg', 'set', 'wg-lab', 'peer', peer_public, 'allowed-ips', '10.233.71.2/32',
             'endpoint', socket.gethostbyname('peer') + ':51820', 'persistent-keepalive', '5'])
    (ROOT / 'wg-configure-peer').touch()
    wait(lambda: (ROOT / 'wg-peer-ready').exists(), 'WireGuard peer')
    command(['ping', '-c', '2', '-W', '3', '10.233.71.2'])
    options = {'interface': 'wg-lab', 'peer': peer_public,
               'expected_allowed_ips': ['10.233.71.2/32'], 'expected_keepalive_seconds': 5}
    obs = runtime('wireguard', options)
    check(int(obs['facts']['rx_bytes']) > 0 and int(obs['facts']['tx_bytes']) > 0,
          'WireGuard did not report real encrypted traffic')
    check('last_handshake' in obs['facts'], 'WireGuard missing real handshake')
    record('wireguard-real-handshake-and-transfer', obs)
    record('wireguard-allowed-ips-policy', runtime('wireguard', {
        **options, 'expected_allowed_ips': ['10.233.71.3/32']},
        outcome='policy', code='allowed_ips_mismatch'))
    record('wireguard-keepalive-policy', runtime('wireguard', {
        **options, 'expected_keepalive_seconds': 0}, outcome='policy', code='keepalive_mismatch'))
    command(['wg', 'set', 'wg-lab', 'peer', peer_public, 'remove'])
    record('wireguard-missing-peer', runtime('wireguard', options, outcome='unknown', code='peer_not_found'))
    command(['ip', 'link', 'del', 'wg-lab'])
    record('wireguard-missing-interface', runtime('wireguard', options, outcome='error'))


def tailscaled(name):
    Path('/var/run/tailscale').mkdir(parents=True, exist_ok=True)
    return start(['tailscaled', '--tun=ts-lab',
                  '--state=/tmp/tailscale.state', '--socket=/var/run/tailscale/tailscaled.sock'],
                 name, env=dict(os.environ, TS_NO_LOGS_NO_SUPPORT='true'))


def tailscale():
    daemon = tailscaled('tailscaled-runner')
    wait(lambda: Path('/var/run/tailscale/tailscaled.sock').exists(), 'tailscaled socket')
    key = secret((ROOT / 'tailscale-authkey').read_text().strip())
    command(['tailscale', 'up', '--login-server=http://headscale:8080', '--authkey=' + key,
             '--hostname=lab-runner', '--accept-dns=false', '--accept-routes=false',
             '--netfilter-mode=off', '--timeout=25s'])
    def peer():
        peers = json.loads(command(['tailscale', 'status', '--json'])).get('Peer', {})
        return next((x for x in peers.values() if x.get('HostName') == 'lab-peer' and x.get('Online')), None)
    node = wait(peer, 'online Tailscale peer', timeout=60)
    record('tailscale-online', runtime('tailscale', {'peer': node['ID']}))
    # Endpoint discovery and the encrypted WireGuard handshake can lag behind
    # the control plane's Online flag. Establish the dataplane independently
    # before checking the adapter's deliberately single-shot 2-second ping.
    wait(lambda: command(['tailscale', 'ping', '--tsmp', '--c=1', '--timeout=2s',
                          node['TailscaleIPs'][0]]), 'Tailscale encrypted dataplane', timeout=20)
    for diagnostic in ('disco', 'tsmp'):
        obs = runtime('tailscale', {'peer': node['TailscaleIPs'][0], 'tailscale_diagnostic': diagnostic})
        check(obs['facts'].get('diagnostic_response') == 'received', 'Tailscale diagnostic did not reply')
        record('tailscale-' + diagnostic, obs)
    record('tailscale-missing-peer', runtime('tailscale', {'peer': 'absent-peer'},
                                           outcome='unknown', code='peer_not_found'))
    command(['tailscale', 'down'])
    record('tailscale-stopped-backend', runtime('tailscale', {'peer': node['ID']},
                                              outcome='unknown', code='backend_stopped'))
    command(['tailscale', 'up', '--login-server=http://headscale:8080',
             '--hostname=lab-runner', '--accept-dns=false', '--accept-routes=false',
             '--netfilter-mode=off', '--timeout=25s'])
    wait(peer, 'Tailscale recovery')
    record('tailscale-recovered', runtime('tailscale', {'peer': node['ID']}))
    stop(daemon)
    record('tailscale-daemon-unavailable', runtime('tailscale', {'peer': node['ID']}, outcome='error'))


def cloudflared():
    tunnel = str(uuid.uuid4())
    credentials = Path('/tmp/cloudflared-credentials.json')
    credentials.write_text(json.dumps({'AccountTag': '0' * 32, 'TunnelID': tunnel,
        'TunnelSecret': secret(base64.b64encode(secrets.token_bytes(32)).decode())}))
    process = start(['cloudflared', 'tunnel', '--no-autoupdate', '--metrics', '127.0.0.1:49312',
                     '--protocol', 'http2', '--credentials-file', credentials,
                     'run', tunnel], 'cloudflared')
    wait(lambda: request('http://127.0.0.1:49312', '/ready')[0] == 503,
         'real disconnected cloudflared')
    record('cloudflared-zero-edge-sessions', runtime('cloudflared', {}, target='http://127.0.0.1:49312',
                                                    outcome='fail', code='tunnel_not_ready'))
    stop(process)
    record('cloudflared-stopped-connector', runtime('cloudflared', {}, target='http://127.0.0.1:49312',
                                                   outcome='error', code='metrics_unavailable'))


def peer_main():
    wireguard_setup(2, '', '')
    wait(lambda: (ROOT / 'wg-configure-peer').exists(), 'WireGuard runner setup', timeout=180)
    public = (ROOT / 'wg-public-1').read_text().strip()
    command(['wg', 'set', 'wg-lab', 'peer', public, 'allowed-ips', '10.233.71.1/32',
             'endpoint', socket.gethostbyname('runner') + ':51820', 'persistent-keepalive', '5'])
    (ROOT / 'wg-peer-ready').touch()
    daemon = tailscaled('tailscaled-peer')
    wait(lambda: Path('/var/run/tailscale/tailscaled.sock').exists(), 'peer tailscaled socket')
    key = (ROOT / 'tailscale-authkey').read_text().strip()
    command(['tailscale', 'up', '--login-server=http://headscale:8080', '--authkey=' + key,
             '--hostname=lab-peer', '--accept-dns=false', '--accept-routes=false',
             '--netfilter-mode=off', '--timeout=25s'])
    while daemon.poll() is None:
        time.sleep(1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('stage', choices=['beszel', 'komari', 'cfsm', 'mihomo', 'wireguard',
                                         'tailscale', 'cloudflared', 'unavailable', 'recovered', 'peer'])
    args = parser.parse_args()
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(130))
    started = dt.datetime.now(dt.timezone.utc).isoformat()
    passed = False
    error = None
    try:
        if args.stage == 'peer':
            peer_main()
        else:
            globals()[args.stage]()
        passed = True
    except Exception as exc:
        error = str(exc) if isinstance(exc, AssertionError) else type(exc).__name__
        print('FAIL ' + args.stage + ': ' + error, flush=True)
    finally:
        for process in reversed(PROCESSES):
            stop(process)
        if args.stage != 'peer':
            evidence = {'schema': 1, 'stage': args.stage, 'passed': passed,
                        'started_at': started, 'finished_at': dt.datetime.now(dt.timezone.utc).isoformat(),
                        'topo_sha256': hashlib.sha256(TOPO.read_bytes()).hexdigest(), 'cases': RESULTS}
            if error:
                evidence['error'] = error
            raw = json.dumps(evidence, indent=2, ensure_ascii=False) + '\n'
            check(not any(s in raw for s in SECRETS if s), 'credential in evidence')
            (ROOT / 'evidence' / (args.stage + '.json')).write_text(raw)
    return 0 if passed else 1


if __name__ == '__main__':
    sys.exit(main())
