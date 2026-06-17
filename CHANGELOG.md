# Changelog

All notable changes to this project are documented here. Releases use calendar
versioning (CalVer): `YYYY.MM.DD`.

## [Unreleased]

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
