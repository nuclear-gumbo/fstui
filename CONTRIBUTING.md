# Contributing to fstui

fstui runs as root and changes pools that hold data. Changes are reviewed with that in mind.

## Issues

Report bugs at https://github.com/nuclear-gumbo/fstui/issues. Include:

- the output of `fstui --version`;
- the terminal, its size, and the theme;
- whether fstui ran as root;
- what you did, what you expected, and what happened.

Report security problems privately, as described in [SECURITY.md](SECURITY.md).

## Before you start

Open an issue before any change larger than a fix, so the approach is agreed before you write it.

## Workflow

1. Fork the repository and clone your fork.
2. Create a branch from `main`, named after the change, e.g. `fix-io-peak`.
3. Make the change, with tests.
4. Run `make check`.
5. Open a pull request against `main`.

## Building

Requirements: Go 1.27 or newer, or Nix.

```sh
make build          # bin/fstui
make build-demo     # bin/fstui-demo: recorded output, no ZFS needed
make check          # the full checks
```

`make check` runs gofumpt, go vet, staticcheck, golangci-lint, shellcheck, govulncheck, the tests with the race detector, and a build of every module combination. With Nix, `nix develop` provides the tools. CI runs the build, the race tests, the module combinations and golangci-lint on every pull request; the maintainer also runs the integration tests against real ZFS pools before a release.

Build as your own user, never as root.

The demo image: `make build-demo`, then `podman build -f Containerfile.demo -t fstui-demo .`

## Tests

- Unit tests check behaviour against the man pages and the recordings in `testdata/zfs/`.
- Recordings come from real commands (`scripts/capture-golden.sh`). Do not write or edit them by hand.
- Integration tests create real pools and run as root. Build them unprivileged:

  ```sh
  make integration-build
  sudo sh scripts/integration.sh bin/integration.test
  ```

  Run them only on a machine or VM you can afford to lose, with OpenZFS 2.3 or newer.

Test pool rules:

- Test pools are named `fstuitest_*` and live on sparse files under `/var/tmp/fstui-test/`. Test code refuses any other pool.
- Each test destroys the pools it creates. `sudo make test-clean` removes leftovers.
- Do not run pool tests in containers. A container shares the host's kernel and pools.
- No test touches a real block device unless you list its `/dev/disk/by-id` path, one per line, in `testdata/allowed-devices.conf` (ignored by git). `scripts/integration.sh` wipes every listed device before the tests (`wipefs -a` on its partitions and the disk) and refuses to run if one is mounted or in an imported pool. List only a disk whose contents you do not need.

Schedules under OpenRC and cron run in throwaway rootless containers (Gentoo with OpenRC, Debian with cron), since a development machine usually has only one of them: `sh scripts/test-schedules-container.sh`, as your own user, with Podman. These tests change the system they run on; never run them outside a container.

## Measuring memory

A build with the tag `fstui_profile` writes a heap profile every 5 seconds to `$FSTUI_HEAPPROFILE`. Release builds do not have it.

```sh
go build -tags fstui_profile -o bin/fstui-profile ./cmd/fstui
FSTUI_HEAPPROFILE=/tmp/heap.pprof bin/fstui-profile --dry-run
go tool pprof -top -inuse_space bin/fstui-profile /tmp/heap.pprof
```

`-sample_index=alloc_space` shows everything allocated since start, which finds short spikes. Measure on a test pool with many snapshots.

## Commits

- One logical change per commit. Squash fixups before review.
- Subject: `area: summary`, imperative, at most 72 characters, no full stop. The area is the package or file changed, e.g. `zfs/plan: block rollback past a clone` or `README: fix the Nix example`.
- Body: why the change is needed, and anything a reviewer cannot see in the diff.
- No `Co-Authored-By`, "Generated with" or other attribution trailers.
- No secrets, serial numbers, host names or other details of a real machine, and no build output.

## Pull requests

The description states:

- what changed and why, with the related issue;
- how it was tested: unit tests, integration tests (with the OpenZFS version), or by hand (with the terminal);
- for UI changes, a screenshot.

List only what you did. A change a user would notice adds a line to `CHANGELOG.md` under "Unreleased".

CI must pass.

## Review

- Review comments do not block a merge unless the reviewer marks the review "Request changes".
- Answer every comment, by changing the code or by saying why not.
- Maintainers may push small fixes, such as commit messages or formatting, to your branch before merging, and will say what they changed. To prevent this, clear "Allow edits and access to secrets by maintainers".

## Code

[docs/codebase.md](docs/codebase.md) maps the packages, how a read and a change flow through them, and what to touch to add a setting, a view, a change, a notification type or a backend.

- A number or choice that shapes what fstui shows or does is a setting in `internal/config/settings.go`, not a constant; `make docs` adds it to the reference.
- Only `internal/execute` starts processes: argument lists with `--` before operands, never a shell.
- Text from ZFS is untrusted. Keep it in `termtext.Text` until it is drawn.
- Wrap errors with context and keep the command's error output.
- Check every `zfs` and `zpool` flag against the installed man page.
- Comments explain why, not what.

## Responsibility

You are responsible for every line you submit, however it was produced. Understand it, review it before submitting, and be able to answer questions about it.

## License

fstui is licensed under GPL-3.0-or-later. Contributions are accepted under the same license.
