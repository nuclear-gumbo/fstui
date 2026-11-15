# Changelog

All notable changes to fstui are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Below 1.0, a minor release may change keys, settings or flags; such changes are listed under "Changed".

## [Unreleased]

## [0.5.5] - 2026-11-15

First public release. Earlier versions were not published.

### Added

- Views of pools, datasets, snapshots, volumes, the ARC, I/O and events, and a dashboard.
- Changes to snapshots, filesystems, volumes, encryption keys, scrubs, trim, devices and the ARC's limits, in write mode, each shown with its commands and confirmed first.
- Saved plans, an audit log, dry-run mode, and changes without root through `zfs allow` delegation.
- Themes, key rebinding, a Settings view, mouse support and a plain-character mode.
- A demo container that runs on recorded output without ZFS.
- `fstui --version` reports the module version for binaries built with `go install`.

[Unreleased]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.5...HEAD
[0.5.5]: https://github.com/nuclear-gumbo/fstui/releases/tag/v0.5.5
