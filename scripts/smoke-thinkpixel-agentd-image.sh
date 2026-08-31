#!/bin/sh
set -eu

docker_bin=${DOCKER:-docker}
image=${AGENTD_IMAGE:-thinkpixel-agentd:development}
container="thinkpixel-agentd-smoke-$$"

cleanup() {
	"$docker_bin" rm --force "$container" >/dev/null 2>&1 || true
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

"$docker_bin" run --detach --name "$container" \
	--read-only --cap-drop ALL --security-opt no-new-privileges \
	"$image" >/dev/null

attempt=0
while [ "$attempt" -lt 10 ]; do
	if [ "$("$docker_bin" inspect --format '{{.State.Running}}' "$container")" = "true" ]; then
		exit 0
	fi
	attempt=$((attempt + 1))
	sleep 1
done

"$docker_bin" logs "$container" >&2
echo "agentd image smoke: process did not remain running" >&2
exit 1
