#!/usr/bin/env python3
"""Read-only trusted-worker proof for one live Pod; run with host root access.

This qualification probe is not a production EffectiveVerifier. It intentionally
prints only selected infrastructure facts, never complete CRI specs or arguments.
"""
import argparse
import hashlib
import json
import pathlib
import subprocess


PINS = {
    "qemu": "e81b15b3da14bcbde77be46c322ab97b93c6146f903fe6c6bb8522aa675f66fa",
    "kernel": "a44d663f4ddad20a35527a3578fadef9beb23c1e5cb720e85d6928d6de70d3a1",
    "image": "7ebd652760c881374c0a761d34addcb76d9a650e35c10c01b780ebcdd9a1f2aa",
}


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def sandbox(args):
    result = subprocess.run(["k3s", "crictl", "pods", "--name", args.name, "-o", "json"],
                            check=True, capture_output=True, timeout=30)
    matches = [p for p in json.loads(result.stdout)["items"]
               if p["metadata"]["name"] == args.name
               and p["metadata"]["namespace"] == args.namespace
               and p["metadata"]["uid"] == args.uid and p["state"] == "SANDBOX_READY"]
    require(len(matches) == 1, "expected exactly one ready CRI sandbox for Pod UID")
    require(matches[0]["runtimeHandler"] == args.handler, "unexpected runtime handler")
    return matches[0]["id"]


def inspect_qemu(argv, sandbox_id):
    def value(flag):
        require(argv.count(flag) == 1, "missing or ambiguous QEMU flag: " + flag)
        return argv[argv.index(flag) + 1]
    require(value("-name") == "sandbox-" + sandbox_id, "QEMU sandbox identity mismatch")
    require("accel=kvm" in value("-machine").split(","), "hardware acceleration missing")
    blocks = [argv[i+1] for i, arg in enumerate(argv) if arg == "-blockdev"]
    images = [field.removeprefix("filename=") for block in blocks for field in block.split(",")
              if field.startswith("filename=")]
    require(len(images) == 1, "ambiguous guest image")
    return {"kernel": value("-kernel"), "image": images[0]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for key in ("name", "namespace", "uid", "handler"):
        parser.add_argument("--" + key, required=True)
    args = parser.parse_args()
    identity = sandbox(args)
    candidates = []
    for process in pathlib.Path("/proc").glob("[0-9]*"):
        try:
            argv = process.joinpath("cmdline").read_bytes().decode().rstrip("\0").split("\0")
            if "sandbox-" + identity in argv and "-machine" in argv:
                candidates.append((process, argv))
        except (FileNotFoundError, ProcessLookupError):
            continue
    require(len(candidates) == 1, "expected exactly one matching QEMU process")
    process, argv = candidates[0]
    start = process.joinpath("stat").read_text().split(")", 1)[1].split()[19]
    paths = inspect_qemu(argv, identity)
    paths["qemu"] = str(process / "exe")
    hashes = {}
    for name, path in paths.items():
        with open(path, "rb") as source:
            hashes[name] = hashlib.file_digest(source, "sha256").hexdigest()
        require(hashes[name] == PINS[name], "unqualified artifact: " + name)
    descriptors = []
    for fd in process.joinpath("fd").iterdir():
        try:
            descriptors.append(str(fd.readlink()))
        except FileNotFoundError:
            continue
    require(any("kvm-vm" in fd for fd in descriptors), "no live KVM VM descriptor")
    require(sandbox(args) == identity, "CRI sandbox changed during inspection")
    require(process.joinpath("stat").read_text().split(")", 1)[1].split()[19] == start,
            "QEMU process changed during inspection")
    print(json.dumps({"pod_uid": args.uid, "cri_sandbox_id": identity,
                      "handler": args.handler, "qemu_pid": int(process.name),
                      "kvm_vm_descriptor": True, "artifact_sha256": hashes}, indent=2))


if __name__ == "__main__":
    main()
