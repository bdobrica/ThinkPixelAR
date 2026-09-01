#!/usr/bin/env bash
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
scanner="$script_dir/repository-hygiene.sh"
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/thinkpixelar-hygiene-test.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

new_repository() {
    fixture=$1
    mkdir -p "$fixture"
    git -C "$fixture" init -q
    git -C "$fixture" config user.email hygiene@example.invalid
    git -C "$fixture" config user.name "Hygiene Test"
}

expect_rejected() {
    name=$1
    path=$2
    content=$3
    fixture="$tmp_dir/$name"
    new_repository "$fixture"
    mkdir -p "$fixture/$(dirname -- "$path")"
    printf '%b' "$content" >"$fixture/$path"
    git -C "$fixture" add -f -- "$path"
    if "$scanner" "$fixture" >/dev/null 2>&1; then
        echo "hygiene self-test: expected rejection for $name" >&2
        exit 1
    fi
}

expect_rejected workspace-state workspace-state/session.json '{}\n'
expect_rejected vendor-credentials .codex/auth.json '{}\n'
expect_rejected kubeconfig config 'kind: Config\nclusters:\n'
expect_rejected sandbox-credentials sandboxes/session/bootstrap.pem 'temporary\n'
expect_rejected binary dist/tool '\177ELF\001\001\001\000'
expect_rejected test-secrets test/fixtures/secrets/token.txt 'placeholder\n'
private_key_marker='-----BEGIN PRIVATE'
expect_rejected private-key docs/leaked.txt "$private_key_marker KEY-----\n"

clean="$tmp_dir/clean"
new_repository "$clean"
mkdir -p "$clean/docs"
printf '%s\n' 'Credentials and kubeconfigs must not be committed.' >"$clean/docs/security.md"
printf '%s\n' 'EXAMPLE_TOKEN=replace-me' >"$clean/.env.example"
git -C "$clean" add -- docs/security.md .env.example
"$scanner" "$clean" >/dev/null

echo "hygiene self-test: passed"
