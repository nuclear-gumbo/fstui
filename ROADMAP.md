# Roadmap

Planned work. [CHANGELOG.md](CHANGELOG.md) lists what has shipped.

fstui 1.0 targets a stable, secure release with the following roadmap items shipped. Patch releases fix bugs between them.

## Feature requests

Feature requests are welcome. Open an [issue](https://github.com/nuclear-gumbo/fstui/issues/new/choose) labeled **feature request**, describing what you want to do and why.

## Shipped

- 0.6: pools and devices: create, add, attach, replace, detach and remove devices; RAIDZ expansion; checkpoints; import, export, upgrade; `zfs send` size estimates.
- 0.7: Tune: whether a pool's settings fit how it is used, with the changes a finding points to.
- 0.8: notifications and schedules: alerts for pool problems and kernel events by ntfy, webhook, Gotify, Pushover, Telegram, Discord, Slack, Matrix or email, sent by the `fstui watch` service; scrub and trim schedules on systemd timers, cron or OpenRC; the Config view.

## Milestones

| Version | Milestone | Scope |
|---|---|---|
| 0.9 | Remote hosts and Proxmox VE | ZFS on other hosts over SSH, with key authentication and known hosts only. Proxmox VE clusters: nodes and their ZFS storage, per node and per cluster. Read-only first. |
| 0.9.5 | Security review | A review of all code to this point, including the SSH layer and the notification senders, with the findings fixed and published. |
| 0.10 | Send and receive, packaging | `zfs send` and `zfs receive` with progress, locally and to other hosts over SSH. Man page. Gentoo ebuild. |
| 0.11 | Btrfs | Filesystems, devices, profiles, subvolumes and snapshots; scrub and balance status and control. |
| 0.12 | XFS and ext4 | Filesystems, space, mount state and health, read-only. |
| 0.13 | Repair | Each filesystem's own checks first, then the repairs its tools run safely on their own: `xfs_repair` and `xfs_scrub`, `e2fsck -p`, Btrfs scrub, and guided steps for ZFS. Btrfs subvolume and snapshot changes. |
| 0.14 | Stabilisation | Hardening and polish across all backends. |
| 0.15 | FreeBSD | FreeBSD support, with ZFS from base and from ports. |
| 1.0 | Stable | All of the above stable on Linux and FreeBSD, with no known data-safety bugs. |

## Later

- Recovery, after 1.0: last-resort tools that can lose data to save the rest, such as `zpool import -F`, `btrfs rescue` and `btrfs restore`, `xfs_repair -L`.
- Read-only views of snapshot tools: sanoid, syncoid, zfs-auto-snapshot and zrepl.

## Not scheduled

OpenZFS 2.2 support, LVM.

**Roadmap items may be added or removed.**
