# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS toolchain
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS build
COPY --from=toolchain /usr/local/go /usr/local/go
COPY --from=toolchain /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV PATH="/usr/local/go/bin:${PATH}" GOPATH=/go CGO_ENABLED=0 GOFLAGS=-mod=readonly
WORKDIR /src

COPY go.mod go.sum ./
COPY applekit/go.mod applekit/go.sum ./applekit/
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY frontend/package.json frontend/package-lock.json ./frontend/
RUN --mount=type=cache,target=/root/.npm npm ci --prefix frontend
COPY . .

ARG VERSION
# The source has no git history; pass the commit to show it in the interface:
# docker build --build-arg COMMIT="$(git rev-parse HEAD)" .
ARG COMMIT
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    if [ -n "$VERSION" ]; then export APPLESEUM_VERSION="${VERSION#v}"; fi; \
    if [ -n "$COMMIT" ]; then export APPLESEUM_COMMIT="$COMMIT"; fi; \
    node scripts/backend_build.js --frontend-only && \
    mkdir -p /out/data && \
    node --input-type=module -e 'import {productVersion} from "./frontend/src/api/types.ts"; process.stdout.write(process.env.APPLESEUM_VERSION ?? productVersion)' > /out/version

ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath \
    -ldflags="-s -w -X appleseum/backend/internal/product.Version=$(cat /out/version)" \
    -o /out/appleseum ./backend/cmd/appleseum

FROM gcr.io/distroless/static-debian13:nonroot AS runtime
LABEL org.opencontainers.image.source="https://github.com/hyperion-cs/appleseum"
LABEL org.opencontainers.image.licenses="Apache-2.0"
WORKDIR /app
COPY --from=build /out/appleseum /app/appleseum
COPY LICENSE /app/LICENSE
COPY --from=build --chown=65532:65532 /out/data /data
VOLUME ["/data"]
EXPOSE 8080 8443
ENTRYPOINT ["/app/appleseum", "--data", "/data"]
