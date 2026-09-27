#!/bin/sh
set -eu
: "${THINKPIXELAR_TEST_DATABASE_URL:?use a disposable migrated database}"
: "${THINKPIXELAR_E2E_SSH:?set the operator SSH alias, for example k3spi}"
: "${THINKPIXELAR_E2E_IMAGE:?set the imported immutable probe image digest}"
export THINKPIXELAR_E2E_KUBERNETES=1
export GOCACHE="${GOCACHE:-/tmp/thinkpixelar-go-cache}"
exec "${GO:-go}" test -v -count=1 -timeout=16m ./internal/adapters/http \
  -run '^TestStandaloneKubernetesContinuation$'
