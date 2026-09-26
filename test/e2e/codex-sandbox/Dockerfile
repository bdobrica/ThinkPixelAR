# Build context contains only current agentd and the two compiled Go test binaries.
# Supply a locally verified Codex runtime image; record its OCI digest as evidence.
ARG CODEX_BASE=thinkpixel-codex:development
FROM ${CODEX_BASE}
COPY --chmod=0755 thinkpixel-agentd /usr/local/bin/thinkpixel-agentd
COPY --chmod=0755 codex.test agentd.test probe.sh /probe/
ENTRYPOINT ["/probe/probe.sh"]
