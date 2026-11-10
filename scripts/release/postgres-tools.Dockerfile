ARG HACKWERK_RELEASE_IMAGE=hackwerk-scan:local
FROM ${HACKWERK_RELEASE_IMAGE} AS hackwerk
FROM postgres:18.6-alpine@sha256:123b17998ccaf47e53b502e8c6a1e79afd8f579c6f3eec83c563da026f957395
COPY --from=hackwerk /hackwerk /hackwerk
COPY scripts/ops /ops
COPY scripts/release/restore-smoke-runner.sh /restore-smoke-runner.sh
ENV HACKWERK_BINARY=/hackwerk
ENTRYPOINT ["sh", "/restore-smoke-runner.sh"]
