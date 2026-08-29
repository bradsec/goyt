# Changelog

All notable changes to this project are documented here. Releases use calendar
versioning (CalVer): `YYYY.MM.DD`.

## [Unreleased]

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
