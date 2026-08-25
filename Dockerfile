# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25.14

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -ldflags="-s -w" \
    -o /out/eth-indexer ./cmd/indexer

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app

COPY --from=build --chown=nonroot:nonroot /out/eth-indexer /usr/local/bin/eth-indexer
COPY --chown=nonroot:nonroot configs/config.container.yaml /etc/eth-indexer/config.yaml

USER nonroot:nonroot

EXPOSE 8080
STOPSIGNAL SIGTERM

ENTRYPOINT ["/usr/local/bin/eth-indexer"]
CMD ["-config", "/etc/eth-indexer/config.yaml"]
