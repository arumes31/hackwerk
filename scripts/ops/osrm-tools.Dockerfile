FROM ghcr.io/project-osrm/osrm-backend:v26.7.3-debian@sha256:a7091038e39a73659767f34ef2d389909b42ea80b09bd2bdca482dce2991cbad

RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl osmium-tool util-linux \
    && rm -rf /var/lib/apt/lists/*

COPY --chmod=0555 scripts/ops/update-osrm.sh /usr/local/bin/hackwerk-osrm-update

USER 65532:65532
ENTRYPOINT []
