# Making changes

fstui changes nothing until you switch to write mode, and then runs a change only after showing what it does and getting your confirmation.

## Modes

The status bar shows the mode.

- `READ-ONLY`: fstui starts in this mode. Changes can be looked at and saved for later, not run.
- `WRITE`: `W` switches to write mode after a confirmation, or start with `fstui --write`. Only one fstui at a time can be in write mode; it holds the lock `/run/fstui.lock` (for other users, `$XDG_RUNTIME_DIR/fstui.lock`). `W` again returns to read-only.
- `DRY RUN`: `fstui --dry-run` goes through every step and records the commands in the audit log without running them.

## Choosing a change

Select something and press `a`. The menu lists the changes possible now, and below "Unavailable" the others with the reason, such as a scrub to pause when none is running. `n` goes straight to a new snapshot.

| Selection | Changes |
|---|---|
| Filesystem (Datasets, 2) | create a snapshot, a filesystem, an encrypted filesystem or a volume below it; rename; set or inherit a property; mount, unmount; load or unload its key, change its passphrase; promote a clone; destroy |
| Volume (Volumes, 4, or Datasets) | create a snapshot, resize, rename, set or inherit a property, promote, destroy |
| Snapshot (Snapshots, 3) | hold, release a hold, bookmark, clone, rename, roll back to it, destroy |
| Pool (Pools, 1) | start, pause, resume or stop a scrub; check known errors only; trim; initialize free space; clear errors |
| Device (Pools, `enter`, Topology, `j` `k` to a device) | bring online, take offline, clear its errors |
| ARC (5) | set its limits now, or keep them after reboot in `/etc/modprobe.d/zfs.conf` |

In Snapshots and Datasets, `m` marks items and `M` clears the marks. With marks, `a` offers what applies to all of them: hold, release or destroy the marked snapshots, or one snapshot of all the marked datasets.

A change with parameters opens a form. Fields marked `*` must be filled. `enter` moves to the next field and, on the last one, shows the change; `ctrl+enter` shows it from any field. Terminals without the kitty keyboard protocol send ctrl+enter as a plain enter; there `alt+enter` does the same, and the key help names the key that works.

## The confirmation window

Before anything runs, the window shows:

- what the change does and what it destroys, from ZFS's own dry run where there is one (`zfs destroy -nvp`);
- what cannot be undone;
- the exact commands.

If something stands in the way, such as a hold, a clone, newer snapshots or a name that exists, the window says so and the change cannot be confirmed.

How much the confirmation asks depends on the risk:

| Asks | For example |
|---|---|
| `y` | create a snapshot, start a scrub |
| `y`, then `enter` | rename, destroy one snapshot, unmount, change a layout property such as recordsize |
| typing the name | destroy a dataset, destroy several snapshots ("destroy 3 snapshots"), shrink a volume, take the last redundant device offline, roll back past newer snapshots |

Keys pressed in the first moments after the window opens are ignored, so a key meant for the screen before cannot confirm.

## Running

Just before running, fstui reads the system again and checks that what it showed is unchanged: the same dataset, not destroyed and recreated, with the property not changed by someone else. If anything differs, nothing runs and the window says what changed.

Commands run one at a time, without a time limit, and stop at the first failure. The result lists what ran, what failed with ZFS's message, and what did not run.

## Saved plans

`ctrl+s` in the confirmation window saves the change, also in read-only mode. Plans (view 8) lists saved changes with their state: saved, applied, or refused and why. `enter` applies one: fstui plans it again against the system as it is then and refuses if anything it depends on has changed. Each saved plan is named by the SHA-256 of its file, for example to refer to it in a change request, and can be applied once.

Saved plans are files in `/var/lib/fstui/plans` (for other users, `$XDG_STATE_HOME/fstui/plans`, by default `~/.local/state/fstui/plans`). They hold the operation and its parameters; the commands are kept only to show what the plan was, and are never run from the file.

## Passphrases

Passphrases are typed into fields that show stars and are cleared after use. They reach `zfs` on its standard input, never on the command line, in the audit log or in a saved plan. A change with a passphrase cannot be saved for later.

## Audit log

Every step is recorded before it runs and after, as one JSON line, in `/var/log/fstui/audit.log` (readable by root only; for other users `$XDG_STATE_HOME/fstui/audit.log`) and in syslog (facility authpriv, tag `fstui`). Under `sudo`, the record names the invoking user. If the log cannot be written, fstui refuses to change anything. Plans (view 8) shows the log in its second tab; `/` filters it.

## Without root

Datasets whose owner delegated permissions with `zfs allow` can be changed by other users. The menu offers only what was delegated and says what is missing for the rest; the Permissions tab of a dataset shows who may do what. Pool, device and ARC changes need root. Linux lets only root mount, so mount and unmount need root, and a filesystem another user creates or clones stays unmounted.

## Builds without changes

`go build -tags fstui_nowrite ./cmd/fstui` leaves out every part of fstui that can change storage, for monitoring hosts. `fstui --version` lists `write` among the modules when it is included.
