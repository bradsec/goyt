# Changelog

All notable changes to this project are documented here. Releases use calendar
versioning (CalVer): `YYYY.MM.DD`.

## [Unreleased]

## [2026.10.28.1]

### Added

- New downloads show selected source quality in a start toast and active-row badges; completed rows use final ffprobe results.
- Completed downloads show final video resolution, HDR, audio channels, and bitrate when available.
- Compatibility mode prefers direct SDR H.264 and stereo AAC downloads at 1080p or lower, then converts when needed. Manual Convert also limits the short side to 1080p and tone maps HDR to SDR. The Intake panel shows the active limit, and converted files get a RE-ENCODED badge.

## [2026.10.28]

### Fixed

- MP4 and WebM downloads now choose the highest available resolution before preferring container-friendly codecs. High-resolution VP9 or AV1 streams are no longer displaced by lower-resolution H.264 or VP9 streams. Downloads still remux by stream copy; optional compatibility re-encoding remains available.
- Paused and canceled downloads remain visible, URL validation ignores stale replies, and connection status recovers after a successful poll.
- State-changing API requests with a cross-origin `Origin` header are rejected via Go's `http.CrossOriginProtection`.
- `/api/login` is limited to 10 attempts per minute per client IP.
- Responses send `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and `Referrer-Policy: same-origin`.
- Titles with a fake extension (for example `Clip./x`) can no longer inject path separators or yt-dlp template fields into the output filename.
- Failed downloads keep yt-dlp's final `ERROR` message instead of sometimes showing only `exit status 1`.
- `goyt setpass` now rotates the session secret, so changing the password signs out existing sessions.
- Go toolchain 1.27.3 fixes 11 reachable standard library vulnerabilities; GitHub Actions and npm tooling updated to current releases.
- Queue workers now claim downloads once, state saves are serialized, and temporary-file cleanup only matches the exact output filename.

## [2026.08.29.5]

### Fixed

- The multi-arch container image failed to publish: `npm ci` in the build stage
  crashed with `SIGILL` on `linux/arm64` under QEMU while running puppeteer's
  postinstall (test-only tooling). The build now runs `npm ci --ignore-scripts`,
  which also speeds it up. This is the first image to actually ship the
  2026.08.29.4 bind-mount permission fix.

## [2026.08.29.4]

### Fixed

- Docker downloads failed with `[Errno 13] Permission denied` when the
  bind-mounted `./downloads` directory was owned by root (Docker auto-creates it
  that way) or by a uid other than the container's runtime user. The image now
  ships an entrypoint that chowns the mounted data directories on start and then
  drops to the unprivileged `goyt` user via `su-exec`, so a plain
  `-v "$(pwd)/downloads:/app/downloads"` works regardless of host ownership.
  Pass `--user` to opt out of the root phase.

## [2026.08.29.3]

### Added

- Releases now publish a pre-built multi-arch container image
  (`linux/amd64`, `linux/arm64`) to `ghcr.io/bradsec/goyt`, tagged with the
  CalVer version and `latest`. `docker-compose.yml` pulls it by default, so a
  Docker install no longer requires a local toolchain build.

### Fixed

- Web UI login was unusable when `WEBUI_PASSWORD` (or a stored password hash)
  was set: the auth middleware redirected `/assets/*` requests to `/login`, so
  the login page received `text/html` for its module script and fonts
  ("Failed to load module script", "invalid sfntVersion"). Static assets under
  `/assets/` are now exempt from the login gate.

## [2026.08.29.2]

### Changed

- Bumped golangci-lint to v2.13.2 so linting runs against the Go 1.27 build
  target; the previous pin could not load the config under Go 1.27.
- Added oxlint for TypeScript and JavaScript linting, wired into CI and
  `npm test`.
- Raised the TypeScript compile target and lib to ES2023.

## [2026.08.29]

### Changed

- Updated the build target to Go 1.27 and refreshed Go and frontend dependencies.
- Migrated browser, service-worker, and UI test sources from JavaScript to
  TypeScript, with compiled assets remaining embedded in the Go binary.
- Updated CI, Docker, Node.js, Alpine, Tailwind CSS, and Puppeteer tooling.
- Migrated golangci-lint to its v2 configuration and enabled modern Go checks.

### Security

- Bounded multipart cookie uploads, hardened session-cookie cleanup, and escaped
  request-derived log values.

### Fixed

- Download-manager configuration updates no longer race active downloads, and
  codec probing no longer holds the manager lock during external I/O.
- Graceful shutdown now cancels and waits for workers, conversions, cleanup,
  progress monitors, and state persistence in the correct order.
- Runtime concurrency changes now resize the conversion limit as well as the
  download worker pool.
- FFmpeg ZIP extraction rejects oversized entries instead of installing a
  silently truncated executable.

## [2026.06.17]

### Fixed

- Large completed files now stream to disk through the browser's native download
  manager instead of being buffered into memory, so the save shows progress and
  no longer appears to hang.
- Download progress bars and spinners no longer reset on each poll; only changed
  cards re-render and the active download patches in place.
- Data race between the API encoding a newly queued download and the worker
  mutating it: the manager now returns a snapshot.
- SaveState no longer holds the state lock across the disk write, so status and
  progress updates are not blocked on the 30s save timer.

## [2026.06.15]

Initial release.
