ARG HACKWERK_RELEASE_IMAGE=hackwerk-scan:local
FROM ${HACKWERK_RELEASE_IMAGE} AS hackwerk
FROM postgres:18.6-alpine@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2
COPY --from=hackwerk /hackwerk /hackwerk
COPY scripts/ops /ops
COPY scripts/release/restore-smoke-runner.sh /restore-smoke-runner.sh
ENV HACKWERK_BINARY=/hackwerk
ENTRYPOINT ["sh", "/restore-smoke-runner.sh"]
