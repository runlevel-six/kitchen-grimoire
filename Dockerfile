# SPDX-License-Identifier: Apache-2.0
#
# Multi-stage, multi-arch build (docs/DESIGN.md §14.2).
# The builder runs on the build host's platform and cross-compiles for the
# target (CGO is off), so no emulation is needed to build arm64 on amd64.

# golang:1.27.4-trixie
FROM --platform=$BUILDPLATFORM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
      -ldflags="-s -w -X github.com/runlevel-six/kitchen-grimoire/internal/product.Version=${VERSION}" \
      -o /out/grimoire ./cmd/grimoire

# An empty /data for the runtime image. It is copied as an entry of a
# staging root so that --chown applies to the directory itself; Docker seeds
# new named volumes from it, so they start out writable by the nonroot user.
RUN mkdir -p /out/rootfs/data

# gcr.io/distroless/static-debian13:nonroot (CA roots, tzdata, uid 65532)
FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931

ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="Kitchen Grimoire" \
      org.opencontainers.image.description="Self-hosted recipe library for one household" \
      org.opencontainers.image.source="https://github.com/runlevel-six/kitchen-grimoire" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

COPY --from=build /out/grimoire /usr/local/bin/grimoire
COPY --from=build --chown=65532:65532 /out/rootfs/ /

# /data (database + images) and /tmp must be writable volumes; the root
# filesystem is meant to run read-only.
VOLUME ["/data"]
ENV GRIMOIRE_DATA_DIR=/data
EXPOSE 8080 9090
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/grimoire"]
CMD ["serve"]
