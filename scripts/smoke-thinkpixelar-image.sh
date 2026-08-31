#!/bin/sh
set -eu

docker_bin=${DOCKER:-docker}
image=${IMAGE:-thinkpixelar:development}
container="thinkpixelar-smoke-$$"
port=${THINKPIXELAR_IMAGE_SMOKE_PORT:-58080}

cleanup() {
	"$docker_bin" rm --force "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

user=$("$docker_bin" image inspect --format '{{.Config.User}}' "$image")
if [ "$user" != "65532:65532" ]; then
	echo "image smoke: expected user 65532:65532, got $user" >&2
	exit 1
fi

"$docker_bin" run --detach --name "$container" \
	--read-only --cap-drop ALL --security-opt no-new-privileges \
	--publish "127.0.0.1:${port}:8080" \
	--env THINKPIXELAR_HTTP_LISTEN_ADDRESS=0.0.0.0:8080 \
	"$image" >/dev/null

attempt=0
while [ "$attempt" -lt 30 ]; do
	if response=$(curl --fail --silent --show-error "http://127.0.0.1:${port}/livez" 2>/dev/null); then
		if [ "$response" != '{"status":"ok"}' ]; then
			echo "image smoke: unexpected liveness response" >&2
			exit 1
		fi
		exit 0
	fi
	if [ "$("$docker_bin" inspect --format '{{.State.Running}}' "$container")" != "true" ]; then
		"$docker_bin" logs "$container" >&2
		exit 1
	fi
	attempt=$((attempt + 1))
	sleep 1
done

"$docker_bin" logs "$container" >&2
echo "image smoke: liveness endpoint did not become ready" >&2
exit 1
