"""The opt-in lab must reject any configuration that can escape its network."""
import copy
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
from accept_orbstack import audit_isolation


class IsolationTests(unittest.TestCase):
    def setUp(self):
        self.container = {
            'HostConfig': {'Privileged': False, 'NetworkMode': 'lab', 'PidMode': '',
                           'PortBindings': {}, 'CapAdd': ['NET_ADMIN']},
            'Mounts': [{'Source': '/tmp/disposable-lab', 'Destination': '/lab'}],
            'Config': {'Labels': {'com.docker.compose.service': 'runner'}},
            'NetworkSettings': {'Networks': {'lab': {'NetworkID': 'lab-id'}}},
        }
        self.network = {'Name': 'lab', 'Internal': True}

    def audit(self):
        def output(args, **kwargs):
            if args[-2:] == ['ps', '-q']:
                return 'container-id\n'
            if args[-2:] == ['inspect', 'container-id']:
                return json.dumps([self.container])
            if args[-3:] == ['network', 'inspect', 'lab-id']:
                return json.dumps([self.network])
            self.fail('unexpected inspection command')
        with patch('accept_orbstack.run', side_effect=output):
            return audit_isolation(['docker', '--context', 'orbstack', 'compose'], {})

    def test_isolated_capability_is_allowed(self):
        self.assertEqual(self.audit()[0]['published_ports'], [])

    def test_host_namespaces_privilege_and_port_publication_are_refused(self):
        for key, value in [('NetworkMode', 'host'), ('PidMode', 'host'),
                           ('Privileged', True),
                           ('PortBindings', {'80/tcp': [{'HostPort': '8080'}]})]:
            with self.subTest(key=key):
                previous = copy.deepcopy(self.container['HostConfig'])
                self.container['HostConfig'][key] = value
                with self.assertRaises(ValueError):
                    self.audit()
                self.container['HostConfig'] = previous

    def test_host_filesystem_and_docker_socket_are_refused(self):
        for path in ['/', '/proc', '/sys', '/dev', '/var/run/docker.sock',
                     '/Users/example/.orbstack/run/docker.sock']:
            with self.subTest(path=path):
                self.container['Mounts'][0]['Source'] = path
                with self.assertRaises(ValueError):
                    self.audit()

    def test_network_with_external_egress_is_refused(self):
        self.network['Internal'] = False
        with self.assertRaises(ValueError):
            self.audit()


if __name__ == '__main__':
    unittest.main()
