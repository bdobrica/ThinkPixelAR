#!/bin/sh
set -eu

readonly temporary_directory="$(mktemp -d)"
trap 'rm -f "$temporary_directory/source.go" "$temporary_directory/formatted.go" "$temporary_directory/unformatted"; rmdir "$temporary_directory"' EXIT HUP INT TERM

git ls-files '*.go' | while IFS= read -r source_file; do
    # Git may materialize CRLF in a Windows checkout. Compare canonical source
    # formatting independently of that checkout-level line-ending conversion.
    tr -d '\r' < "$source_file" > "$temporary_directory/source.go"
    gofmt "$temporary_directory/source.go" > "$temporary_directory/formatted.go"
    if ! cmp -s "$temporary_directory/source.go" "$temporary_directory/formatted.go"; then
        echo "$source_file"
    fi
done > "$temporary_directory/unformatted"

if [ -s "$temporary_directory/unformatted" ]; then
    echo "Go source files require formatting:" >&2
    cat "$temporary_directory/unformatted" >&2
    echo "run: make fmt" >&2
    exit 1
fi
