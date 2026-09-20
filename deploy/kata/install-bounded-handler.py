#!/usr/bin/env python3
"""Install a separate Kata handler on the trusted K3s worker; restart is explicit.

Generate OCI defaults with the installed containerd, then add fixed guest limits.
Existing vendor handlers and generated containerd configuration are untouched.
"""
import json
import pathlib
import subprocess

BASE = pathlib.Path('/opt/kata-ar331/share/defaults/kata-containers/ar-bounded-oci.json')
DROP = pathlib.Path('/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/99-thinkpixel-bounded.toml')
CONFIG = '''[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.kata-qemu-runtime-rs-ar331-bounded]
runtime_type = "io.containerd.kata-qemu-runtime-rs-ar331.v2"
runtime_path = "/opt/kata-ar331/runtime-rs/bin/containerd-shim-kata-v2"
privileged_without_host_devices = true
base_runtime_spec = "/opt/kata-ar331/share/defaults/kata-containers/ar-bounded-oci.json"
container_annotations = ["io.kubernetes.container.terminationMessage*"]
[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.kata-qemu-runtime-rs-ar331-bounded.options]
ConfigPath = "/opt/kata-ar331/share/defaults/kata-containers/runtime-rs/runtimes/qemu-runtime-rs/configuration-qemu-runtime-rs.toml"
'''


def main():
    result = subprocess.run(['k3s', 'ctr', 'oci', 'spec'], check=True, capture_output=True, timeout=30)
    spec = json.loads(result.stdout)
    spec['linux'].setdefault('resources', {})['pids'] = {'limit': 128}
    limits = spec['process'].setdefault('rlimits', [])
    limits[:] = [r for r in limits if r['type'] != 'RLIMIT_NPROC']
    limits.append({'type': 'RLIMIT_NPROC', 'hard': 128, 'soft': 128})
    encoded = json.dumps(spec, indent=2) + '\n'
    for path, contents in ((BASE, encoded), (DROP, CONFIG)):
        if path.exists() and path.read_text() != contents:
            raise SystemExit('Refusing to overwrite differing operator configuration: ' + str(path))
    for path, contents in ((BASE, encoded), (DROP, CONFIG)):
        if not path.exists():
            with path.open('x') as target:
                target.write(contents)
            path.chmod(0o644)
    print('Installed. Restart k3s-agent in a maintenance window, then qualify a fresh Pod.')


if __name__ == '__main__':
    main()
