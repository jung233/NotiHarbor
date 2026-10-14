# amd64 and arm64 images are built on matching native GitHub runners.
# CGO remains enabled because the project retains its SQLite driver.
FROM node:20-bookworm-slim AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
COPY web/ ./
ARG BUILD_VERSION=dev
RUN PUBLIC_URL=/ REACT_APP_VERSION="${BUILD_VERSION}" npm run build

FROM golang:1.26-bookworm AS go-build
ARG TARGETARCH
ARG BUILD_VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-build /src/web/build ./web/build
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*
RUN CGO_ENABLED=1 GOOS=linux GOARCH="${TARGETARCH}" go build -trimpath -ldflags="-s -w -X=message-pusher/common.Version=${BUILD_VERSION}" -o /out/message-pusher .

FROM debian:bookworm-slim AS runtime
ARG BUILD_VERSION=dev
ENV PORT=3000 SQLITE_PATH=/data/message-pusher.db TZ=UTC
LABEL org.opencontainers.image.title="NotiHarbor" \
      org.opencontainers.image.description="Self-hosted notification gateway" \
      org.opencontainers.image.source="https://github.com/jung233/NotiHarbor" \
      org.opencontainers.image.version="${BUILD_VERSION}"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tzdata && rm -rf /var/lib/apt/lists/* && groupadd --gid 10001 app && useradd --uid 10001 --gid app --home-dir /data --no-create-home --shell /usr/sbin/nologin app && mkdir -p /data/logs && chown -R app:app /data
COPY --from=go-build --chown=app:app /out/message-pusher /usr/local/bin/message-pusher
WORKDIR /data
USER 10001:10001
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD curl --fail --silent --show-error http://127.0.0.1:3000/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/message-pusher"]
CMD ["--log-dir", "/data/logs"]
