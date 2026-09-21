#!/usr/bin/env python3
"""Prepare a fresh bounded local scratch PV on a trusted Linux worker.

Print a PV manifest for operator review/application. Never reuse data, alter an
existing filesystem, or grant the workload host access. Linux utilities required.
"""
import argparse
import json
import pathlib
import re
import subprocess


def run(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=60).stdout.strip()


def install_units(image, mount, name):
    unit = run('systemd-escape', '--path', '--suffix=mount', str(mount))
    files = {
        pathlib.Path('/etc/systemd/system') / unit:
            '[Unit]\nDescription=ThinkPixelAR bounded scratch ' + name + '\n'
            '[Mount]\nWhat=' + str(image) + '\nWhere=' + str(mount) + '\n'
            'Type=ext4\nOptions=loop,nodev,nosuid\nTimeoutSec=30\n',
        pathlib.Path('/etc/systemd/system/k3s-agent.service.d') / (name + '.conf'):
            '[Unit]\nRequiresMountsFor=' + str(mount) + '\nBindsTo=' + unit + '\n',
    }
    for path in files:
        if path.exists():
            raise SystemExit('Refusing existing systemd configuration: ' + str(path))
    for path, contents in files.items():
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open('x') as target:
            target.write(contents)
        path.chmod(0o644)
    run('systemctl', 'daemon-reload')
    run('systemctl', 'start', unit)
    run('systemctl', 'is-active', unit)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--name', required=True)
    p.add_argument('--node', required=True)
    p.add_argument('--root', default='/mnt/ssd/thinkpixelar-scratch')
    p.add_argument('--size-mib', type=int, default=32)
    args = p.parse_args()
    if not re.fullmatch(r'ar-scratch-[a-z0-9-]{1,40}', args.name) or not re.fullmatch(r'[a-z0-9.-]{1,63}', args.node):
        raise SystemExit('Invalid PV/node name')
    if args.size_mib != 32:
        raise SystemExit('This qualified StorageClass requires exactly 32 MiB slots')
    root = pathlib.Path(args.root)
    if not root.is_absolute() or root.is_symlink() or not re.fullmatch(r'/[A-Za-z0-9/_-]+', str(root)):
        raise SystemExit('Expected an absolute trusted storage root')
    run('findmnt', '-M', str(root.parent))
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    root = root.resolve(strict=True)
    image, mount = root / (args.name + '.ext4'), root / args.name
    if image.exists() or mount.exists():
        raise SystemExit('Refusing existing backing image or mount; use a fresh name')
    # Reserve physical host space before advertising capacity (no sparse overcommit).
    with image.open('x'):
        pass
    image.chmod(0o600)
    run('fallocate', '-l', str(args.size_mib * 1048576), str(image))
    run('mkfs.ext4', '-q', '-F', '-m', '0', str(image))
    mount.mkdir(mode=0o000)
    run('mount', '-o', 'loop,nodev,nosuid', str(image), str(mount))
    mount.chmod(0o700)
    run('chown', '65532:65532', str(mount))
    info = json.loads(run('findmnt', '-J', '-T', str(mount)))['filesystems'][0]
    if info['target'] != str(mount) or info['fstype'] != 'ext4':
        raise SystemExit('Unexpected mount; do not publish PV')
    install_units(image, mount, args.name)
    print(json.dumps({'apiVersion': 'v1', 'kind': 'PersistentVolume',
        'metadata': {'name': args.name}, 'spec': {
            'capacity': {'storage': str(args.size_mib) + 'Mi'},
            'volumeMode': 'Filesystem', 'accessModes': ['ReadWriteOnce'],
            'persistentVolumeReclaimPolicy': 'Retain', 'storageClassName': 'ar-bounded-scratch-v1',
            'local': {'path': str(mount)},
            'nodeAffinity': {'required': {'nodeSelectorTerms': [{'matchExpressions': [{
                'key': 'kubernetes.io/hostname', 'operator': 'In', 'values': [args.node]}]}]}}}}, indent=2))


if __name__ == '__main__':
    main()
