# syntax=docker/dockerfile:1

# Build stage: compile CSS assets and the Go binary.
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache nodejs npm
WORKDIR /app

# Cache Go module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Cache npm install for the Tailwind CSS toolchain.
COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run build-production

# Runtime stage.
# Alpine 3.22 ships deno 2.x; older releases (3.20) only have deno 1.43, which
# yt-dlp rejects as unsupported for solving YouTube's nsig challenge.
FROM alpine:3.22
# ffmpeg: media muxing/conversion. python3: required by the yt-dlp zipapp the
# updater downloads. deno: JavaScript runtime yt-dlp uses to solve YouTube's
# nsig challenge (without it YouTube returns only image formats and downloads
# fail with "Requested format is not available"). ca-certificates: TLS for
# yt-dlp downloads and checksum verification.
RUN apk add --no-cache ffmpeg python3 deno ca-certificates \
    && adduser -D -h /app goyt
WORKDIR /app

COPY --from=builder /app/goyt .

# Writable runtime dirs: downloads and the auto-downloaded yt-dlp binary.
RUN mkdir -p downloads assets/yt-dlp && chown -R goyt:goyt /app
USER goyt

# Bind to all interfaces so the published port is reachable from the host.
ENV GOYT_BIND=0.0.0.0 \
    GOYT_PORT=3000
EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -qO- http://127.0.0.1:3000/health || exit 1

CMD ["./goyt"]
