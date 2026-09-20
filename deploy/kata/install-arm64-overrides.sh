#!/bin/sh
# Run as root on an installed ARM64 worker, with the adjacent source files.
set -eu
[ "$(id -u)" = 0 ] || { echo 'run as root' >&2; exit 1; }
[ "$(uname -m)" = aarch64 ] || { echo 'ARM64 required' >&2; exit 1; }
kata_source_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
kata_prefix=/opt/kata-ar331
kata_dropins=$kata_prefix/share/defaults/kata-containers/runtime-rs/runtimes/qemu-runtime-rs/config.d
[ -d "$kata_dropins" ]
"$kata_prefix/runtime-rs/bin/containerd-shim-kata-v2" --version | grep -F 'version: 3.31.0,' >/dev/null
for kata_artifact in bin/qemu-system-aarch64 libexec/virtiofsd share/kata-containers/vmlinux.container share/kata-containers/kata-containers.img share/aavmf/AAVMF_CODE.fd share/kata-qemu/qemu/efi-virtio.rom; do
    [ -r "$kata_prefix/$kata_artifact" ] || { echo 'required Kata artifact absent' >&2; exit 1; }
done
install -m 0755 "$kata_source_dir/qemu-system-aarch64-ar331" "$kata_prefix/bin/qemu-system-aarch64-ar331"
install -m 0644 "$kata_source_dir/90-arm64-paths.toml" "$kata_dropins/90-arm64-paths.toml"
"$kata_prefix/bin/kata-runtime" --kata-config "$kata_prefix/share/defaults/kata-containers/runtime-rs/runtimes/qemu-runtime-rs/configuration-qemu-runtime-rs.toml" check
