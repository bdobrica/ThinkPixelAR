# Operator-built qualification image, never a production runtime entrypoint.
ARG CODEX_BASE=thinkpixel-codex:development
FROM ${CODEX_BASE}
COPY --chmod=0755 agentd.test /probe/agentd.test
COPY config.example.json /probe/deploy/agentd/config.example.json
WORKDIR /probe/internal/app/agentd
ENTRYPOINT ["/bin/sleep", "1800"]
