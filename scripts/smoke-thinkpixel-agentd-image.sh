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
	"$docker_bin" rm --force "$container" "$container-missing" "$container-writable" "$container-credentials" >/dev/null 2>&1 || true
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

# Build a test-only static probe for the image architecture; never add it to the image.
arch=$("$docker_bin" image inspect --format '{{.Architecture}}' "$image")
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "${GO:-go}" build -trimpath -o "$fixture/privilegeprobe" ./test/security/privilegeprobe
chmod 555 "$fixture/privilegeprobe"

# Even an empty KUBECONFIG override must fail startup.
result=0
timeout 10 "$docker_bin" run --rm --name "$container-credentials" --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --env KUBECONFIG= \
  --mount "type=bind,src=$fixture,dst=/run/thinkpixel/bootstrap,readonly" \
  "$image" >/dev/null 2>&1 || result=$?
[ "$result" = 1 ] || { echo 'agentd image smoke: Kubernetes environment accepted' >&2; exit 1; }

# A conventional service-account projection is forbidden even without a token.
mkdir "$fixture/serviceaccount"
chmod 755 "$fixture/serviceaccount"
result=0
timeout 10 "$docker_bin" run --rm --name "$container-credentials" --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges \
  --mount "type=bind,src=$fixture,dst=/run/thinkpixel/bootstrap,readonly" \
  --mount "type=bind,src=$fixture/serviceaccount,dst=/var/run/secrets/kubernetes.io/serviceaccount,readonly" \
  "$image" >/dev/null 2>&1 || result=$?
[ "$result" = 1 ] || { echo 'agentd image smoke: service-account projection accepted' >&2; exit 1; }


# A config-only mount must now fail, rather than leave a dormant supervisor.
result=0
timeout 10 "$docker_bin" run --rm --name "$container" --network none \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  --mount "type=bind,src=$fixture,dst=/run/thinkpixel/bootstrap,readonly" \
  "$image" >/dev/null 2>&1 || result=$?
[ "$result" = 1 ] || { echo 'agentd image smoke: incomplete transport accepted' >&2; exit 1; }

# Supply ephemeral test-only credentials so the real supervisor remains PID 1
# while attempting a bounded connection. No endpoint or authority is available.
mkdir "$fixture/..transport"
chmod 755 "$fixture/..transport"
"${GO:-go}" run ./test/harnessfixture/cmd/bootstrap "$fixture/..transport"
ln -sfn ..transport "$fixture/..data"
for name in client.crt client.key server-ca.crt bootstrap.proof challenge.bin trust-domain; do
  ln -s "..data/$name" "$fixture/$name"
done
"$docker_bin" run --detach --name "$container" --network none \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  --mount "type=bind,src=$fixture,dst=/run/thinkpixel/bootstrap,readonly" \
  "$image" >/dev/null
attempt=0
configured=false
while [ "$attempt" -lt 10 ]; do
  if "$docker_bin" logs "$container" 2>&1 | grep -q 'agent supervisor connecting'; then
    configured=true
    break
  fi
  attempt=$((attempt + 1))
  sleep 1
done
[ "$configured" = true ] || { echo 'agentd image smoke: transport bundle rejected' >&2; exit 1; }
settings=$("$docker_bin" inspect --format '{{.HostConfig.Privileged}}|{{.HostConfig.PidMode}}|{{.HostConfig.IpcMode}}|{{.HostConfig.NetworkMode}}' "$container")
[ "$settings" = 'false||private|none' ] || { echo 'agentd image smoke: unexpected namespace configuration' >&2; exit 1; }
"$docker_bin" exec "$container" /run/thinkpixel/bootstrap/privilegeprobe
"$docker_bin" stop --time 5 "$container" >/dev/null
[ "$("$docker_bin" inspect --format '{{.State.ExitCode}}' "$container")" = 0 ] || {
  echo 'agentd image smoke: signal shutdown failed' >&2
  exit 1
}
echo 'agentd image smoke: protected bundle, incomplete transport rejection, privileges and SIGTERM passed'
