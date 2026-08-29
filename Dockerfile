# syntax=docker/dockerfile:1

# Build stage: compile CSS assets and the Go binary.
FROM golang:1.27.0-alpine AS builder
RUN apk add --no-cache nodejs npm
WORKDIR /app

# Cache Go module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Install the Tailwind CSS + TypeScript toolchain. --ignore-scripts skips
# lifecycle scripts: only build-time deps are needed here, and puppeteer's
# postinstall (test-only) crashes with SIGILL under QEMU on linux/arm64, which
# broke the multi-arch image build. tailwindcss and typescript ship prebuilt
# binaries and need no postinstall.
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund

COPY . .

# Version stamped into the binary. Defaults to package.json so a plain
# `docker build` matches the npm build; CI passes the git tag via
# `--build-arg VERSION=$TAG` to keep the image in sync with releases.
ARG VERSION
RUN npm run build-assets \
    && go build -ldflags="-s -w -X goyt/internal/api.Version=${VERSION:-$(node -p "require('./package.json').version")}" \
        -o goyt ./cmd/goyt

# Runtime stage.
# Alpine 3.22 ships deno 2.x; older releases (3.20) only have deno 1.43, which
# yt-dlp rejects as unsupported for solving YouTube's nsig challenge.
FROM alpine:3.24
# ffmpeg: media muxing/conversion. python3: required by the yt-dlp zipapp the
# updater downloads. deno: JavaScript runtime yt-dlp uses to solve YouTube's
# nsig challenge (without it YouTube returns only image formats and downloads
# fail with "Requested format is not available"). ca-certificates: TLS for
# yt-dlp downloads and checksum verification. su-exec: drop root to the goyt
# user in the entrypoint after fixing bind-mount ownership.
RUN apk add --no-cache ffmpeg python3 deno ca-certificates su-exec \
    && adduser -D -h /app goyt
WORKDIR /app

COPY --from=builder /app/goyt .
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# Writable runtime dirs: downloads and the auto-downloaded yt-dlp binary.
RUN mkdir -p downloads assets/yt-dlp \
    && chown -R goyt:goyt /app \
    && chmod +x /usr/local/bin/docker-entrypoint.sh

# Bind to all interfaces so the published port is reachable from the host.
ENV GOYT_BIND=0.0.0.0 \
    GOYT_PORT=3000
EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -qO- http://127.0.0.1:3000/health || exit 1

# The entrypoint starts as root only to chown the bind-mounted data dirs, then
# runs the app as goyt via su-exec. Pass `--user` to skip the root phase.
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["./goyt"]
