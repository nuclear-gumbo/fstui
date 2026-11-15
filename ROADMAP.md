# Roadmap

Planned work. [CHANGELOG.md](CHANGELOG.md) lists what has shipped.

fstui 1.0 targets a stable, secure release with the following roadmap items shipped. Patch releases fix bugs between them.

## Feature requests

Feature requests are welcome. Open an [issue](https://github.com/nuclear-gumbo/fstui/issues/new/choose) labeled **feature request**, describing what you want to do and why.

## Milestones

| Version | Milestone | Scope |
|---|---|---|
| 0.6 | ZFS pools and devices | Pool creation with disk safety checks; add, attach, detach, replace and remove devices; resilver progress after fstui exits; RAIDZ expansion; checkpoint discard and rewind; import, export and upgrade. |
| 0.7 | ZFS tune mode | A monitoring and tuning mode alongside the current one, for systems whose pools rarely change. Views for the dbuf cache, prefetch, the ZIL, transaction groups, per-dataset I/O, and latency and queue histograms. ZFS module parameters with their descriptions and defaults, changed with the same confirmation and audit log as other changes. |
| 0.8 | Remote hosts and Proxmox VE | ZFS on other hosts over SSH, with key authentication and known hosts only. Proxmox VE clusters: nodes and their ZFS storage, per node and per cluster. Read-only first. |
| 0.9 | Send and receive, packaging | `zfs send` and `zfs receive` with progress, locally and to other hosts over SSH. Man page. Gentoo ebuild. |
| 0.9.5 | Security review | A review of all code to this point, including the SSH layer, with the findings fixed and published. |
| 0.10 | Btrfs | Filesystems, devices, profiles, subvolumes and snapshots; scrub and balance status and control. |
| 0.11 | XFS and ext4 | Filesystems, space, mount state and health, read-only. |
| 0.12 | Repair | Guided use of each filesystem's own repair tools, with read-only checks first. Btrfs subvolume and snapshot changes. |
| 0.13 | Recovery | ZFS and Btrfs recovery tools; hardening across all backends. |
| 0.14 | FreeBSD | FreeBSD support, with ZFS from base and from ports. |
| 1.0 | Stable | All of the above stable on Linux and FreeBSD, with no known data-safety bugs. |

## Later

- Read-only views of snapshot tools: sanoid, syncoid, zfs-auto-snapshot and zrepl.

## Not scheduled

OpenZFS 2.2 support, LVM, notifications.

**Roadmap items may be added or removed.**
