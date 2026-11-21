# Installing fstui

fstui is one static binary, `fstui`. It needs Linux with OpenZFS 2.3 or newer (userland and kernel module; `zfs version` shows both) and a terminal of at least 80 by 24 cells. Building needs Go 1.27 or newer.

## Release binary

Each [release](https://github.com/nuclear-gumbo/fstui/releases) has:

- `fstui-<version>-linux-amd64`: the binary, built without cgo;
- `fstui-<version>.tar.xz`: the source with its Go dependencies, for building offline;
- `SHA256SUMS`: checksums of both.

```sh
v=0.8.6
curl -LO https://github.com/nuclear-gumbo/fstui/releases/download/v$v/fstui-$v-linux-amd64
curl -LO https://github.com/nuclear-gumbo/fstui/releases/download/v$v/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
sudo install -m 0755 fstui-$v-linux-amd64 /usr/local/bin/fstui
```

## Go

```sh
go install github.com/nuclear-gumbo/fstui/cmd/fstui@latest
```

The binary lands in `$(go env GOPATH)/bin`. `fstui --version` reports the module version.

## Nix

```sh
nix run github:nuclear-gumbo/fstui
```

On NixOS, add the flake as an input (`inputs.fstui.url = "github:nuclear-gumbo/fstui";`) and `inputs.fstui.packages.x86_64-linux.default` to `environment.systemPackages`. NixOS keeps `zfs` in `/run/current-system/sw/bin`, where fstui does not look yet; see [troubleshooting.md](troubleshooting.md#zfs-or-zpool-is-not-found).

## From source

```sh
git clone https://github.com/nuclear-gumbo/fstui.git
cd fstui
make build
sudo make install
```

`make build` writes `bin/fstui`. `make install` copies it to `/usr/local/bin` and never compiles, so nothing is built as root. `PREFIX`, `DESTDIR` and a packager's `GOFLAGS` are honoured.

Distribution packages of OpenZFS 2.3: on Debian 13, `zfsutils-linux` and `zfs-dkms` in `contrib`, with Go 1.27 in `trixie-backports`; on Gentoo, `sys-fs/zfs` and `sys-fs/zfs-kmod` 2.3 or newer.

## Without the code that changes storage

```sh
go build -tags fstui_nowrite -o fstui ./cmd/fstui
```

This binary only reads: write mode, the change menus, schedule changes, saved plans and the audit log are not in it. `fstui check` and `fstui watch` are, since they change nothing. `fstui --version` lists the compiled-in modules; `write` is absent from this build.

## Demo container

```sh
podman run -it --rm ghcr.io/nuclear-gumbo/fstui-demo
```

The demo runs fstui on output recorded from OpenZFS 2.3.9 pools, with simulated I/O and ARC activity. It runs no `zfs` or `zpool` command, needs no ZFS, and behaves as fstui does on a real system:

- In write mode every change is carried out on the demo's copy of the pools, in memory: snapshots, filesystems and volumes; scrubs, device states and checkpoints; creating, importing, exporting and upgrading pools; adding, attaching, replacing, detaching and removing devices; rewinding to a checkpoint; the ARC limits.
- The Tune tab measures kernel statistics replayed from a real pool under known workloads.
- The Schedules tab changes schedules in a systemd of the demo's own, in a temporary directory removed when fstui exits.
- `fstui check` and `fstui watch` look at the demo's pools and print each message instead of sending it; a test message from the Notifications tab is not sent either.

`docker run` works the same way.
