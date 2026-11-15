# fstui

A terminal UI for ZFS on Linux: pools, devices, datasets, snapshots, volumes, the ARC and I/O, and the changes you make to them.

[![CI](https://github.com/nuclear-gumbo/fstui/actions/workflows/ci.yml/badge.svg)](https://github.com/nuclear-gumbo/fstui/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/nuclear-gumbo/fstui)](https://github.com/nuclear-gumbo/fstui/releases)
[![License: GPL-3.0-or-later](https://img.shields.io/badge/license-GPL--3.0--or--later-blue)](LICENSE)

<!-- screenshot -->

fstui starts read-only. It reads state with `zfs` and `zpool` and changes nothing until you switch to write mode. Every change is shown first, with what it destroys and the exact commands, then confirmed, checked again just before it runs, and recorded in an audit log.

fstui is below 1.0: features are still being added, and a minor release may change keys or settings. [CHANGELOG.md](CHANGELOG.md) lists every change.

## Features

- **Pools**: health, capacity, fragmentation, last scrub, device errors and warnings; per pool the device tree, scrub and resilver progress, which datasets hold the space, properties with inline help, features and `zpool history`.
- **Datasets**: every filesystem and volume as a tree, with used space by reason, properties, snapshots, and the permissions delegated with `zfs allow`.
- **Snapshots** and **volumes** across all pools, and what changed since a snapshot (`zfs diff`).
- **ARC**: size against its bounds, hit ratio over five minutes, what it holds, and the L2ARC.
- **I/O**: read and write rates and peaks per pool, the selected pool's history as charts, and per-device latency (`zpool iostat`).
- **Events**: what the kernel reported (`zpool events`), filterable by text, severity, pool and class.
- **Dashboard**: pool health, recent errors, I/O, the ARC and running scans on one screen.
- **Changes**: snapshots (create, hold, release, bookmark, clone, rename, roll back, destroy), filesystems and volumes (create, rename, resize, set or inherit properties, mount, promote, destroy), encryption keys, scrubs, trim, device online and offline, and the ARC's limits. Several snapshots or datasets can be changed at once.
- **Saved plans**: save a change and apply it later; it is planned again against the system as it is then.
- Works without root for viewing, and for changes delegated with `zfs allow`.
- Mouse support, themes, vim-style keys that can be rebound, and a plain-character mode for the Linux console.

## Requirements

- Linux with OpenZFS 2.3 or newer, userland and kernel module (`zfs version` shows both). fstui reads the JSON output added in 2.3 and refuses older versions with a message naming the one it found.
- A terminal of at least 80x24 cells.
- To build from source: Go 1.27 or newer.

## Installation

### Release binary

Each [release](https://github.com/nuclear-gumbo/fstui/releases) has a static linux-amd64 binary, a source tarball that includes the Go dependencies for offline builds, and `SHA256SUMS`:

```sh
v=0.5.5
curl -LO https://github.com/nuclear-gumbo/fstui/releases/download/v$v/fstui-$v-linux-amd64
curl -LO https://github.com/nuclear-gumbo/fstui/releases/download/v$v/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
sudo install -m 0755 fstui-$v-linux-amd64 /usr/local/bin/fstui
```

### Go

```sh
go install github.com/nuclear-gumbo/fstui/cmd/fstui@latest
```

### Nix

```sh
nix run github:nuclear-gumbo/fstui
```

On NixOS, add `inputs.fstui.url = "github:nuclear-gumbo/fstui";` to your flake and `inputs.fstui.packages.x86_64-linux.default` to `environment.systemPackages`.

### From source

```sh
git clone https://github.com/nuclear-gumbo/fstui.git
cd fstui
make build
sudo make install
```

`make install` copies `bin/fstui` to `/usr/local/bin` and never compiles, so nothing is built as root. `PREFIX` and `DESTDIR` work as usual, and the Makefile honours a packager's `GOFLAGS`.

On Debian 13, OpenZFS 2.3 is in `contrib` (`zfsutils-linux`, `zfs-dkms`) and Go 1.27 in `trixie-backports`. On Gentoo, `sys-fs/zfs` and `sys-fs/zfs-kmod` must be 2.3 or newer.

### Demo without ZFS

The demo container runs fstui on output recorded from OpenZFS 2.3.9 pools, with simulated I/O and ARC activity. It needs no ZFS and runs no `zfs` or `zpool` commands. Write mode works: changes are carried out on the demo's copy of the pools, in memory.

```sh
podman run -it --rm ghcr.io/nuclear-gumbo/fstui-demo
```

`docker run` works the same way.

## Usage

```
fstui                 start the UI
fstui --dashboard     start on the dashboard
fstui --write         start in write mode
fstui --dry-run       record confirmed changes in the audit log without running them
fstui --superuser     hide advisory warnings (never confirmations)
fstui --list-themes   list the themes fstui can use
fstui --version       version, compiled-in modules, OpenZFS versions
```

Viewing needs no root, except for pool history, which `zpool history` refuses to other users. Changes need root, or permissions delegated with `zfs allow`. Run `sudo fstui` to make changes as root; the audit log then records the invoking user.

fstui runs `zfs` and `zpool` only from `/usr/sbin`, `/usr/bin`, `/sbin` or `/bin`, and only when the file and every directory above it are owned by root and writable by no one else.

## Key bindings

`?` in fstui lists every key for the current view.

| Key | Action |
|---|---|
| `0`-`9` | Dashboard, Pools, Datasets, Snapshots, Volumes, ARC, I/O, Events, Plans, Settings |
| `j` `k`, arrows | Move |
| `enter`, `space` | Open |
| `h` `l` | Previous and next tab; collapse and expand in the dataset tree |
| `esc`, `q` | Back; quit at the top |
| `r` | Refresh now |
| `b` | Exact byte counts or IEC units |
| `a` | Changes for the selection |
| `n` | New snapshot |
| `W` | Write mode on or off |
| `m`, `M` | Mark items for one change to several; clear the marks |
| `=` | What changed since the selected snapshot |
| `/` | Filter (Events, audit log) |
| `ctrl+c` | Quit |

Keys can be rebound in the configuration file.

## Making changes

1. Press `W` and confirm, or start with `fstui --write`. Only one fstui can be in write mode at a time.
2. Select something and press `a` for its changes. A form asks for any parameters; `enter` moves through the fields, and `ctrl+enter` (`alt+enter` in terminals that cannot tell ctrl+enter from enter) goes to the confirmation.
3. The confirmation window shows what the change does, what it destroys (from ZFS's own dry run where there is one), what cannot be undone, and the commands.
4. Confirm with `y`; `y` then `enter`; or by typing the name, depending on the risk.

Before running, fstui checks that what it showed you is unchanged; if not, nothing runs. Each step is recorded before and after it runs in `/var/log/fstui/audit.log` and in syslog. `ctrl+s` saves a change to apply later from Plans (view 8).

[docs/changes.md](docs/changes.md) covers write mode, confirmations, saved plans, passphrases, the audit log and changes without root.

`go build -tags fstui_nowrite` builds a binary that contains no code that changes storage.

## Configuration

Settings are read from `/etc/fstui/config.yaml`, then `~/.config/fstui/config.yaml`; each file overrides the previous one key by key. The Settings view (`9`) edits your file and shows the diff before saving.

```yaml
theme: nord
mouse: true
thresholds:
  capacity_warn: 80
  capacity_error: 90
refresh:
  pools: 5
```

[docs/configuration.md](docs/configuration.md) lists every setting, themes and key rebinding.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report bugs at https://github.com/nuclear-gumbo/fstui/issues with the output of `fstui --version`, your terminal and its size, and what you saw.

Report security problems privately as described in [SECURITY.md](SECURITY.md).

## License

Copyright (C) 2026 nuclear-gumbo

fstui is free software under the GNU General Public License, version 3 or later. See [LICENSE](LICENSE). The shipped themes credit their palettes' authors in `internal/theme/themes/`.
