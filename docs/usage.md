# Using fstui

fstui has ten views, numbered 0 to 9; the number keys switch between them. Lists move with `j` and `k` (or the arrows), `enter` opens the selected item's detail, and `h` and `l` switch the detail's tabs. `?` lists every key of the current view; [keys.md](keys.md) has them all.

Data is read again in the background: by default pools every 5 seconds, datasets every 15, the ARC every second while shown (`refresh:` in [configuration.md](configuration.md#reference)). After a slow read, fstui waits at least four times as long as it took before the next (`limits.slow_read_factor`). The status bar shows the host, the mode, whether fstui runs as root, and how old the data is. `H` lists other hosts and switches to one ([remote.md](remote.md)). `r` reads now. `b` switches every size between IEC units (`1.5 GiB`) and exact bytes.

## 0 Dashboard

One screen for leaving open: the pools with health, capacity, errors and warnings; recent error and warning events; I/O per pool; the ARC's size and hit ratio; and running scrubs, resilvers, RAIDZ expansions and vdev removals with their progress. `fstui --dashboard` starts here.

## 1 Pools

Every imported pool with health, capacity, fragmentation and device errors. Below the list, warnings: a degraded pool, a pool over the capacity or fragmentation threshold, an overdue scrub, a checkpoint held for long. `x` hides a warning for the session.

The detail has six tabs:

- **Topology**: the device tree with states and error counts; ZFS's explanation of a problem and what to do; scrub, resilver, expansion and removal progress; the checkpoint; which systemd timer or cron job scrubs the pool. `enter` moves into the tree to select a device, whose changes `a` then offers.
- **Capacity**: which datasets hold the space.
- **Properties**: every pool property with its source and a line of help.
- **Features**: feature flags and their states.
- **History**: `zpool history`, newest first; `i` adds internal events. The most recent 10,000 commands and 10,000 internal events are kept (`limits.history_entries`). Reading it needs root.
- **Tune**: whether the pool's settings fit how it is used, measured while the tab is open from `/proc/spl/kstat/zfs` and `zpool iostat`; no root needed. Five questions: write amplification, record size against request size, where memory would buy speed, what limits the pool, how well data compresses. Each gets a verdict (fits, worth a look, mismatch, not enough data yet), the figure behind it and what would change it. Write amplification and what limits the pool need a minute of activity (`tune.min_window`; every limit behind the verdicts is a `tune.` setting).
  `r` measures again, `enter` shows the details, `a` offers the changes the findings point to. `c` reads every file's size and allocation for the compression question: metadata only, at most 200,000 files or 60 seconds (`tune.walk_files`, `tune.walk_time`).

## 2 Datasets

Filesystems and volumes as a tree; `h` and `l` collapse and expand. The detail's tabs:

- **Capacity**: used space by reason (the dataset's own data, snapshots, children, reservations), written since the last snapshot, compression ratio.
- **Properties**: every property with its source and help.
- **Snapshots**: the dataset's snapshots; `s` and `S` sort.
- **Permissions**: what `zfs allow` delegates on the dataset, and to whom.

## 3 Snapshots

Every snapshot of every pool, with creation time, used and referenced space, holds and clones. The list is read once when the view opens, which takes `zfs` about half a second per thousand snapshots; `r` reads it again. `s` and `S` sort.

- `m` marks snapshots for one change to several (hold, release, destroy); `M` clears the marks.
- `=` shows what changed between the snapshot and now (`zfs diff`).
- `e` estimates how large a `zfs send` of the marked snapshots, or of the selected one, would be: a full stream of the oldest per dataset, then increments. `c`, `w` and `I` switch to compressed, raw, or every snapshot in between; `t` sets a capacity such as a tape (`18TB` is decimal, `16T` binary) and shows how full it gets. The commands are shown for copying; nothing is sent.

## 4 Volumes

Every volume with its size, used and referenced space, block size, whether it is sparse, encryption, and its device under `/dev/zvol`.

## 5 ARC

The ARC's size against its target, minimum and maximum, and the memory available; its size and hit ratio over the last five minutes (`charts.arc_window`); what it holds (data and metadata, recently and frequently used, compressed size); and the L2ARC when there is one. `a` changes the size limits.

## 6 I/O

Read and write rates and their peaks per pool, from `zpool iostat`. The selected pool's rates over the last ten minutes as charts (`charts.io_window`, `charts.io_keep`), and its devices' operations, rates and wait times over the last second.

## 7 Events

What the kernel reported (`zpool events`), newest first, with the selected event's fields on the right. `/` filters by text; `P`, `v` and `c` step through pools, minimum severities and classes; `f` steps through saved filters ([configuration.md](configuration.md)). `F` follows the newest event; moving away from it pauses, and new events are counted.

## 8 Plans

Two tabs: changes saved for later, and the audit log of every change fstui has run. `enter` on a saved plan makes it, after planning it again against the system as it is then. `/` filters the audit log. [changes.md](changes.md) describes both.

## 9 Config

Four tabs:

- **General**: the settings in groups (display, warnings, refresh, charts, dashboard, events, limits, notifications, schedules, Tune). `enter` opens a group, which lists each setting by name with its value; the one selected is described below the list, with the file it comes from (the default, or the file that set it, drop-ins included). `enter` edits a value; `p` shows the change to the file that sets it as a diff, `w` writes it, `u` restores the previous version, `d` discards unsaved edits.
- **Notifications**: a box at the top, "Getting notified", shows the three steps and which are done: where to send (a channel), what to send (alerts on), and whether anything sends, which is the `fstui watch` service, a timer or cron job running `fstui check`, or nothing. When nothing does, `s` opens the Schedules tab on `fstui watch` with its change ready (write mode, as root). It also says whether a heartbeat URL is set, which lets a service outside the host warn you when the host goes down. Below are the channels, with their destination, least severity and whether their secret is set (never the secret). `a` adds a channel and `enter` edits the selected one, in a form with the settings its type needs; the secret is typed hidden, or pasted. `d` twice removes a channel. `p` reviews the change to `config.yaml` as a diff, with the secrets that change named but not shown; `w` writes `config.yaml` and `secrets.yaml` (mode 0600), keeping the previous versions. `t` sends a test message to the selected channel ([configuration.md](notifications.md)).
- **Alerts**: what is sent, as a checklist in groups (Pool health, Space, Maintenance, Performance, System, Your own): each kind of alert in plain words, ticked when on, with its threshold and where it goes ("vault → phone"). Below the list, the selected alert's message is filled in for an example pool. `space` turns one on (for every pool and channel) or off; `enter` opens its form: the pools and channels to tick (none ticked means all), the threshold, severity, how often it is sent again, whether it says when it clears, and the message. `a` adds your own alert: what it measures, when it is sent, then the same settings. `d` twice removes your own. Until your file lists alerts, the defaults are shown ticked ([configuration.md](notifications.md#alerts)).
- **Schedules**: the scrub and trim of each pool, `fstui check` and `fstui watch`: when they run, when next, and what runs them (a systemd timer, cron, an OpenRC service). Schedules fstui did not write, such as Debian's monthly scrub, are listed too so nothing runs twice. In write mode, `a` changes the selected one: weekly, monthly or off for a scrub or trim, on or off for check and watch. Each change shows the files it writes in full and the commands it runs, and asks y then enter ([configuration.md](configuration.md#schedules)).

## Without root

Everything is shown to other users except pool history and kernel events, which `zpool history` and `zpool events` refuse them. Changes need root, or permissions delegated with `zfs allow` for dataset and snapshot changes ([changes.md](changes.md#without-root)).
