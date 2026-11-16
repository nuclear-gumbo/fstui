# Changelog

All notable changes to fstui are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Below 1.0, a minor release may change keys, settings or flags; such changes are listed under "Changed".

## [Unreleased]

## [0.6.1] - 2026-11-16

Fixes and documentation. On GitHub this is the first release with the changes of 0.6.0 (pools and devices, the send size estimate), which was published only on the development forge; they are listed under 0.6.0 below. Tested with OpenZFS 2.3.9 on Debian 13: unit tests on recorded output, and integration tests on pools built from sparse files and on a whole disk.

### Changed

- The README is a short overview. The manual is in `docs/`: installation, the views, every key and change menu letter, changes and how they are confirmed, configuration, and troubleshooting by the messages fstui prints.

### Fixed

- `a` in a view without changes (I/O, Events, the dashboard) opened a seven-line message saying fstui cannot change pools or disks, which has been wrong since 0.6.0. It now names the views that have changes, in one sentence.
- The empty Plans view said changes are made only in Datasets, Snapshots and Volumes. It now says how a change is saved, in one line.

## [0.6.0] - 2026-11-16

Pools and their devices: create a pool, add devices, attach, replace, detach and remove them, widen a raidz vdev, take and use checkpoints, import, export and upgrade; and the size of a `zfs send` before running it.

### Added

- **New pools**: Pools offers "Create a new pool" (`a`, then `N`), also when no pool is imported. It lists the disks and partitions from lsblk with their stable `/dev/disk/by-id` names, sizes and models, and lists the ones that cannot be used with the reason (mounted, swap, held by device-mapper, md or LVM, in a pool, read-only, smaller than 64 MiB, a disk with partitions). `space` sets each device's role (data, log, cache, special, spare); layouts are stripe, mirror and raidz1 to raidz3; options are ashift, compression, autotrim and the mountpoint. A device holding something (an old pool's label, a filesystem) is used only with "overwrite" switched on, and is then listed as destroyed. The preview shows ZFS's own dry run (`zpool create -n`) and the usable capacity; the pool name is typed to confirm, and the devices are read again just before creating it. Needs root and `lsblk`.
- **Adding devices**: on a pool, "Add devices" (`a`, then `A`) uses the same form: a data vdev of the pool's own layout, log, cache, special and spare devices. A data vdev with other redundancy than the pool's is refused, as is a single special device. "Take a checkpoint first" lets you rewind the pool if the result is wrong. The preview shows `zpool add -n` and says when the new vdev can never be removed (any pool with raidz).
- **Checkpoints**: on a pool, take a checkpoint (`C`), discard it (`D`), look at the pool as it was at the checkpoint (`L`: read-only, nothing written; export and import it again to return), or rewind to it (`w`). Rewinding lists the filesystems, volumes and snapshots created since the checkpoint, which it removes, and you confirm by typing the pool name. It is refused for a pool that holds the running system's `/`. Taking one is refused unless the pool's `zpool_checkpoint` feature is enabled.
- **Attach, replace, detach**: on a device in a pool's Topology, attach another device to it (`a`, making or widening a mirror), replace it (`r`), or detach it from its mirror (`d`). The new device is chosen from the same disk list as for a new pool, and a device smaller than the one it mirrors or replaces is refused. Detaching is refused when no other side is online, and asks for the typed name when it leaves the mirror without redundancy. All three are refused while the pool has a checkpoint, as ZFS refuses them.
- **RAIDZ expansion**: attaching a device to a member of a raidz vdev (`a` in Topology) widens the vdev by one device (OpenZFS 2.3). The preview says that data already written keeps its old ratio of data to parity until rewritten, and that a raidz vdev never gets narrower again, so you confirm by typing the name. Topology and the dashboard show how far an expansion has got.
- **Removing vdevs**: on a device in Topology, `R` removes it from the pool with its vdev (the whole mirror for a mirror's member), and on a pool `X` cancels a removal that is running. Log, cache and spare devices go without a trace. A data, special or dedup vdev has its data copied to the other vdevs first, which leaves a table of moved blocks in the pool for good, so you confirm it by typing the name. It is refused in a pool with raidz vdevs and when the other vdevs have no room for its data. Topology and the dashboard show the progress.
- **Import and export**: `x` on a pool exports it, after you type its name; it is refused for the pool holding the running system's `/`. `I` (on Pools, also when no pool is imported) lists the pools that `zpool import` finds, with their state and devices, and imports the one you choose: read-only, without mounting, or under a new name if you like. A pool zpool says was last used by another system is not imported: fstui does not force it.
- **Upgrade**: `u` on a pool enables the features this system supports and the pool does not have yet, after you type its name. The preview lists the features to be enabled and those the pool's `compatibility` setting keeps disabled; fstui reads the compatibility files for this, because `zpool upgrade`'s own listing ignores them. For a pool that boots the system and has no compatibility setting, it warns that a boot loader may not read the new features.
- **Send size**: `e` in Snapshots estimates how large a `zfs send` of the marked snapshots, or the selected one, would be: a full stream of the oldest per dataset, then increments, from ZFS's own dry run (`zfs send -n -P`). `c`, `w` and `I` switch compressed, raw and every-snapshot-between streams; `t` sets a capacity such as a tape (`18TB` decimal, `16T` binary) and a gauge shows how full it gets. The commands are shown quoted for a shell; nothing is sent. A dataset whose key is not loaded needs a raw send, and an encrypted dataset sent without raw is flagged as sent decrypted.

### Changed

- Messages name the pool, device, dataset or vdev they are about: "tank has no scrub running", "/dev/sdb is already online", "the key of tank/home is not loaded", instead of "it" or "the pool". A long reason in the change menu wraps instead of being cut off. With no pool imported, Pools says which keys import or create one.
- Less memory with many snapshots. Measured with 100,000: each snapshot takes 288 bytes instead of 442, reading the list peaks 140 MiB above the start instead of 178, and the Snapshots view no longer copies and sorts the whole list each time it redraws (7.5 ms and 0.3 MB per redraw instead of 76 ms and 6.4 MB).

### Fixed

- A vdev named `raidz` or `draid` without a parity digit, or a scan without a function name, no longer stops fstui.

- A change that needs a tool fstui did not find at startup (`tee`, `cp`, `chmod`, `mv`) is refused before anything runs, naming the tool, instead of stopping halfway.

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

[Unreleased]: https://github.com/nuclear-gumbo/fstui/compare/v0.6.1...HEAD
[0.6.1]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.6...v0.6.1
[0.6.0]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.6...v0.6.1
[0.5.6]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.5...v0.5.6
[0.5.5]: https://github.com/nuclear-gumbo/fstui/releases/tag/v0.5.5
