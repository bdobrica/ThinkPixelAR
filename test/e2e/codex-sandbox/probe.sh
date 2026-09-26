#!/bin/sh
set -eu
export THINKPIXELAR_TEST_CODEX_BINARY=/usr/local/bin/codex
uname -srmo
test "$(id -u)" = 65532
test ! -e /var/run/secrets/kubernetes.io/serviceaccount/token
test ! -e /dev/kvm
grep -Eq '^CapEff:[[:space:]]+0000000000000000$' /proc/self/status
grep -Eq '^NoNewPrivs:[[:space:]]+1$' /proc/self/status
grep -Eq '^Seccomp:[[:space:]]+2$' /proc/self/status
/probe/agentd.test -test.run '^TestPinnedCodexSupervisedStartup$' -test.v -test.timeout 90s
/probe/codex.test -test.run '^TestPinned(AppServer|TurnStart|TurnEvents|TurnFailure|TurnInterrupt)$' -test.v -test.timeout 180s
echo 'CDX-016 PASS: real pinned Codex supervisor, turn, candidates and interruption'
# Keep the guest alive briefly for independent operator host/KVM correlation.
# The Pod deadline also bounds failed/stalled probes. No automatic deletion.
sleep 600
