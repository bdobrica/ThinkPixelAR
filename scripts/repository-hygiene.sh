#!/usr/bin/env bash
set -euo pipefail

repository=${1:-.}
cd "$repository"

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
    echo "repository hygiene: not a Git work tree" >&2
    exit 2
}

failures=0

report() {
    echo "repository hygiene: $1: $2" >&2
    failures=$((failures + 1))
}

is_forbidden_path() {
    path=$1
    basename=${path##*/}

    case "/$path/" in
        */.thinkpixelar/*|*/.workspace/*|*/workspace-state/*|*/vendor-state/*|*/checkpoints/*|*/sandboxes/*)
            return 0
            ;;
        */.codex/*|*/.claude/*|*/.kube/*)
            return 0
            ;;
        */test/secrets/*|*/test/fixtures/secrets/*|*/testdata/secrets/*)
            return 0
            ;;
    esac

    case "$basename" in
        .env|.env.*|kubeconfig|kubeconfig.*|*.kubeconfig|auth.json|credentials.json|credentials.yml|credentials.yaml|*.pem|*.key|*.p12|*.pfx)
            [ "$basename" = ".env.example" ] && return 1
            return 0
            ;;
        thinkpixelar|thinkpixel-agentd|migrate|*.exe|*.dll|*.so|*.dylib|*.a|*.o|*.out|*.test)
            return 0
            ;;
    esac

    return 1
}

scan_content() {
    path=$1
    blob=$2

    if [ -s "$blob" ] && ! LC_ALL=C grep -Iq . "$blob"; then
        report "binary content is not allowed" "$path"
        return
    fi

    if LC_ALL=C grep -Eiq -- '-----BEGIN ([A-Z0-9 ]+ )?PRIVATE KEY-----|(^|[^A-Za-z0-9])(gh[pousr]_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{60,}|sk-[A-Za-z0-9]{40,}|AKIA[0-9A-Z]{16})([^A-Za-z0-9]|$)' "$blob"; then
        report "credential-like content is not allowed" "$path"
        return
    fi

    if LC_ALL=C grep -Eq -- '^[[:space:]]*kind:[[:space:]]*Config[[:space:]]*$' "$blob" &&
       LC_ALL=C grep -Eq -- '^[[:space:]]*(clusters|contexts|users):[[:space:]]*$' "$blob"; then
        report "kubeconfig content is not allowed" "$path"
    fi
}

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/thinkpixelar-hygiene.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM
index_dir="$tmp_dir/index/"
mkdir -p "$index_dir"
git checkout-index --all --prefix="$index_dir"

while IFS= read -r -d '' path; do
    if is_forbidden_path "$path"; then
        report "forbidden tracked path" "$path"
        continue
    fi

    blob="$index_dir$path"
    [ -f "$blob" ] || { report "could not inspect index entry" "$path"; continue; }
    scan_content "$path" "$blob"
done < <(git ls-files -z)

if [ "$failures" -ne 0 ]; then
    exit 1
fi

echo "repository hygiene: passed"
