FROM ghcr.io/project-osrm/osrm-backend:26.11.0-debian@sha256:e1929ae2818226284d7ef007329098a30851c11528ab3712bc9c8d68da78278b

RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl osmium-tool socat util-linux \
    && rm -rf /var/lib/apt/lists/*

COPY --chmod=0555 scripts/ops/update-osrm.sh /usr/local/bin/hackwerk-osrm-update

USER 65532:65532
ENTRYPOINT []
