# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.27.0

FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X example.invalid/hackplan/internal/buildinfo.Version=${VERSION} -X example.invalid/hackplan/internal/buildinfo.Commit=${COMMIT} -X example.invalid/hackplan/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/hackwerk ./cmd/hackwerk

FROM scratch AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/hackwerk /hackwerk
USER 65532:65532
EXPOSE 18533
ENTRYPOINT ["/hackwerk"]
CMD ["serve"]
