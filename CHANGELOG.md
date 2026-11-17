# Changelog

All notable changes to fstui are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Below 1.0, a minor release may change keys, settings or flags; such changes are listed under "Changed".

## [Unreleased]

## [0.7.1] - 2026-11-16

Memory and large systems. Measured with 331 datasets and 66,200 snapshots on OpenZFS 2.3.9.

### Changed

- Peak memory across all views: 248 MiB → 59 MiB. Listings are parsed while `zfs` prints them; a snapshot row uses an eighth of the memory.
- History tab keeps the newest 10,000 commands and 10,000 internal events. ZFS logs an internal event for every snapshot, so an old pool's history runs to millions of lines.
- After a slow read, the next waits at least four times as long, so `zfs` is busy at most a fifth of the time.
- View bar at 80 columns: `dash`, `data`, `snaps`, `vols` instead of names cut to four letters.
- Dashboard on short terminals: the health panel gets up to two fifths of the height.
- Tune: shorter findings; module parameters are only raised, never lowered.

### Fixed

- Snapshots view never loaded with tens of thousands of snapshots: `zfs list` ran past the 30 s limit. Listings now get 10 minutes.
- History tab re-rendered every entry on each key press; a very long history could fail at the 256 MiB output limit.
- Switching pools with the Tune tab open could mix the first pool's measurements into the second's.
- Volumes: device path cut from the left (`…/tank/vol`), not the right; block size and allocation columns fit their values.
- Docs: kernel events need root, like pool history.

## [0.7.0] - 2026-11-16

### Added

- Tune tab in each pool's detail: measures the pool while shown and answers five questions, each with a verdict, the deciding figure and what would change it. No root needed.
  - Write amplification, with the cause named: sync writes through the ZIL, read-modify-write, raidz padding.
  - Record size against request size, per dataset.
  - Where memory would buy speed: ARC ghost hits, unused prefetch, metadata misses, L2ARC overhead.
  - What limits the pool: write throttle, slow transaction groups, memory, a slow device, ZFS queues, ZIL stalls.
  - How well data compresses; `c` reads file metadata to find what does not.
- `a` in the Tune tab offers the changes a finding points to: `recordsize`, `compression=lz4`, `zfs_prefetch_disable` or `zfs_arc_meta_balance` until reboot, ARC limits.

### Changed

- Pool detail has six tabs; `h` from Topology goes to Tune.

## [0.6.1] - 2026-11-16

First GitHub release with the 0.6.0 changes below.

### Changed

- The manual moved to `docs/`: install, views, keys, changes, configuration, troubleshooting. The README is an overview.

### Fixed

- `a` in a view without changes showed a seven-line message, wrong since 0.6.0; it now names the views that have changes.
- The empty Plans view named the wrong views; it now says how to save a change.

## [0.6.0] - 2026-11-16

### Added

- Create a pool (`a`, `N` in Pools): disks from `lsblk` by `/dev/disk/by-id` name, with the reason any cannot be used; stripe, mirror, raidz1–3; log, cache, special and spare devices; preview from `zpool create -n`. Needs root.
- Add devices to a pool (`A`), optionally after a checkpoint. Mismatched redundancy and a single special device are refused.
- Checkpoints: take (`C`), discard (`D`), look read-only (`L`), rewind (`w`). Rewinding discards everything written since the checkpoint and lists what it removes; refused for the pool holding `/`.
- Device changes in Topology: attach (`a`), replace (`r`), detach (`d`), remove with its vdev (`R`); cancel a removal on the pool (`X`). A smaller device, or detaching the last online side, is refused.
- RAIDZ expansion: attaching to a raidz member widens the vdev. Data already written keeps its old data-to-parity ratio until rewritten, and the vdev can never be made narrower again. Progress shows in Topology and on the dashboard.
- Import (`I`): read-only, without mounting, or under a new name. A pool last used by another system is not imported, since forcing it can destroy it. Export (`x`): refused for the pool holding `/`.
- Upgrade (`u`): lists the features it enables and those the pool's `compatibility` setting keeps off.
- Send size (`e` in Snapshots): how large a `zfs send` of the marked snapshots would be, from `zfs send -n -P`, as compressed, raw or intermediate streams. `t` sets a capacity, such as a tape, and shows how full it would get. Nothing is sent.
- Pool-wide changes that remove data or cannot be undone (rewind, export, upgrade, removing a data vdev, RAIDZ expansion) ask for the pool name.

### Changed

- Messages name their subject: "tank has no scrub running", not "it has no scrub running".
- 100,000 snapshots: 288 bytes each instead of 442; a redraw takes 7.5 ms instead of 76 ms.

### Fixed

- A `raidz` or `draid` vdev without a parity digit, or a scan without a function name, crashed fstui.
- A change needing `tee`, `cp`, `chmod` or `mv` that fstui did not find stopped halfway; it is now refused before anything runs.

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

[Unreleased]: https://github.com/nuclear-gumbo/fstui/compare/v0.7.1...HEAD
[0.7.1]: https://github.com/nuclear-gumbo/fstui/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/nuclear-gumbo/fstui/compare/v0.6.1...v0.7.0
[0.6.1]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.6...v0.6.1
[0.6.0]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.6...v0.6.1
[0.5.6]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.5...v0.5.6
[0.5.5]: https://github.com/nuclear-gumbo/fstui/releases/tag/v0.5.5
