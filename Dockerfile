# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
ARG GO_IMAGE=golang:1.27.0-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452
ARG SOURCE_URL=https://example.invalid/hackwerk

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
WORKDIR /src
ARG VERSION=dev
ARG RELEASE_VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
ARG TARGETOS
ARG TARGETARCH
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath \
    -ldflags="-s -w -X example.invalid/hackplan/internal/buildinfo.Version=${VERSION} -X example.invalid/hackplan/internal/buildinfo.Release=${RELEASE_VERSION} -X example.invalid/hackplan/internal/buildinfo.Commit=${COMMIT} -X example.invalid/hackplan/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/hackwerk ./cmd/hackwerk

FROM scratch AS runtime
ARG VERSION=dev
ARG RELEASE_VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
ARG SOURCE_URL
LABEL org.opencontainers.image.title="HackWerk" \
      org.opencontainers.image.version="${RELEASE_VERSION}" \
      at.hackwerk.build.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.source="${SOURCE_URL}" \
      org.opencontainers.image.licenses="Proprietary"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/hackwerk /hackwerk
USER 65532:65532
EXPOSE 18533
ENTRYPOINT ["/hackwerk"]
CMD ["serve"]
STOPSIGNAL SIGTERM
