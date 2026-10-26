FROM ghcr.io/project-osrm/osrm-backend:26.10.0-debian@sha256:55dbfd47d984c2cacd64e32901b4321c86b5ec761438e69aa053dc78698a786f

RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl osmium-tool socat util-linux \
    && rm -rf /var/lib/apt/lists/*

COPY --chmod=0555 scripts/ops/update-osrm.sh /usr/local/bin/hackwerk-osrm-update

USER 65532:65532
ENTRYPOINT []
