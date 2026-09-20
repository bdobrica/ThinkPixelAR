#!/bin/sh
# Render the exact upstream core release plus the reviewed operator overlay.
set -eu
source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
render_dir=$(mktemp -d)
trap 'rm -rf "$render_dir"' EXIT HUP INT TERM
curl --fail --silent --show-error --location --max-time 90 \
  https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.0/sandbox.yaml \
  -o "$render_dir/upstream.yaml"
printf '%s  %s\n' 4535d101df688c00ccca565b4b76d2e87d697913182a6dbb7339a811b59e02d7 "$render_dir/upstream.yaml" | sha256sum -c - >&2
cp "$source_dir/kustomization.yaml" "$render_dir/kustomization.yaml"
kubectl kustomize "$render_dir"
