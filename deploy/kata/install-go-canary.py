#!/usr/bin/env python3
"""Generate the UNQUALIFIED Go-runtime diagnostic configuration on worker02.

Requires install-bounded-handler.py first. Does not restart services or select
this handler for AR. Existing differing operator configuration is never replaced.
"""
import pathlib
import re
import tomllib


def main():
    root = pathlib.Path('/opt/kata-ar331/ar-go-bounded')
    base = pathlib.Path('/opt/kata-ar331/share/defaults/kata-containers/ar-bounded-oci.json')
    if not base.is_file():
        raise SystemExit('Install the bounded OCI base specification first')
    source = pathlib.Path('/opt/kata-ar331/share/defaults/kata-containers/configuration-qemu.toml')
    rendered = source.read_text().replace('/opt/kata/', '/opt/kata-ar331/')
    overrides = {
        'path': '"/opt/kata-ar331/bin/qemu-system-aarch64-ar331"',
        'valid_hypervisor_paths': '["/opt/kata-ar331/bin/qemu-system-aarch64-ar331"]',
        'enable_annotations': '[]', 'disable_guest_seccomp': 'false',
        'disable_guest_empty_dir': 'true', 'firmware_volume': '""',
    }
    for key, value in overrides.items():
        rendered, count = re.subn(r'^' + key + r' = .*$', key + ' = ' + value, rendered, flags=re.M)
        if count != 1:
            raise SystemExit('Unexpected source configuration: ' + key)
    tomllib.loads(rendered)
    drop = pathlib.Path('/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/99-thinkpixel-go-bounded.toml')
    config = '''[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.kata-qemu-ar331-bounded]
runtime_type = "io.containerd.kata.v2"
runtime_path = "/opt/kata-ar331/bin/containerd-shim-kata-v2"
privileged_without_host_devices = true
base_runtime_spec = "/opt/kata-ar331/share/defaults/kata-containers/ar-bounded-oci.json"
container_annotations = ["io.kubernetes.container.terminationMessage*"]
[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.kata-qemu-ar331-bounded.options]
ConfigPath = "/opt/kata-ar331/ar-go-bounded/configuration.toml"
'''
    files = ((root / 'configuration.toml', rendered), (drop, config))
    for path, contents in files:
        if path.exists() and path.read_text() != contents:
            raise SystemExit('Refusing to overwrite differing configuration: ' + str(path))
    root.mkdir(exist_ok=True)
    for path, contents in files:
        if not path.exists():
            with path.open('x') as target:
                target.write(contents)
            path.chmod(0o644)
    print('Diagnostic configuration installed; explicit agent restart and qualification required.')


if __name__ == '__main__':
    main()
