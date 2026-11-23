# Changelog

All notable changes to fstui are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Below 1.0, a minor release may change keys, settings or flags; such changes are listed under "Changed".

## [Unreleased]

### Fixed

- Config view: adding a host, a second notification channel or an alert of your own crashed fstui as the saved file was shown (hosts in 0.9.0, channels since 0.8.0, your own alerts since 0.8.1). The tab now shows what it wrote at once.

## [0.9.0] - 2026-11-22

fstui looks at other machines: their pools over SSH, read-only, with your own keys and SSH configuration, chosen with `--host` or `H`, or kept in the Config view's new Hosts tab. On a Proxmox VE node it finds the cluster's other nodes, and `fstui watch` can check every host for one set of notifications. Changes on other hosts come in 0.9.1. Tested with OpenZFS 2.3.9 on Debian 13, over SSH to the same machine, and against the files of a Proxmox VE 9.2 cluster (OpenZFS 2.4.4).

### Added

- Other hosts over SSH, read-only: `hosts:` in `config.yaml` names them, `fstui --host NAME` opens one, and `H` lists them and switches. fstui uses the OpenSSH client with your keys, agent and `~/.ssh/config`, never asks for a password or a new host key, and checks each host's shell and its `zfs`, `zpool` and `env` before reading it. See `docs/remote.md`.
- Config view, Hosts tab: add, change and remove hosts, test a connection (`t`), and tick which machines `fstui watch` sends notifications about (`space`); each change is reviewed as a diff, as for channels.
- Proxmox VE: on a node, as root, `H` lists the cluster's other nodes and reaches them as the cluster does; a listed host that is a node adds its cluster's nodes.
- `notifications.hosts`: `fstui check` and `fstui watch` look at other hosts too. Each host's messages name it, what was sent is kept per host, a host that cannot be reached is reported once, and the heartbeat is requested once per check.
- Dashboard: a `hosts` panel (add it to `dashboard.panels`) with each host's pools and health, read every `refresh.hosts`.
- Settings `remote.connect_timeout`, `remote.alive_interval` and `remote.persist` (Remote hosts group), and `refresh.hosts`.
- `ui.text_width` (Display group): the widest a line of text runs in the Config view before it wraps, 110 columns by default.
- A build tag `fstui_noremote` (Gentoo USE flag `remote`) leaves out other hosts.

### Changed

- Config view, Alerts tab: the built-in alerts have short titles ("A device is offline", "Reads are slow"); the box under the list explains each. On a short terminal the box shortens to the explanation, so the list keeps its rows.
- Config view, General tab: the opening line and each group's description fit 80 columns instead of being cut off.
- The demo has a Proxmox VE cluster of hosts: `H` in the demo lists its nodes, one of which cannot be reached. The demo container's command now passes `COLORTERM`, so a terminal with full colour shows the theme's own colours.

### Fixed

- The `fstui check` and `fstui watch` units written by the Schedules tab now start after the network is up (`network-online.target`), so a check at boot no longer reports hosts or channels as unreachable before the network is there.
- When fstui could not start because of its configuration, the reason could be a file it had ignored (for example one others can write), which it did not say. It now lists such files with the error.

### Upgrading

- To check other hosts from the `fstui-watch` or `fstui-check` service that 0.8 set up on systemd, turn it off and on again in the Schedules tab: the new unit gives ssh a directory for its connections (`RuntimeDirectory=fstui`), which the 0.8 unit's sandbox does not allow. Without other hosts, nothing needs to change.

## [0.8.6] - 2026-11-20

The 0.8 series brings notifications and schedules to fstui, and this is its first public release. In short:

- fstui tells you when a pool needs attention: a pool that is not ONLINE, a device that dropped out, data errors, a pool filling up, slow disks, an overdue scrub, and more. Each alert is turned on per pool and per channel, with its own threshold and message, and you can add your own.
- Messages go to ntfy, a webhook, Gotify, Pushover, Telegram, Discord, Slack, Matrix or email, from `fstui watch`, a service fstui sets up for you on systemd or OpenRC, or from `fstui check` on a timer or cron job. An optional heartbeat URL lets a service outside the host warn you when the host itself goes down.
- View 9 is now Config: grouped settings, channels, alerts and schedules, each change shown as a diff before it is written. Scrub and trim schedules per pool are set there too.
- Every former fixed value is a setting, and settings can be split into drop-in files.

The sections below, from 0.8.0, list every change. Tested with OpenZFS 2.3.9 on Debian 13; schedules also under OpenRC (Gentoo) and cron (Debian) in containers.

### Changed

- Keys are a soft orchid in every shipped theme, a colour nothing else on the screen uses, quiet enough not to pull the eye from the content. A theme file sets it with `key`.
- Dashboard: the I/O panel leaves out the peak columns when it is too narrow for them, so pool names are no longer cut to three letters. The health panel's title says what is new ("2 new warnings").
- Events view: records from ZFS's own pool history read "History: scan done errors=0", so they are not taken for fstui's wording.
- Plans view: with no saved plans, the key bar offers only `r` (refresh).

### Fixed

- Text that was coloured and wrapped lost its colour after the first line, as in the Pools view's "No scrub is scheduled" line. Every wrapped line now keeps it.
- In the I/O view, the read chart's label showed the higher of the two peaks. Each chart now shows its own peak, and the title gives the scale both are drawn to.

## [0.8.5] - 2026-11-20

Keys get a colour of their own.

### Changed

- Keys, in the key bar and wherever text names one, have their own colour token, `key`, instead of the accent, which also marks the selected tab, the cursor and headings. A theme file can set `key` like any other token. 0.8.6 changes its value.

### Fixed

- In a muted sentence, the words after a key showed in the terminal's own colour. They now keep the sentence's colour.

## [0.8.4] - 2026-11-20

Wording and boxes in the Config view.

### Changed

- Config view: keys named in sentences ("`space` turns an alert on or off") are drawn like the key bar's, so they stand out from the words around them.
- Config view, Alerts tab: the box under the list says what each built-in alert means for the pool and why it is worth hearing of, instead of what it measures.

### Fixed

- A box holding a wrapped paragraph cut off its last lines and lost the indent of the wrapped ones.

## [0.8.3] - 2026-11-20

Notifications that say how they are delivered, alerts as a checklist, and room in the Config view.

### Added

- Config view, Notifications tab: "Getting notified" shows where messages go, which alerts are on, and whether anything sends them: the `fstui watch` service, a timer or cron job running `fstui check`, or nothing. When nothing does, `s` opens the Schedules tab on `fstui watch` with its change ready.
- `notifications.heartbeat_url`: requested after every check that read the pools, so Healthchecks or an Uptime Kuma push monitor warns you when the checks stop, as when the host is down.

### Changed

- Config view: the Alerts and Templates tabs are one Alerts tab, a checklist of every kind of alert in groups, in plain words, with its threshold and where it goes. `space` turns one on or off; `enter` opens a form where pools and channels are ticked from lists and "how often" is a choice (once, every hour, every day, every week). Your own alerts, formerly in the Templates tab, are added with `a` in the same form. Configuration files need no change.
- Config view: margins inside the frame, blank lines between sections, the selected setting described in a box of its own, and text no wider than 110 columns. Forms put their fields under headings and scroll when taller than the screen.
- The notification settings say what they do in plain words ("Repeat an open problem every", "Kernel events sent by").

## [0.8.2] - 2026-11-20

### Fixed

- Pasting into a field did nothing, so a password from a password manager could not be entered. A paste is now typed into the field that has focus (a channel's password or token, a passphrase, a name, a filter), without line breaks, so a trailing newline does not finish the form. A paste where no field takes text is ignored. The name typed to confirm a high-risk change still has to be typed.

## [0.8.1] - 2026-11-19

Alerts, and a Config view that reads well.

### Added

- Alerts: what `fstui check` and `fstui watch` send. fstui ships alerts for the usual cases, among them a device down, device errors, low free space, a resilver and slow reads or writes, each turned on for chosen pools and channels, with its own threshold, severity, messages and how often it repeats. `notifications.templates` adds your own and `notifications.alerts` lists the ones turned on; messages take placeholders such as `{pool}` and `{value}`. Without `notifications.alerts`, what 0.8.0 sent is still sent.
- Config view, Alerts and Templates tabs, to set them with a form, a diff and a backup, as for channels. 0.8.3 makes them one tab.

### Changed

- Config view, General tab: the settings are in groups; `enter` opens one. Each setting has a plain name, and the selected one is described below the list.
- Config view, Notifications tab: the channel form works like every other form: typing goes to the selected field, `enter` moves to the next and finishes on the last, a choice changes with `space`, the arrows or its first letter.
- Pools view, Tune tab: "evidence" is now "details", and an answer still missing says what it waits for.
- Plans view, audit log tab: a table with a box per column; RESULT reads success, failed or n/a (the record written before a step runs), with the exit status in the record's own box below.

### Fixed

- Config view, General tab: the list did not scroll past the bottom of the screen.

### Upgrading

- What `fstui check` and `fstui watch` remember is carried over: problems open under 0.8.0 are not sent again, and none is reported as cleared.

## [0.8.0] - 2026-11-18

Notifications and schedules.

### Added

- `fstui check`: looks at every pool once and sends what changed (health, data errors, capacity, fragmentation, overdue scrubs, held checkpoints, kernel events) to ntfy, a webhook, Gotify, Pushover, Telegram, Discord, Slack, Matrix or SMTP. `--test NAME` sends a test message.
- `fstui watch`: the same as a service, every 5 minutes and within 15 seconds of a kernel event.
- Config view, Notifications tab: add, edit, remove and test channels. Passwords and tokens go in `secrets.yaml` (mode 0600) and are never shown.
- Config view, Schedules tab: scrub and trim per pool, `fstui check` and `fstui watch`, on systemd timers, cron or OpenRC. Each change is confirmed and audited.
- Drop-in configuration files in `config.d/`, read in name order after the `config.yaml` beside them.
- Settings for most former fixed values: refresh intervals, timeouts, chart spans, layout widths, notification timing, the Tune tab's limits. `docs/configuration.md` lists them all.

### Changed

- View 9, Settings, is now Config, with tabs.
- The demo does what fstui does on a real system: every pool change (create, import, export, add, attach, replace, detach, remove, rewind) is carried out on its recorded pools, the Tune tab measures replayed kernel statistics, schedules change, and `fstui check` and `fstui watch` run, printing their messages instead of sending them.

### Fixed

- `refresh.arc` had no effect: the ARC view read every second.
- A saved change to `dashboard.panels` or `dashboard.columns` waited for a restart.
- On a pool imported read-only (a look at its checkpoint), changes were offered and then failed with zpool's error; they are now unavailable, with the way back.
- A configuration file reached through a symbolic link was read even when the directory holding the link could be written by others.
- The Tune tab's transaction-group reader could crash on a malformed kernel statistic.

## [0.7.1] - 2026-11-16

Memory and large systems. Measured with 331 datasets and 66,200 snapshots on OpenZFS 2.3.9.

### Changed

- Peak memory across all views: 248 MiB → 59 MiB. Listings are parsed while `zfs` prints them; a snapshot row uses an eighth of the memory.
- History tab keeps the newest 10,000 commands and 10,000 internal events. ZFS logs an internal event for every snapshot, so an old pool's history runs to millions of lines.
- After a slow read, the next waits at least four times as long, so `zfs` is busy at most a fifth of the time.
- View bar at 80 columns: `dash`, `data`, `snaps`, `vols` instead of names cut to four letters.
- Dashboard on short terminals: the health panel gets up to two fifths of the height.
- Tune: shorter findings; module parameters are only raised, never lowered.

### Fixed

- Snapshots view never loaded with tens of thousands of snapshots: `zfs list` ran past the 30 s limit. Listings now get 10 minutes.
- History tab re-rendered every entry on each key press; a very long history could fail at the 256 MiB output limit.
- Switching pools with the Tune tab open could mix the first pool's measurements into the second's.
- Volumes: device path cut from the left (`…/tank/vol`), not the right; block size and allocation columns fit their values.
- Docs: kernel events need root, like pool history.

## [0.7.0] - 2026-11-16

### Added

- Tune tab in each pool's detail: measures the pool while shown and answers five questions, each with a verdict, the deciding figure and what would change it. No root needed.
  - Write amplification, with the cause named: sync writes through the ZIL, read-modify-write, raidz padding.
  - Record size against request size, per dataset.
  - Where memory would buy speed: ARC ghost hits, unused prefetch, metadata misses, L2ARC overhead.
  - What limits the pool: write throttle, slow transaction groups, memory, a slow device, ZFS queues, ZIL stalls.
  - How well data compresses; `c` reads file metadata to find what does not.
- `a` in the Tune tab offers the changes a finding points to: `recordsize`, `compression=lz4`, `zfs_prefetch_disable` or `zfs_arc_meta_balance` until reboot, ARC limits.

### Changed

- Pool detail has six tabs; `h` from Topology goes to Tune.

## [0.6.1] - 2026-11-16

First GitHub release with the 0.6.0 changes below.

### Changed

- The manual moved to `docs/`: install, views, keys, changes, configuration, troubleshooting. The README is an overview.

### Fixed

- `a` in a view without changes showed a seven-line message, wrong since 0.6.0; it now names the views that have changes.
- The empty Plans view named the wrong views; it now says how to save a change.

## [0.6.0] - 2026-11-16

### Added

- Create a pool (`a`, `N` in Pools): disks from `lsblk` by `/dev/disk/by-id` name, with the reason any cannot be used; stripe, mirror, raidz1–3; log, cache, special and spare devices; preview from `zpool create -n`. Needs root.
- Add devices to a pool (`A`), optionally after a checkpoint. Mismatched redundancy and a single special device are refused.
- Checkpoints: take (`C`), discard (`D`), look read-only (`L`), rewind (`w`). Rewinding discards everything written since the checkpoint and lists what it removes; refused for the pool holding `/`.
- Device changes in Topology: attach (`a`), replace (`r`), detach (`d`), remove with its vdev (`R`); cancel a removal on the pool (`X`). A smaller device, or detaching the last online side, is refused.
- RAIDZ expansion: attaching to a raidz member widens the vdev. Data already written keeps its old data-to-parity ratio until rewritten, and the vdev can never be made narrower again. Progress shows in Topology and on the dashboard.
- Import (`I`): read-only, without mounting, or under a new name. A pool last used by another system is not imported, since forcing it can destroy it. Export (`x`): refused for the pool holding `/`.
- Upgrade (`u`): lists the features it enables and those the pool's `compatibility` setting keeps off.
- Send size (`e` in Snapshots): how large a `zfs send` of the marked snapshots would be, from `zfs send -n -P`, as compressed, raw or intermediate streams. `t` sets a capacity, such as a tape, and shows how full it would get. Nothing is sent.
- Pool-wide changes that remove data or cannot be undone (rewind, export, upgrade, removing a data vdev, RAIDZ expansion) ask for the pool name.

### Changed

- Messages name their subject: "tank has no scrub running", not "it has no scrub running".
- 100,000 snapshots: 288 bytes each instead of 442; a redraw takes 7.5 ms instead of 76 ms.

### Fixed

- A `raidz` or `draid` vdev without a parity digit, or a scan without a function name, crashed fstui.
- A change needing `tee`, `cp`, `chmod` or `mv` that fstui did not find stopped halfway; it is now refused before anything runs.

## [0.5.6] - 2026-11-15

Security and bug fixes.

### Security

- `/etc/modprobe.d/zfs.conf`, written when ARC limits are kept after reboot, is always mode 0644. It took fstui's umask before, and could be group-writable.
- fstui keeps its umask at least 022 and marks itself not dumpable: no core dump holds a passphrase, and other processes of the same user cannot read its memory.
- A passphrase reaches only the command that reads it, not the `zfs mount` steps after `zfs load-key`.
- The demo's temporary directory and the saved plans directory are refused unless they belong to the user and nobody else can open them.
- Settings saves exactly the file shown for review.
- Command output is read up to 256 MiB; a larger listing fails with a message.
- Event filter values are sanitised before they are drawn.
- Integration tests refuse a pool directory under `/var/tmp` that another user could control.
- Built with Go 1.27.4.

### Fixed

- If fstui cannot find your config file, Settings says so and edits nothing, instead of editing `/etc/fstui/config.yaml`.
- On a terminal smaller than 80x24, the size message fits the screen.

## [0.5.5] - 2026-11-15

First public release. Earlier versions were not published.

### Added

- Views of pools, datasets, snapshots, volumes, the ARC, I/O and events, and a dashboard.
- Changes to snapshots, filesystems, volumes, encryption keys, scrubs, trim, devices and the ARC's limits, in write mode, each shown with its commands and confirmed first.
- Saved plans, an audit log, dry-run mode, and changes without root through `zfs allow` delegation.
- Themes, key rebinding, a Settings view, mouse support and a plain-character mode.
- A demo container that runs on recorded output without ZFS.
- `fstui --version` reports the module version for binaries built with `go install`.

[Unreleased]: https://github.com/nuclear-gumbo/fstui/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/nuclear-gumbo/fstui/compare/v0.7.1...v0.8.0
[0.7.1]: https://github.com/nuclear-gumbo/fstui/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/nuclear-gumbo/fstui/compare/v0.6.1...v0.7.0
[0.6.1]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.6...v0.6.1
[0.6.0]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.6...v0.6.0
[0.5.6]: https://github.com/nuclear-gumbo/fstui/compare/v0.5.5...v0.5.6
[0.5.5]: https://github.com/nuclear-gumbo/fstui/releases/tag/v0.5.5
