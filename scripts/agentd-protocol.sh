#!/bin/sh
# Reproducible source generation; no checked-in downloaded executables.
set -eu
mode=${1:-check}
case "$mode" in generate|check) ;; *) echo 'Expected generate or check' >&2; exit 1;; esac
[ "$(protoc --version)" = 'libprotoc 3.21.12' ] || { echo 'protoc 3.21.12 required' >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
GOBIN="$tmp" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
protoc --plugin="protoc-gen-go=$tmp/protoc-gen-go" --go_out="$tmp" --go_opt=paths=source_relative api/agentd/v1/agentd.proto
if [ "$mode" = generate ]; then
  cp "$tmp/api/agentd/v1/agentd.pb.go" api/agentd/v1/agentd.pb.go
else
  diff -u api/agentd/v1/agentd.pb.go "$tmp/api/agentd/v1/agentd.pb.go"
fi
