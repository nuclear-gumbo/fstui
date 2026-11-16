# Changelog

All notable changes to fstui are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Below 1.0, a minor release may change keys, settings or flags; such changes are listed under "Changed".

## [Unreleased]

## [0.5.6] - 2026-11-15

Security and bug fixes.

### Security

- `/etc/modprobe.d/zfs.conf`, written when ARC limits are kept after reboot, is always mode 0644. It took fstui's umask before, and could be group-writable.
- fstui keeps its umask at least 022 and marks itself not dumpable: no core dump holds a passphrase, and other processes of the same user cannot read its memory.
- A passphrase reaches only the command that reads it, not the `zfs mount` steps after `zfs load-key`.
- The demo's temporary directory and the saved plans directory are refused unless they belong to the user and nobody else can open them.
- Settings saves exactly the file shown for review.
- Command output is read up to 256 MiB; a larger listing fails with a message.
- Event filter values are sanitised before they are drawn.
- Integration tests refuse a pool directory under `/var/tmp` that another user could control.
- Built with Go 1.27.4.

### Fixed

- If fstui cannot find your config file, Settings says so and edits nothing, instead of editing `/etc/fstui/config.yaml`.
- On a terminal smaller than 80x24, the size message fits the screen.

## [0.5.5] - 2026-11-15

First public release. Earlier versions were not published.

### Added

- Views of pools, datasets, snapshots, volumes, the ARC, I/O and events, and a dashboard.
- Changes to snapshots, filesystems, volumes, encryption keys, scrubs, trim, devices and the ARC's limits, in write mode, each shown with its commands and confirmed first.
- Saved plans, an audit log, dry-run mode, and changes without root through `zfs allow` delegation.
- Themes, key rebinding, a Settings view, mouse support and a plain-character mode.
- A demo container that runs on recorded output without ZFS.
- `fstui --version` reports the module version for binaries built with `go install`.

[Unreleased]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.6...HEAD
[0.5.6]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.5...v0.5.6
[0.5.5]: https://github.com/nuclear-gumbo/fstui/releases/tag/v0.5.5
