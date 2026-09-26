#!/usr/bin/env python3
"""Render an isolated, credential-free Agent Sandbox qualification probe."""
import argparse
from datetime import datetime, timedelta, timezone
import json
import re

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--image', required=True, help='locally imported image@sha256:manifest')
parser.add_argument('--namespace', required=True, help='fresh namespace, retained for evidence')
parser.add_argument('--node', required=True)
parser.add_argument('--arch', choices=['amd64', 'arm64'], default='arm64')
parser.add_argument('--runtime-class', default='kata-qemu-runtime-rs-ar331')
parser.add_argument('--part', choices=['infrastructure', 'sandbox', 'all'], default='all')
a = parser.parse_args()
for value in (a.namespace, a.node, a.runtime_class):
    if not re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', value):
        parser.error('namespace, node and runtime class must be DNS labels')
if not re.fullmatch(r'[a-zA-Z0-9./:_-]+@sha256:[0-9a-f]{64}', a.image):
    parser.error('immutable image manifest/index digest required')
security = {'runAsNonRoot': True, 'runAsUser': 65532, 'runAsGroup': 65532,
            'fsGroup': 65532, 'seccompProfile': {'type': 'RuntimeDefault'}}
container = {
    'name': 'probe', 'image': a.image, 'imagePullPolicy': 'Never',
    'securityContext': {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True,
                        'capabilities': {'drop': ['ALL']}},
    'resources': {'requests': {'cpu': '250m', 'memory': '512Mi'},
                  'limits': {'cpu': '2', 'memory': '1Gi'}},
    'volumeMounts': [{'name': 'scratch', 'mountPath': '/tmp'},
                     {'name': 'workspace', 'mountPath': '/workspace'}],
}
pod = {'runtimeClassName': a.runtime_class, 'automountServiceAccountToken': False,
       'enableServiceLinks': False, 'restartPolicy': 'Never', 'activeDeadlineSeconds': 900,
       'terminationGracePeriodSeconds': 5, 'securityContext': security,
       'nodeSelector': {'kubernetes.io/hostname': a.node, 'kubernetes.io/arch': a.arch},
       'containers': [container],
       'volumes': [{'name': name, 'emptyDir': {'medium': 'Memory', 'sizeLimit': '128Mi'}}
                   for name in ('scratch', 'workspace')]}
items = [
    {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': a.namespace,
      'labels': {'pod-security.kubernetes.io/enforce': 'restricted'}}},
    {'apiVersion': 'networking.k8s.io/v1', 'kind': 'NetworkPolicy',
     'metadata': {'name': 'deny-all', 'namespace': a.namespace},
     'spec': {'podSelector': {}, 'policyTypes': ['Ingress', 'Egress']}},
    {'apiVersion': 'agents.x-k8s.io/v1beta1', 'kind': 'Sandbox',
     'metadata': {'name': 'codex-probe', 'namespace': a.namespace},
     'spec': {'operatingMode': 'Running', 'service': False,
              'shutdownTime': (datetime.now(timezone.utc) + timedelta(minutes=20)).strftime('%Y-%m-%dT%H:%M:%SZ'),
              'shutdownPolicy': 'Retain',
              'podTemplate': {'metadata': {'labels': {'app': 'cdx016'}}, 'spec': pod}}},
]
if a.part == 'infrastructure':
    items = items[:2]
elif a.part == 'sandbox':
    items = items[2:]
print(json.dumps({'apiVersion': 'v1', 'kind': 'List', 'items': items}, indent=2))
