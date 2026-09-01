#!/bin/sh
set -eu

fail() {
  echo "supported versions: $1" >&2
  exit 1
}

require_literal() {
  file=$1
  literal=$2
  grep -Fq -- "$literal" "$file" || fail "$file does not contain expected pin: $literal"
}

go_pin=$(tr -d '[:space:]' < .go-version)
module_go_pin=$(awk '$1 == "go" { print $2; exit }' go.mod | tr -d '[:space:]')
[ "$go_pin" = "$module_go_pin" ] || fail ".go-version and go.mod disagree"

actual_go=$(go env GOVERSION)
[ "$actual_go" = "go$go_pin" ] || fail "Go $go_pin required; found $actual_go"

node_pin=$(node -p 'require("./package.json").engines.node')
npm_pin=$(node -p 'require("./package.json").engines.npm')
actual_node=$(node --version)
actual_npm=$(npm --version)
[ "$actual_node" = "v$node_pin" ] || fail "Node.js $node_pin required; found $actual_node"
[ "$actual_npm" = "$npm_pin" ] || fail "npm $npm_pin required; found $actual_npm"

builder='golang:1.26.7-alpine3.23@sha256:b17af760035fc2f338eed92d448a6c67f2d45438844fc6c60678fa5f99e44b57'
runtime='gcr.io/distroless/static-debian13:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7'
postgres='postgres:18.6-alpine3.24@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2'

for dockerfile in Dockerfile Dockerfile.agentd; do
  require_literal "$dockerfile" "$builder"
  require_literal "$dockerfile" "$runtime"
done
require_literal compose.yaml "$postgres"
require_literal Makefile 'STATICCHECK_VERSION := v0.8.1'
require_literal Makefile 'GOVULNCHECK_VERSION := v1.7.0'
require_literal package.json '"@redocly/cli": "2.49.0"'
require_literal package-lock.json '"version": "2.49.0"'
require_literal .github/workflows/ci.yml 'node-version: 24.11.1'

for pin in \
  "$go_pin" "$node_pin" "$npm_pin" \
  'Staticcheck `v0.8.1`' 'govulncheck `v1.7.0`' \
  "$builder" "$runtime" "$postgres"; do
  require_literal docs/supported-versions.md "$pin"
done

echo "supported versions: passed (Go $go_pin, Node.js $node_pin, npm $npm_pin)"
