# Troubleshooting

Headings are the messages fstui shows.

## `fstui needs OpenZFS 2.3 or newer`

fstui reads the JSON output that OpenZFS 2.3 added to `zfs` and `zpool`. The message names the userland and kernel versions found; both must be 2.3 or newer. After upgrading the package, the kernel module is the old one until it is reloaded or the machine restarts.

## `zfs` or `zpool` is not found

```
fstui: resolving zfs: not found in system directories
```

fstui runs `zfs` and `zpool` only from `/usr/sbin`, `/usr/bin`, `/sbin` or `/bin`, and only when the file and every directory above it are owned by root and writable by no one else. A copy elsewhere, or one owned by another user, is refused on purpose: fstui may run it as root.

NixOS installs them in `/run/current-system/sw/bin`, which fstui does not search yet.

## `fstui is interactive and needs a terminal`

fstui was started without a terminal, for example from a pipe or a service. `fstui --version` works without one.

## `Terminal is 70x20. fstui needs at least 80x24.`

Make the window larger; the screen comes back as soon as it fits. `q` quits.

## `History needs root` and `ZFS events need root`

`zpool history` and `zpool events` refuse other users. Start fstui with `sudo` to see them.

## `context deadline exceeded after 30s`

A read did not finish in time: 30 seconds for most reads, 10 minutes for listings of datasets and snapshots, which take `zfs` about half a second per thousand snapshots. A pool whose devices stopped answering also hangs `zfs` and `zpool`; `zpool status` in a shell shows whether it is suspended. fstui keeps the last data it read and tries again; `r` tries now.

## A change is listed under "Unavailable"

The reason is next to it: a scrub to pause when none runs, a checkpoint that blocks device changes, a permission that was not delegated. In read-only mode every change can be looked at and saved for later, but not run; `W` switches to write mode.

## `another fstui (PID 1234) is in write mode`

Only one fstui can be in write mode at a time. The other one holds `/run/fstui.lock` (`$XDG_RUNTIME_DIR/fstui.lock` for other users). Leave write mode there with `W`, or quit it.

## `Something changed since you looked, so nothing was done`

Just before running, fstui reads the system again. Something the change depends on differed from what the confirmation showed: a snapshot taken or destroyed, a property set by someone else, a device that changed state, a scrub that finished. The message lists what. Nothing ran; `r` in the window plans the change again against the system as it is.

## `ignored: writable by group or others`

A configuration file, or a directory above it, can be written by someone other than you or root, so fstui does not read it. Fix the mode, for example `chmod 644 ~/.config/fstui/config.yaml` and `chmod 755` on its directories.

## Colours or box characters look wrong

- `NO_COLOR` set to anything turns colours off.
- On the Linux console (`TERM=linux`) fstui uses plain characters; elsewhere, `ascii: true` in the configuration does the same.
- fstui asks the terminal for its background colour and uses the theme's dark or light colours to match. A terminal that does not answer gets the dark colours; another theme (Settings, 9) or a theme file with a `light` block may read better there ([configuration.md](configuration.md#themes)).

## Creating a pool says it needs `lsblk`

fstui lists disks with `lsblk` from util-linux, in the same system directories as `zfs`. Install util-linux.

## Reporting a bug

Open an issue at https://github.com/nuclear-gumbo/fstui/issues with the output of `fstui --version`, the terminal and its size, and what you saw. Report security problems privately ([SECURITY.md](../SECURITY.md)).
