#!/usr/bin/env python3
"""Derive the CRD pin from reviewed render.sh output via server dry-run replace.

Uses operator kubectl credentials locally. Does not modify cluster objects.
Never derive the expected schema by trusting the observed CRD spec on first use.
"""
import argparse
import hashlib
import json
import subprocess


def kube(*args, data=None):
    result = subprocess.run(['kubectl', '--request-timeout=30s', *args],
                            input=data, capture_output=True, check=True, timeout=60)
    text = result.stdout.decode().strip()
    decoder, documents = json.JSONDecoder(), []
    while text:
        item, end = decoder.raw_decode(text)
        documents.append(item)
        text = text[end:].lstrip()
    if not documents:
        raise SystemExit('Empty Kubernetes response')
    return documents[0] if len(documents) == 1 else {'items': documents}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', required=True)
    parser.add_argument('--server-version', required=True)
    args = parser.parse_args()
    document = kube('create', '--dry-run=client', '-f', args.manifest, '-o', 'json')
    candidates = [x for x in document.get('items', [document])
                  if x.get('kind') == 'CustomResourceDefinition'
                  and x['metadata']['name'] == 'sandboxes.agents.x-k8s.io']
    if len(candidates) != 1:
        raise SystemExit('Expected exactly one core Sandbox CRD in reviewed manifest')
    desired = candidates[0]
    current = kube('get', 'crd', 'sandboxes.agents.x-k8s.io', '-o', 'json')
    desired['metadata']['resourceVersion'] = current['metadata']['resourceVersion']
    normalized = kube('replace', '--dry-run=server', '-f', '-', '-o', 'json',
                      data=json.dumps(desired).encode())
    # Match Go encoding/json.Marshal's sorted map keys, UTF-8 and HTML escaping.
    raw = json.dumps(normalized['spec'], sort_keys=True, separators=(',', ':'), ensure_ascii=False)
    for char, escaped in [('&', r'\u0026'), ('<', r'\u003c'), ('>', r'\u003e'),
                          ('\u2028', r'\u2028'), ('\u2029', r'\u2029')]:
        raw = raw.replace(char, escaped)
    print(json.dumps({'server_version': args.server_version,
                     'crd_spec_digest': 'sha256:' + hashlib.sha256(raw.encode()).hexdigest(),
                     'controller_namespace': 'agent-sandbox-system',
                     'controller_name': 'agent-sandbox-controller'}, indent=2))


if __name__ == '__main__':
    main()
