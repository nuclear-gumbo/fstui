# Using fstui

fstui has ten views, numbered 0 to 9; the number keys switch between them. Lists move with `j` and `k` (or the arrows), `enter` opens the selected item's detail, and `h` and `l` switch the detail's tabs. `?` lists every key of the current view; [keys.md](keys.md) has them all.

Data is read again in the background (pools every 5 seconds, datasets every 15, the ARC every second while shown; see [configuration.md](configuration.md)). The status bar shows the host, the mode, whether fstui runs as root, and how old the data is. `r` reads now. `b` switches every size between IEC units (`1.5 GiB`) and exact bytes.

## 0 Dashboard

One screen for leaving open: the pools with health, capacity, errors and warnings; recent error and warning events; I/O per pool; the ARC's size and hit ratio; and running scrubs, resilvers, RAIDZ expansions and vdev removals with their progress. `fstui --dashboard` starts here.

## 1 Pools

Every imported pool with health, capacity, fragmentation and device errors. Below the list, warnings: a degraded pool, a pool over the capacity or fragmentation threshold, an overdue scrub, a checkpoint held for long. `x` hides a warning for the session.

The detail has five tabs:

- **Topology**: the device tree with states and error counts; ZFS's explanation of a problem and what to do; scrub, resilver, expansion and removal progress; the checkpoint; which systemd timer or cron job scrubs the pool. `enter` moves into the tree to select a device, whose changes `a` then offers.
- **Capacity**: which datasets hold the space.
- **Properties**: every pool property with its source and a line of help.
- **Features**: feature flags and their states.
- **History**: `zpool history`; `i` adds internal events. Reading it needs root.

## 2 Datasets

Filesystems and volumes as a tree; `h` and `l` collapse and expand. The detail's tabs:

- **Capacity**: used space by reason (the dataset's own data, snapshots, children, reservations), written since the last snapshot, compression ratio.
- **Properties**: every property with its source and help.
- **Snapshots**: the dataset's snapshots; `s` and `S` sort.
- **Permissions**: what `zfs allow` delegates on the dataset, and to whom.

## 3 Snapshots

Every snapshot of every pool, with creation time, used and referenced space, holds and clones. The list is read once when the view opens; `r` reads it again. `s` and `S` sort.

- `m` marks snapshots for one change to several (hold, release, destroy); `M` clears the marks.
- `=` shows what changed between the snapshot and now (`zfs diff`).
- `e` estimates how large a `zfs send` of the marked snapshots, or of the selected one, would be: a full stream of the oldest per dataset, then increments. `c`, `w` and `I` switch to compressed, raw, or every snapshot in between; `t` sets a capacity such as a tape (`18TB` is decimal, `16T` binary) and shows how full it gets. The commands are shown for copying; nothing is sent.

## 4 Volumes

Every volume with its size, used and referenced space, block size, whether it is sparse, encryption, and its device under `/dev/zvol`.

## 5 ARC

The ARC's size against its target, minimum and maximum, and the memory available; its size and hit ratio over the last five minutes; what it holds (data and metadata, recently and frequently used, compressed size); and the L2ARC when there is one. `a` changes the size limits.

## 6 I/O

Read and write rates and their peaks per pool, from `zpool iostat`. The selected pool's rates over the last ten minutes as charts, and its devices' operations, rates and wait times over the last second.

## 7 Events

What the kernel reported (`zpool events`), newest first, with the selected event's fields on the right. `/` filters by text; `P`, `v` and `c` step through pools, minimum severities and classes; `f` steps through saved filters ([configuration.md](configuration.md)). `F` follows the newest event; moving away from it pauses, and new events are counted.

## 8 Plans

Two tabs: changes saved for later, and the audit log of every change fstui has run. `enter` on a saved plan makes it, after planning it again against the system as it is then. `/` filters the audit log. [changes.md](changes.md) describes both.

## 9 Settings

The settings in effect, where each comes from (default, `/etc/fstui/config.yaml`, or your file), and a description. `enter` edits a value; `p` shows the change to your file as a diff, `w` writes it, `u` restores the previous version, `d` discards unsaved edits.

## Without root

Everything is shown to other users except pool history, which `zpool history` refuses them. Changes need root, or permissions delegated with `zfs allow` for dataset and snapshot changes ([changes.md](changes.md#without-root)).
