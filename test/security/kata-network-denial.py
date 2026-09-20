#!/usr/bin/env python3
"""Run retained, bounded network probes using the operator's local kubectl context.

Requires a matching-architecture networkprobe binary. No credentials are copied
into Pods. This script never deletes test objects or modifies existing policies.
"""
import argparse
import datetime
import json
import pathlib
import subprocess


def kubectl(*args, data=None):
    return subprocess.run(["kubectl", "--request-timeout=30s", *args], input=data,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          check=True, timeout=180).stdout


def apply(obj):
    kubectl("create", "-f", "-", data=json.dumps(obj).encode())


def pod(name, namespace, node, image, runtime=None, server=False):
    spec = {
        "automountServiceAccountToken": False, "enableServiceLinks": False,
        "restartPolicy": "Never", "activeDeadlineSeconds": 900,
        "terminationGracePeriodSeconds": 1,
        "nodeSelector": {"kubernetes.io/hostname": node},
        "securityContext": {"runAsNonRoot": True, "runAsUser": 65532,
                            "runAsGroup": 65532, "seccompProfile": {"type": "RuntimeDefault"}},
        "volumes": [{"name": "tmp", "emptyDir": {"sizeLimit": "32Mi"}}],
        "containers": [{"name": "probe", "image": image,
            "command": ["sh", "-ec", "exec httpd -f -p 8080 -h /tmp" if server else "sleep 850"],
            "securityContext": {"allowPrivilegeEscalation": False,
                                "readOnlyRootFilesystem": True, "capabilities": {"drop": ["ALL"]}},
            "volumeMounts": [{"name": "tmp", "mountPath": "/tmp"}],
            "resources": {"requests": {"cpu": "250m", "memory": "256Mi", "ephemeral-storage": "64Mi"},
                          "limits": {"cpu": "1", "memory": "512Mi", "ephemeral-storage": "128Mi"}}}]
    }
    if runtime:
        spec["runtimeClassName"] = runtime
    return {"apiVersion": "v1", "kind": "Pod", "metadata": {
        "name": name, "namespace": namespace, "labels": {"probe-role": name}}, "spec": spec}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--probe", required=True, type=pathlib.Path)
    parser.add_argument("--runtime-class", required=True)
    parser.add_argument("--node", required=True)
    args = parser.parse_args()
    binary = args.probe.read_bytes()
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d%H%M%S")
    namespaces = {k: "ar-net-" + k + "-" + stamp for k in ("deny", "control", "targets")}
    image = "docker.io/library/busybox@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0"
    for ns in namespaces.values():
        apply({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": ns,
               "labels": {"pod-security.kubernetes.io/enforce": "restricted"}}})
    # Policy precedes Pod creation and selects every Pod in the isolated namespace.
    apply({"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
           "metadata": {"name": "offline-probe", "namespace": namespaces["deny"]},
           "spec": {"podSelector": {}, "policyTypes": ["Ingress", "Egress"],
                    "egress": [{"to": [{"namespaceSelector": {"matchLabels": {
                        "kubernetes.io/metadata.name": namespaces["targets"]}},
                        "podSelector": {"matchLabels": {"probe-role": "allowed"}}}],
                        "ports": [{"protocol": "TCP", "port": 8080}]}]}})
    for name in ("allowed", "forbidden"):
        apply(pod(name, namespaces["targets"], args.node, image, server=True))
    for lane in ("control", "deny"):
        apply(pod("probe", namespaces[lane], args.node, image, args.runtime_class))
    for ns in namespaces.values():
        kubectl("-n", ns, "wait", "--for=condition=Ready", "pod", "--all", "--timeout=150s")
    targets = {}
    for name in ("allowed", "forbidden"):
        item = json.loads(kubectl("-n", namespaces["targets"], "get", "pod", name, "-o", "json"))
        targets[name] = item["status"]["podIP"] + ":8080"
    service = json.loads(kubectl("-n", "default", "get", "svc", "kubernetes", "-o", "json"))
    targets["api-service"] = service["spec"]["clusterIP"] + ":443"
    endpoints = json.loads(kubectl("-n", "default", "get", "endpointslices",
                                  "-l", "kubernetes.io/service-name=kubernetes", "-o", "json"))
    for index, item in enumerate(endpoints["items"]):
        for endpoint in item["endpoints"]:
            for address in endpoint["addresses"]:
                host = "[" + address + "]" if ":" in address else address
                targets["api-direct-" + str(index) + "-" + address] = host + ":" + str(item["ports"][0]["port"])
    targets["metadata-v4"] = "169.254.169.254:80"
    results = {"namespaces": namespaces, "node": args.node, "targets": targets, "checks": {}}
    for lane in ("control", "deny"):
        ns = namespaces[lane]
        kubectl("-n", ns, "exec", "-i", "probe", "--", "sh", "-ec",
                "cat > /tmp/networkprobe; chmod 500 /tmp/networkprobe", data=binary)
        item = json.loads(kubectl("-n", ns, "get", "pod", "probe", "-o", "json"))
        results[lane + "-pod-ip"] = item["status"]["podIP"]
        results["checks"][lane] = {}
        for name, address in targets.items():
            result = json.loads(kubectl("-n", ns, "exec", "probe", "--", "/tmp/networkprobe", address))
            results["checks"][lane][name] = result["connected"]
    print(json.dumps(results, indent=2), flush=True)
    for name in targets:
        if name != "metadata-v4" and not results["checks"]["control"][name]:
            raise SystemExit("INCONCLUSIVE: positive control unreachable: " + name)
        if results["checks"]["deny"][name] != (name == "allowed"):
            raise SystemExit("FAIL: unexpected candidate connectivity: " + name)
    if not results["checks"]["control"]["metadata-v4"]:
        print("Metadata endpoint absent: obtain trusted packet-drop evidence; failure alone is not denial proof.")
    print("PASS: reachable API and peer controls; candidate allows only the selected peer.")


if __name__ == "__main__":
    main()
