# Contributing to fstui

fstui changes pools that hold people's data, so changes to it are reviewed with that in mind. Contributions are welcome.

## Before you start

Open an issue at https://github.com/nuclear-gumbo/fstui/issues before anything larger than a fix, so the approach can be agreed first. Pull requests go to `main` from a branch and are merged when review and CI pass.

## Building and checking

You need Go 1.27 or newer, or Nix.

```sh
make build          # bin/fstui
make build-demo     # bin/fstui-demo: recorded output, no ZFS needed
make check          # what CI runs
```

The demo container is built from the demo binary: `make build-demo`, then `podman build -f Containerfile.demo -t fstui-demo .`

`make check` runs gofumpt, go vet, staticcheck, golangci-lint, shellcheck, govulncheck, the tests with the race detector, and a build of every module combination. With Nix, `nix develop` provides the tools and `nix develop .#ci -c make check` runs the checks.

Build as your own user, never as root. Integration tests are compiled unprivileged and only the resulting binary runs as root.

## Tests

- Unit tests check behaviour against the man pages and the recorded output in `testdata/zfs/`, which comes from real commands (`scripts/capture-golden.sh`), never written by hand.
- Integration tests create real pools on sparse files and must run as root:

  ```sh
  make integration-build
  sudo sh scripts/integration.sh bin/integration.test
  ```

  Run them on a machine or VM you can afford to lose, with OpenZFS 2.3 or newer.

Test pool safety:

- Test pools live on sparse files under `/var/tmp/fstui-test/`, and their names start with `fstuitest_`. Test code refuses to touch any other pool, and that refusal is tested.
- Every test that creates a pool destroys it when it ends. `sudo make test-clean` removes leftovers: only `fstuitest_` pools and their files.
- Do not run pool tests in containers: a container shares the host's kernel and its pools.

## Commits

- One logical change per commit. The subject is in the imperative and under 72 characters, then a blank line and a body that says why.
- No `Co-Authored-By`, "Generated with" or similar attribution lines.
- Never commit secrets, serial numbers or other details of a real machine, or build output.
- Add a line to `CHANGELOG.md` under "Unreleased" for anything a user would notice.

## Code

- Only `internal/execute` starts processes. Commands are argument lists with `--` before operands, never a shell.
- Text from ZFS is untrusted: keep it in `termtext.Text` until it is drawn.
- Wrap errors with context and keep the command's error output. Comments explain why, not what.
- Check every `zfs` and `zpool` flag against the installed man page.

## License

fstui is licensed under GPL-3.0-or-later. By contributing you agree that your contribution is licensed under the same terms.
