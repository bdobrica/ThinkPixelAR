#!/bin/sh
# Reproducible source generation; no checked-in downloaded executables.
set -eu
mode=${1:-check}
case "$mode" in generate|check) ;; *) echo 'Expected generate or check' >&2; exit 1;; esac
[ "$(protoc --version)" = 'libprotoc 3.21.12' ] || { echo 'protoc 3.21.12 required' >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
GOBIN="$tmp" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
GOBIN="$tmp" go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
protoc --plugin="protoc-gen-go=$tmp/protoc-gen-go" --plugin="protoc-gen-go-grpc=$tmp/protoc-gen-go-grpc" --go-grpc_out="$tmp" --go-grpc_opt=paths=source_relative --go_out="$tmp" --go_opt=paths=source_relative api/agentd/v1/agentd.proto
for file in agentd.pb.go agentd_grpc.pb.go; do
  if [ "$mode" = generate ]; then
    cp "$tmp/api/agentd/v1/$file" "api/agentd/v1/$file"
  else
    diff -u "api/agentd/v1/$file" "$tmp/api/agentd/v1/$file"
  fi
done
