# fstui

fstui is a terminal interface for ZFS on Linux. It shows pools, devices, datasets, snapshots, volumes, the ARC, I/O and kernel events, and makes changes to them with `zfs` and `zpool`, each one previewed and confirmed first.

[![CI](https://github.com/nuclear-gumbo/fstui/actions/workflows/ci.yml/badge.svg)](https://github.com/nuclear-gumbo/fstui/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/nuclear-gumbo/fstui)](https://github.com/nuclear-gumbo/fstui/releases)

![The Pools view, with the selected pool's devices](docs/images/pools.png)

fstui starts read-only. Changes need write mode (`W`), show the exact commands and what they destroy before anything runs, are checked against the system again just before they run, and are recorded in an audit log.

fstui is below 1.0. A minor release may change keys or settings; [CHANGELOG.md](CHANGELOG.md) says which.

## What it shows

- Pools: health, capacity, fragmentation, scrubs, device errors, the device tree, space by dataset, properties with help text, features, history.
- Datasets: the hierarchy, space by reason, properties, snapshots, delegated permissions.
- Snapshots and volumes across all pools; what changed since a snapshot; the size of a `zfs send` before running it.
- The ARC, I/O per pool and device, and kernel events.
- Tune: whether a pool's settings fit how it is used, measured while you watch. Write amplification, record size against request size, where memory would buy speed, what limits the pool, how well data compresses.

## What it changes

- Snapshots: create, hold, release, bookmark, clone, rename, roll back, destroy.
- Filesystems and volumes: create, rename, resize, set and inherit properties, mount, unmount, promote, destroy; encryption keys.
- Pools: create, add devices, checkpoints, scrub, trim, initialize, clear errors, import, export, upgrade.
- Devices: attach, replace, detach, remove, online, offline; RAIDZ expansion.
- The ARC's size limits, and the two module parameters Tune can point to.

[docs/changes.md](docs/changes.md) lists every change and how it is confirmed.

## Requirements

- Linux with OpenZFS 2.3 or newer, userland and kernel module. fstui refuses older versions and names the one it found.
- `zfs` and `zpool` in `/usr/sbin`, `/usr/bin`, `/sbin` or `/bin`, owned by root.
- A terminal of at least 80 by 24 cells.

## Install

```sh
v=0.7.1
curl -LO https://github.com/nuclear-gumbo/fstui/releases/download/v$v/fstui-$v-linux-amd64
curl -LO https://github.com/nuclear-gumbo/fstui/releases/download/v$v/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
sudo install -m 0755 fstui-$v-linux-amd64 /usr/local/bin/fstui
```

`go install`, Nix and building from source: [docs/install.md](docs/install.md).

To try fstui without ZFS, the demo container runs it on recorded output:

```sh
podman run -it --rm ghcr.io/nuclear-gumbo/fstui-demo
```

## Start

```
fstui                 read-only
sudo fstui --write    start in write mode
fstui --dry-run       record confirmed changes without running them
```

`?` lists the keys of the current view. `0` to `9` switch views, `a` opens the changes for the selection.

## Documentation

- [docs/install.md](docs/install.md): every way to install, and building without the code that changes storage
- [docs/usage.md](docs/usage.md): the views and what they show
- [docs/keys.md](docs/keys.md): every key
- [docs/changes.md](docs/changes.md): write mode, confirmations, saved plans, the audit log, changes without root
- [docs/configuration.md](docs/configuration.md): the configuration file, themes, rebinding keys
- [docs/troubleshooting.md](docs/troubleshooting.md): messages and what to do about them

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md). Report security problems privately as described in [SECURITY.md](SECURITY.md).

## License

GPL-3.0-or-later. See [LICENSE](LICENSE). The shipped themes credit their palettes' authors in `internal/theme/themes/`.
