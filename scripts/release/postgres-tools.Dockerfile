ARG HACKWERK_RELEASE_IMAGE=hackwerk-scan:local
FROM ${HACKWERK_RELEASE_IMAGE} AS hackwerk
FROM postgres:18.6-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
COPY --from=hackwerk /hackwerk /hackwerk
COPY scripts/ops /ops
COPY scripts/release/restore-smoke-runner.sh /restore-smoke-runner.sh
ENV HACKWERK_BINARY=/hackwerk
ENTRYPOINT ["sh", "/restore-smoke-runner.sh"]
