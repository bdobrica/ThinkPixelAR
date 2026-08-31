#!/bin/sh
set -eu

readonly source_file="${OPENAPI_SOURCE_FILE:-docs/api/openapi.yaml}"
readonly generated_file="${OPENAPI_GENERATED_FILE:-api/openapi/openapi.json}"

generate() {
  npx --no-install redocly lint "$source_file"
  npx --no-install redocly bundle "$source_file" --ext json --output "$generated_file"
}

check() {
  readonly temporary_directory="$(mktemp -d)"
  readonly temporary_file="$temporary_directory/openapi.json"
  trap 'rm -f "$temporary_file"; rmdir "$temporary_directory"' EXIT HUP INT TERM

  npx --no-install redocly lint "$source_file"
  npx --no-install redocly bundle "$source_file" --ext json --output "$temporary_file"
  if ! cmp -s "$temporary_file" "$generated_file"; then
    echo "generated OpenAPI artifact is stale; run: npm run openapi:generate" >&2
    diff -u "$generated_file" "$temporary_file" || true
    exit 1
  fi
}

case "${1:-}" in
  generate) generate ;;
  check) check ;;
  *) echo "usage: $0 {generate|check}" >&2; exit 2 ;;
esac
