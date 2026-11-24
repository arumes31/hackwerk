ARG HACKWERK_RELEASE_IMAGE=hackwerk-scan:local
FROM ${HACKWERK_RELEASE_IMAGE} AS hackwerk
FROM postgres:18.7-alpine@sha256:0115267ab5a509022df9319c77be365cef335adb71556c75f60f5360ee247e71
COPY --from=hackwerk /hackwerk /hackwerk
COPY scripts/ops /ops
COPY scripts/release/restore-smoke-runner.sh /restore-smoke-runner.sh
ENV HACKWERK_BINARY=/hackwerk
ENTRYPOINT ["sh", "/restore-smoke-runner.sh"]
