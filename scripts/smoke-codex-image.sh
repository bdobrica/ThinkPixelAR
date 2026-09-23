#!/bin/sh
set -eu

docker_bin=${DOCKER:-docker}
image=${CODEX_IMAGE:-thinkpixel-codex:development}
probe=$(mktemp -d)
trap 'rm -rf "$probe"' EXIT HUP INT TERM
chmod 755 "$probe"

# Reuse the real protocol probe inside the image; no host Codex installation or
# operator configuration is mounted. This probe currently qualifies amd64 only.
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${GO:-go}" test -c \
  -o "$probe/protocol" ./internal/adapters/harness/codex
"$docker_bin" run --rm --platform linux/amd64 --network none --read-only \
  --cap-drop ALL --security-opt no-new-privileges --pids-limit 128 \
  --tmpfs /tmp:rw,nosuid,nodev,size=128m,mode=1777 \
  --mount "type=bind,src=$probe,dst=/probe,readonly" \
  --env THINKPIXELAR_TEST_CODEX_BINARY=/usr/local/bin/codex \
  --entrypoint /probe/protocol "$image" \
  -test.run '^TestPinnedAppServer$' -test.v -test.timeout 45s

"$docker_bin" run --rm --network none --read-only \
  --cap-drop ALL --security-opt no-new-privileges \
  --tmpfs /workspace:rw,nosuid,nodev,size=16m,uid=65532,gid=65532 \
  --entrypoint /bin/bash "$image" -euc \
  'test "$(id -u)" = 65532; git init -q /workspace/repo; cd /workspace/repo; printf "demo\n" > sample; rg -q demo sample; test -s /etc/ssl/certs/ca-certificates.crt; test -s /usr/share/licenses/codex/LICENSE; test -r /usr/share/licenses/codex/LICENSE; test -r /usr/share/licenses/codex/NOTICE'

DOCKER="$docker_bin" AGENTD_IMAGE="$image" sh ./scripts/smoke-thinkpixel-agentd-image.sh
echo 'codex image smoke: pinned protocol, coding tools and protected supervisor passed'
