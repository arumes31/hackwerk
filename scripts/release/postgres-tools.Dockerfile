ARG HACKWERK_RELEASE_IMAGE=hackwerk-scan:local
FROM ${HACKWERK_RELEASE_IMAGE} AS hackwerk
FROM postgres:18.6-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5
COPY --from=hackwerk /hackwerk /hackwerk
COPY scripts/ops /ops
COPY scripts/release/restore-smoke-runner.sh /restore-smoke-runner.sh
ENV HACKWERK_BINARY=/hackwerk
ENTRYPOINT ["sh", "/restore-smoke-runner.sh"]
