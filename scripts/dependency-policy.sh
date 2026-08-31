#!/bin/sh
set -eu

go_command=$1

replacements=$($go_command list -mod=readonly -m -f '{{if .Replace}}{{.Path}} => {{.Replace.Path}}{{end}}' all)
if [ -n "$replacements" ]; then
    echo "dependency policy: module replacements are not allowed:" >&2
    echo "$replacements" >&2
    exit 1
fi

unversioned=$($go_command list -mod=readonly -m -f '{{if and (not .Main) (not .Version)}}{{.Path}}{{end}}' all)
if [ -n "$unversioned" ]; then
    echo "dependency policy: unversioned modules are not allowed:" >&2
    echo "$unversioned" >&2
    exit 1
fi

echo "Go module inventory:"
$go_command list -mod=readonly -m -f '{{if .Main}}{{.Path}} (main){{else}}{{.Path}} {{.Version}}{{end}}' all

inventory_file=$(mktemp "${TMPDIR:-/tmp}/thinkpixelar-go-licenses.XXXXXX")
trap 'rm -f "$inventory_file"' EXIT HUP INT TERM

$go_command list -buildvcs=false -mod=readonly -deps -test \
    -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{end}}{{end}}' ./... \
    | sed '/^$/d; s/ /\	/' | sort -u > "$inventory_file"

if ! sed '/^#/d; /^[[:space:]]*$/d' build/dependency-licenses.tsv \
    | cut -f 1-2 | sort -u | diff -u - "$inventory_file"; then
    echo "dependency policy: build/dependency-licenses.tsv is stale or incomplete" >&2
    exit 1
fi

echo "Go dependency license inventory:"
sed '/^#/d; /^[[:space:]]*$/d' build/dependency-licenses.tsv
