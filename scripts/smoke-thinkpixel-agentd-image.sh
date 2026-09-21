#!/bin/sh
set -eu

docker_bin=${DOCKER:-docker}
image=${AGENTD_IMAGE:-thinkpixel-agentd:development}
container="thinkpixel-agentd-smoke-$$"
fixture=$(mktemp -d)
mkdir "$fixture/..revision"
cp deploy/agentd/config.example.json "$fixture/..revision/config.json"
ln -s ..revision "$fixture/..data"
ln -s ..data/config.json "$fixture/config.json"
chmod 755 "$fixture" "$fixture/..revision"
chmod 444 "$fixture/..revision/config.json"

cleanup() {
	"$docker_bin" rm --force "$container" "$container-missing" "$container-writable" >/dev/null 2>&1 || true
	rm -rf "$fixture"
}
trap cleanup EXIT HUP INT TERM

user=$("$docker_bin" image inspect --format '{{.Config.User}}' "$image")
if [ "$user" != "65532:65532" ]; then
	echo "agentd image smoke: expected user 65532:65532, got $user" >&2
	exit 1
fi

entrypoint=$("$docker_bin" image inspect --format '{{join .Config.Entrypoint " "}}' "$image")
if [ "$entrypoint" != "/usr/local/bin/thinkpixel-agentd" ]; then
	echo "agentd image smoke: unexpected entrypoint $entrypoint" >&2
	exit 1
fi

# Missing bootstrap and a writable bootstrap mount must fail startup.
result=0
timeout 10 "$docker_bin" run --rm --name "$container-missing" --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges "$image" >/dev/null 2>&1 || result=$?
if [ "$result" != 1 ]; then
  echo 'agentd image smoke: missing bootstrap accepted' >&2
  exit 1
fi
result=0
timeout 10 "$docker_bin" run --rm --name "$container-writable" --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges \
  --mount "type=bind,src=$fixture,dst=/run/thinkpixel/bootstrap" \
  "$image" >/dev/null 2>&1 || result=$?
if [ "$result" != 1 ]; then
  echo 'agentd image smoke: writable bootstrap mount accepted' >&2
  exit 1
fi

"$docker_bin" run --detach --name "$container" --network none \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  --mount "type=bind,src=$fixture,dst=/run/thinkpixel/bootstrap,readonly" \
  "$image" >/dev/null

attempt=0
configured=false
while [ "$attempt" -lt 10 ]; do
  if "$docker_bin" logs "$container" 2>&1 | grep -q 'awaiting_transport'; then
    configured=true
    break
  fi
  attempt=$((attempt + 1))
  sleep 1
done
if [ "$configured" != true ]; then
  echo 'agentd image smoke: configuration was not accepted' >&2
  exit 1
fi
"$docker_bin" stop --time 5 "$container" >/dev/null
[ "$("$docker_bin" inspect --format '{{.State.ExitCode}}' "$container")" = 0 ] || {
  echo 'agentd image smoke: signal shutdown failed' >&2
  exit 1
}
echo 'agentd image smoke: read-only bootstrap, rejected unsafe startup, SIGTERM passed'
