# Configuration

Every setting is optional, and almost everything fstui does that has a number or a choice in it is a setting: thresholds, refresh intervals, chart spans, timeouts, layout, notification timing and the limits behind the Tune tab's verdicts. The full list is in the [reference](#reference) below.

## Files

fstui reads these files in order. Each overrides the ones before it, key by key:

1. `/etc/fstui/config.yaml`, for site defaults.
2. `/etc/fstui/config.d/*.yaml`, in name order.
3. `$XDG_CONFIG_HOME/fstui/config.yaml`, by default `~/.config/fstui/config.yaml`. For root the home directory comes from the password database, so under `sudo` root's file applies, not the invoking user's.
4. `$XDG_CONFIG_HOME/fstui/config.d/*.yaml`, in name order.
5. Command-line flags (`--superuser`, `--dashboard`).

Drop-ins in `config.d` take priority over the `config.yaml` beside them. Use them to split settings by topic, for example:

```
~/.config/fstui/
├── config.yaml
└── config.d/
    ├── 10-ui.yaml             # theme, mouse, ui:, dashboard:
    ├── 20-notifications.yaml  # notifications:
    └── 30-tune.yaml           # tune:
```

Every file takes the same keys as `config.yaml`. A drop-in can be any name ending in `.yaml`; names starting with `.` are skipped. A numeric prefix makes the order explicit.

How values combine across files:

- A setting from the reference is replaced by each later file that sets it.
- `keys:` and `colors:` combine by action and by colour: a later file rebinds only the actions it names.
- Lists replace: a later file's `events.filters`, `dashboard.panels` or `notifications.channels` replaces the whole list from earlier files.

A file is read only if it and every directory above it are owned by you or root and writable by no one else. fstui usually runs as root, and another local user must not be able to steer that session, for example by rebinding the confirm key. An ignored file is named in the UI with the reason.

A mistake in a file is shown with file and line; the setting keeps its previous value and the rest of the file still applies.

`docs/config.schema.json` is a JSON schema of the file, made from the same list of settings. Editors that read YAML schemas (VS Code with Red Hat's YAML extension, Neovim and Helix with yaml-language-server) complete keys and mark mistakes as you type when the file starts with:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/nuclear-gumbo/fstui/main/docs/config.schema.json
```

## Config view

View 9 edits your own files, in four tabs: General (the settings below), Notifications (where messages go, and what sends them), Alerts (what is sent, [below](#alerts-and-templates)) and Schedules. In General, `enter` opens a group and `enter` or `space` changes the selected setting. A change is written to the file that sets it now: one of your drop-ins if one does, else `config.yaml`. The box under the list says which file the selected value comes from. Changes stay pending until you review them with `p`, which writes the new file to a private temporary file, checks it, and shows the diff. `w` writes it, `n` or `esc` keeps the old file, and `d` discards the pending changes. The previous file is kept with `.bak` added, and `u` reverts to it.

Saved settings apply at once, except the few marked "applies after a restart". `keys:` and `colors:` are edited in the file.

## Settings

A few settings, for a feel of the file:

```yaml
theme: default
mouse: true

thresholds:
  capacity_warn: 80       # percent full shown as WARN
  capacity_error: 90      # percent full shown as FULL, above capacity_warn

refresh:                  # seconds between reads
  pools: 5
  datasets: 15

ui:
  start_view: dashboard

events:
  filters:                # saved filters, cycled with f in the Events view
    - name: errors only
      severity: [error, warn]
    - name: tank
      pool: tank

dashboard:
  panels: [health, events, io, arc]   # any of health, events, io, arc, scans, in order

keys:                     # rebind actions: one key or a list
  select: [enter, space]
  left: [h, left]

colors:                   # override single colours of the theme
  accent: "#7C9CB4"
  warn: { dark: "#D2B26E", light: "#86651F" }
```

An event filter has a `name` and any of `severity` (one of, or a list of, `info`, `notice`, `warn`, `error`), `pool`, `class` and `text`.

Some settings are checked against each other: `capacity_error` above `capacity_warn`, `charts.io_keep` at least `charts.io_window`, `dashboard.three_columns_from` above `two_columns_from`, and `tune.amplification_mismatch` above `amplification_fits`.

### Reference

Durations are written like `30s`, `5m`, `24h`. Keys, colours, event filters, dashboard panels and notification channels are lists or maps, described in their own sections.

<!-- settings reference: made from internal/config/settings.go by make docs -->

#### General

| Setting | Default | Accepts | Meaning |
|---|---|---|---|
| `theme` | `default` | a theme name | Colour theme |
| `units` | `iec` | `iec`, `exact` | Sizes in IEC units or exact byte counts |
| `ascii` | `auto` | `auto`, `true`, `false` | Plain characters instead of box and block glyphs; auto on the Linux console |
| `mouse` | `true` | `true`, `false` | Clickable tabs and rows, wheel scrolling |
| `natural_scrolling` | `false` | `true`, `false` | The wheel moves the content, as on a touchpad set to natural scrolling |
| `superuser` | `false` | `true`, `false` | Hide advisory warnings; confirmations are always shown |

#### `thresholds:`

| Setting | Default | Accepts | Meaning |
|---|---|---|---|
| `thresholds.capacity_warn` | `80` | 1 to 100 | Capacity shown as WARN from this percentage |
| `thresholds.capacity_error` | `90` | 1 to 100 | Capacity shown as FULL from this percentage |
| `thresholds.fragmentation_warn` | `50` | 1 to 100 | Warn about free-space fragmentation from this percentage |
| `thresholds.scrub_overdue_days` | `35` | 1 to 3650 | Warn when the last scrub is older than this many days |
| `thresholds.checkpoint_age` | `24h` | 1h to 8760h | Warn about a checkpoint held longer than this |

#### `refresh:`

| Setting | Default | Accepts | Meaning |
|---|---|---|---|
| `refresh.pools` | `5` | 2 to 3600 seconds | Seconds between pool reads |
| `refresh.datasets` | `15` | 5 to 3600 seconds | Seconds between dataset reads |
| `refresh.arc` | `1` | 1 to 3600 seconds | Seconds between ARC reads while the ARC view is shown |
| `refresh.arc_hidden` | `10` | 1 to 3600 seconds | Seconds between ARC reads while its view is not shown (the dashboard's graph) |
| `refresh.dataset_detail` | `30` | 5 to 3600 seconds | Seconds between reads of a dataset's properties, snapshots and permissions |
| `refresh.properties` | `60` | 5 to 3600 seconds | Seconds between reads of a pool's properties and features |
| `refresh.schedules` | `60` | 5 to 3600 seconds | Seconds between reads of scrub timers and cron jobs |
| `refresh.events` | `2` | 1 to 3600 seconds | Seconds between reads of kernel events while the Events view is shown |
| `refresh.events_hidden` | `30` | 1 to 3600 seconds | Seconds between reads of kernel events while the Events view is not shown |
| `refresh.tune` | `5` | 1 to 3600 seconds | Seconds between the Tune tab's measurements; applies the next time the tab opens |

#### `charts:`

| Setting | Default | Accepts | Meaning |
|---|---|---|---|
| `charts.arc_window` | `5m` | 1m to 1h | Time the ARC graphs show |
| `charts.io_window` | `2m` | 30s to 1h | Time the I/O rates show |
| `charts.io_keep` | `10m` | 1m to 6h | I/O history kept per pool and device; at least io_window |

#### `limits:`

| Setting | Default | Accepts | Meaning |
|---|---|---|---|
| `limits.read_timeout` | `30s` | 5s to 10m | Longest a zfs or zpool read may take; applies after a restart |
| `limits.list_timeout` | `10m` | 30s to 2h | Longest a listing of datasets or snapshots may take |
| `limits.slow_read_factor` | `4` | 1 to 20 | After a read of d seconds, wait at least this many times d before the next |
| `limits.history_entries` | `10000` | 100 to 1000000 | Pool history entries kept, of user commands and of internal events each; applies after a restart |
| `limits.diff_entries` | `20000` | 100 to 1000000 | Changes shown from zfs diff; applies after a restart |
| `limits.history_timeout` | `10m` | 30s to 2h | Longest zpool history may take; applies after a restart |
| `limits.audit_entries` | `2000` | 100 to 100000 | Audit records the Audit tab reads, newest first |

#### `ui:`

| Setting | Default | Accepts | Meaning |
|---|---|---|---|
| `ui.start_view` | `pools` | `dashboard`, `pools`, `datasets`, `snapshots`, `volumes`, `arc`, `io`, `events`, `plans`, `config` | The view fstui opens on; --dashboard overrides; applies after a restart |
| `ui.cursor_blink` | `true` | `true`, `false` | A typing cursor blinks |
| `ui.split_from` | `120` | 80 to 1000 | Terminal width from which a list and the selected item's detail sit side by side |
| `ui.text_width` | `110` | 60 to 1000 | The widest a line of text runs in the Config view before it wraps, so a wide terminal does not spread a sentence across it |

#### `events:`

| Setting | Default | Accepts | Meaning |
|---|---|---|---|
| `events.retention` | `5000` | 100 to 100000 | Events kept in memory |

#### `dashboard:`

| Setting | Default | Accepts | Meaning |
|---|---|---|---|
| `dashboard.columns` | `0` | 0 to 3 | Dashboard columns; 0 chooses by the terminal's width |
| `dashboard.two_columns_from` | `120` | 80 to 1000 | With columns 0, two columns from this terminal width |
| `dashboard.three_columns_from` | `200` | 80 to 2000 | With columns 0, three columns from this terminal width |

#### `notifications:`

| Setting | Default | Accepts | Meaning |
|---|---|---|---|
| `notifications.repeat` | `24h` | 0 to 720h | While a problem lasts, send it again after this long; 0 sends it once |
| `notifications.resolved` | `true` | `true`, `false` | Send a message when a problem goes away |
| `notifications.events` | `auto` | `auto`, `on`, `off` | Who sends ZFS kernel events, such as checksum errors: on, fstui; off, nobody; auto, ZED when it is set up to send them, else fstui |
| `notifications.events_min` | `error` | `info`, `notice`, `warn`, `error` | The least serious kernel event sent: info, notice, warn or error |
| `notifications.host` | empty | up to 64 letters, digits, ., - and _ | What messages call this system; empty uses its host name |
| `notifications.heartbeat_url` | empty | an https:// URL | Requested after every check, so a service outside this host (Healthchecks, an Uptime Kuma push monitor) warns you when the checks stop, as when the host is down; empty sends none |
| `notifications.attempt_timeout` | `10s` | 1s to 5m | A try at sending that takes longer is given up |
| `notifications.send_timeout` | `30s` | 5s to 30m | All the tries of one message together take no longer than this |
| `notifications.retries` | `3` | 0 to 10 | How many more times a message is tried after a network or server error |
| `notifications.check_timeout` | `5m` | 30s to 1h | A check of the pools that takes longer is stopped and reported |
| `notifications.watch_interval` | `5m` | 30s to 24h | How often the notification service (fstui watch) checks every pool |
| `notifications.watch_events` | `15s` | 0 to 1h | How often the service looks for new kernel events between checks, so they go out at once; 0 only at checks |

#### `schedules:`

| Setting | Default | Accepts | Meaning |
|---|---|---|---|
| `schedules.check_every` | `15m` | `5m`, `10m`, `15m`, `30m`, `1h`, `2h`, `6h`, `12h`, `24h` | How often a schedule written by fstui runs fstui check |
| `schedules.cron_time` | `03:00` | HH:MM, 24-hour | When cron starts scrubs and trims fstui schedules |
| `schedules.scrub_weekday` | `sun` | `sun`, `mon`, `tue`, `wed`, `thu`, `fri`, `sat` | Day of a weekly scrub under cron |
| `schedules.scrub_monthday` | `1` | 1 to 28 | Day of the month of a monthly scrub under cron |
| `schedules.trim_weekday` | `wed` | `sun`, `mon`, `tue`, `wed`, `thu`, `fri`, `sat` | Day of a weekly trim under cron |
| `schedules.trim_monthday` | `15` | 1 to 28 | Day of the month of a monthly trim under cron |

#### `tune:`

| Setting | Default | Accepts | Meaning |
|---|---|---|---|
| `tune.min_window` | `1m` | 10s to 1h | I/O measured before the amplification and saturation questions answer |
| `tune.min_written_mib` | `16` | 1 to 1048576 | MiB written by applications before amplification is judged |
| `tune.min_requests` | `50` | 1 to 1073741824 | Requests to a dataset before its record size is judged |
| `tune.min_moved_mib` | `1` | 1 to 1048576 | MiB read or written by a dataset before its record size is judged |
| `tune.min_lookups` | `10000` | 100 to 1073741824 | ARC lookups before the memory question answers |
| `tune.min_operations` | `1000` | 10 to 1073741824 | Device operations before saturation is judged |
| `tune.min_prefetch` | `1000` | 1 to 1073741824 | Prefetched blocks before prefetch is judged |
| `tune.compression_gib` | `1` | 0 to 1048576 | GiB a dataset holds before its compression is judged |
| `tune.walk_files` | `200000` | 1000 to 100000000 | Files the per-file compression walk reads at most |
| `tune.walk_time` | `1m` | 5s to 1h | Longest the per-file compression walk runs |
| `tune.slow_txg` | `5s` | 1s to 10m | A transaction group syncing longer than this is slow |
| `tune.small_ratio` | `8` | 2 to 1024 | Records this many times the average request are too large for it |
| `tune.large_ratio` | `4` | 2 to 1024 | Requests this many records long are too large for the records |
| `tune.l2_headers_mib` | `64` | 1 to 1048576 | MiB of L2ARC headers worth weighing against its hits |
| `tune.amplification_fits` | `1.5` | 1 to 100 | Writes within this many times what the layout explains fit |
| `tune.amplification_mismatch` | `3` | 1 to 1000 | Writes past this many times what the layout explains are a mismatch |
| `tune.ghost_share` | `0.25` | 0 to 1 | Share of ARC misses just evicted that points at a larger ARC |
| `tune.prefetch_use` | `0.25` | 0 to 1 | Share of prefetched blocks read below which prefetch is wasted |
| `tune.metadata_miss` | `0.1` | 0 to 1 | Share of metadata lookups missing that points at metadata crowded out |
| `tune.l2_share` | `0.1` | 0 to 1 | Share of L2ARC lookups hitting below which it costs more than it gives |
| `tune.slow_device` | `2` | 1 to 100 | A device this many times slower than its siblings is worth a look |
| `tune.queueing` | `4` | 1 to 100 | Total wait this many times the disk wait is queueing in ZFS |
| `tune.poor_ratio` | `1.05` | 1 to 10 | An expensive compression earning less than this ratio is worth a look |
| `tune.incompressible` | `1.1` | 1 to 10 | Files saving less than this ratio do not compress |

<!-- end of settings reference -->

## Notifications

Nothing is sent until something on the system runs the checks, and you never need to run one by hand. The usual way is `fstui watch` as a service (systemd, or OpenRC's supervise-daemon): it checks every pool every `notifications.watch_interval` (5 minutes) and sends a new kernel event within `notifications.watch_events` (15 seconds). Where a service is not wanted, a timer or cron job runs `fstui check`, which checks once and exits. The Schedules tab of view 9 writes either, and the Notifications tab says which one runs, or that nothing does; there, `s` sets up `fstui watch`. Both share what was sent, so switching from one to the other sends nothing twice. Run either as root to see kernel events and pool history. Neither changes anything on the pools. `fstui watch` reads the configuration again for every check, checks at once on SIGHUP, logs only checks that sent or failed, and refuses to start while another `fstui watch` of the same user runs.

A monitor on the host cannot tell you that the host itself went down. For that, set `notifications.heartbeat_url` to a Healthchecks check or an Uptime Kuma push monitor: every check that read the pools requests it, and the service warns you when the requests stop.

What is sent, and to whom, is set in two tabs of view 9: Notifications (the channels) and Alerts (what each alert sends, and where). This section is what they write.

```yaml
notifications:
  repeat: 24h          # send an open problem again after this; 0 never repeats
  resolved: true       # send a message when a problem clears
  events: auto         # kernel events: auto (left to ZED when ZED sends them), on, off
  events_min: error    # least severe kernel event sent: info, notice, warn, error
  channels:
    - name: phone      # lower-case letters, digits, - and _
      type: ntfy
      url: https://ntfy.example.org
      topic: zfs
      user: alice      # with the secret as password; without user, the secret is a token
      min_severity: warning   # info, warning, error
```

What each type needs:

| Type | Settings | Secret |
|---|---|---|
| `ntfy` | `url`, `topic`, `user` (optional) | password, or access token |
| `webhook` | `url` | optional bearer token |
| `gotify` | `url` | application token |
| `pushover` | `user_key` | application token |
| `telegram` | `chat_id` | bot token |
| `discord`, `slack` | none | the webhook URL |
| `matrix` | `url` (the homeserver), `room` (`!id:server`) | access token |
| `smtp` | `host`, `port`, `from`, `to` (one or a list), `user` (optional) | password |

Every channel also takes `ca_file` (a PEM certificate authority, for a server with a private certificate), `insecure_http: true` (allow an `http://` URL) and `min_severity`.

- URLs must be `https://` unless `insecure_http` is set. Certificates are always verified. The proxy environment variables are not used, and redirects are not followed.
- SMTP uses TLS from the start on port 465. On any other port the server must offer STARTTLS; fstui does not send mail, or a password, unencrypted.
- A problem is sent when it appears or its state changes, again after `repeat`, and once when it clears. Each channel gets one message per run listing what changed. A channel that fails is tried again on the next run.
- Kernel events are sent once each. The first run sends none of the events the kernel already holds.
- Timeouts, retries, the host name in messages and how long one run may take are `notifications.*` settings in the [reference](#reference).

### Alerts and templates

In the Alerts tab every kind of alert is a line to tick: "A pool is filling up", "A device is offline", "Reads are slow". In the file, each kind is a template: a measure of a pool, a comparison, a threshold, a severity and the message it sends. An entry under `alerts:` turns a template on for some pools and channels, or all of them, and can change its threshold, severity, messages, how often it repeats and whether it says when the condition clears. Your own alerts, added with `a` in the Alerts tab, are templates under `templates:`.

```yaml
notifications:
  templates:                       # your own; one named like a built-in template replaces it
    - name: nearly-full
      description: Less than a terabyte free
      metric: free
      op: "<"
      value: 1T
      error_value: 200G            # past this it is an error
      severity: warning
      text: "{pool} has {value} free"
      resolved_text: "{pool} has room again"
  alerts:
    - template: pool-unhealthy     # every pool, every channel
    - template: capacity
      pools: [tank, vault]
      channels: [phone]
      value: "70"
      error_value: "85"
    - template: nearly-full
      pools: backup
      repeat: 12h                  # this alert only; 0 sends once
      resolved: false
```

Without `alerts:`, fstui sends what it sent before alerts existed: `pool-unhealthy`, `data-errors`, `capacity`, `fragmentation`, `scrub-overdue` and `checkpoint-held` at the [thresholds](#thresholds), `version-mismatch`, and `kernel-events` at `events_min`. Once the file lists alerts, only those are sent. The Alerts tab shows the defaults ticked and writes them out with your first change.

The built-in templates:

| Template | Sent when | Severity |
|---|---|---|
| `pool-unhealthy` | the pool is not ONLINE | error |
| `device-down` | a device is offline, faulted, unavailable or removed | error |
| `device-errors` | devices count read, write or checksum errors | warning |
| `data-errors` | files have permanent data errors | error |
| `capacity` | the pool is 80% full; an error at 90% | warning |
| `low-free-space` | less than 100 GiB is free | warning |
| `fragmentation` | free space is 50% fragmented | warning |
| `scrub-overdue` | the last scrub finished more than 35 days ago | warning |
| `resilvering` | a resilver runs | info |
| `checkpoint-held` | a checkpoint is older than 24h | warning |
| `slow-reads`, `slow-writes` | reads or writes wait more than 50 ms on average | warning |
| `version-mismatch` | the userland and kernel module versions differ | warning |
| `kernel-events` | the kernel reports an event of `error` or worse | the event's |

The measures a template can compare, and how its `value` is written:

| `metric` | Value |
|---|---|
| `capacity`, `fragmentation` | a percentage: `80` |
| `free` | a size: `500G`, `2T` (powers of 1024) |
| `health` | a state: `ONLINE`, `DEGRADED`, compared with `==` or `!=` |
| `devices_down`, `device_errors`, `data_errors` | a count |
| `resilvering`, `versions_differ` | 1 or 0 |
| `scrub_age` | days |
| `checkpoint_age` | a duration: `24h` |
| `read_wait`, `write_wait` | milliseconds, averaged over one second of `zpool iostat`, read only when an alert uses them |
| `events` | `info`, `notice`, `warn` or `error`, with `>=` |

`op` is one of `>`, `>=`, `<`, `<=`, `==`, `!=`. A message can include `{pool}`, `{value}` (as measured, with its unit), `{threshold}`, `{metric}`, `{severity}` and `{detail}` (what fstui adds, such as `: 1 device OFFLINE`). A pool the backend cannot measure something of is never sent an alert about it.

### Secrets

Passwords and tokens go in `secrets.yaml` next to `config.yaml` (for root also `/etc/fstui/secrets.yaml`), by channel name:

```yaml
phone: the password or token
```

The file must belong to the user fstui runs as, with mode 0600; otherwise fstui ignores it and says why. A channel can instead name a file of its own with `secret_file`, under the same rule. Secrets never appear in `config.yaml`, the audit log, saved plans or messages.

`fstui check --test NAME` sends a test message to one channel.

## Schedules

The Schedules tab of view 9 shows and, in write mode, changes what runs on a schedule. Each change is a plan: the files it writes, in full, and the commands it runs, confirmed with y then enter and recorded in the audit log. It needs root.

| | systemd | cron, without systemd | OpenRC |
|---|---|---|---|
| Scrub, trim | OpenZFS's own timers, `zfs-scrub-weekly@POOL.timer` and the like: `systemctl enable --now`; no file is written | `/etc/cron.d/fstui` | through cron |
| `fstui check` | `fstui-check.timer` and `.service` in `/etc/systemd/system`, every `schedules.check_every` | `/etc/cron.d/fstui` | through cron |
| `fstui watch` | `fstui-watch.service` | not offered | `/etc/init.d/fstui-watch` under `supervise-daemon`, in the default runlevel |

- OpenZFS's timers catch up a run they missed, so turning a weekly scrub on can start a scrub at once. They run weekly (Monday 00:00) or monthly, with up to an hour's random delay; cron uses `schedules.cron_time` and the day settings instead.
- fstui writes `/etc/cron.d/fstui` whole and keeps only the entries under its `# fstui:` lines.
- `fstui check` on a schedule and `fstui watch` send the same messages, so only one of them can be on.
- The units, the cron line and the script name the fstui that wrote them, and run it as root, so fstui refuses unless that file and its directories are owned by root and writable by no one else: install fstui under `/usr`, not in a home directory. The units run it with `ProtectSystem=strict`, writing only `/var/lib/fstui`.
- Turning a schedule off stops and disables it, and leaves fstui's unit files and init script in place.

## Keys

`?` in fstui lists the keys in effect for the current view. Under `keys:`, each action takes one key or a list. A key bound to two actions that are active in the same view is reported. Actions:

| Action | Default | |
|---|---|---|
| `up`, `down` | `k` `up`, `j` `down` | move |
| `first`, `last` | `g` `home`, `G` `end` | first and last row |
| `left`, `right` | `h` `left`, `l` `right` | previous and next tab; collapse and expand |
| `select` | `enter` `space` | open |
| `back` | `esc` `q` | back; quit at the top |
| `quit` | `ctrl+c` | quit |
| `help` | `?` | help |
| `refresh` | `r` | read now |
| `bytes` | `b` | exact bytes or IEC units |
| `hide_warnings` | `x` | hide a warning for the session |
| `internal_events` | `i` | internal events in pool history |
| `sort`, `reverse_sort` | `s`, `S` | sort column, reverse the order |
| `save`, `write`, `revert`, `discard` | `p`, `w`, `u`, `d` | Config: review, write, revert, discard |
| `confirm`, `cancel` | `y`, `n` | confirmation windows |
| `actions` | `a` | changes for the selection |
| `write_mode` | `W` | write mode on or off |
| `new_snapshot` | `n` | new snapshot |
| `diff` | `=` | what changed since the snapshot |
| `send_size` | `e` | estimate the size of a `zfs send` |
| `measure_files` | `c` | Tune tab: measure compression per file |
| `test_channel` | `t` | Config, Notifications tab: send a test message |
| `mark`, `clear_marks` | `m`, `M` | mark items; clear the marks |
| `filter` | `/` | filter by text |
| `filter_pool`, `filter_severity`, `filter_class` | `P`, `v`, `c` | Events: next pool, minimum severity, class |
| `saved_filter`, `follow` | `f`, `F` | Events: next saved filter, follow the newest |

## Themes

Shipped themes: `default`, `catppuccin`, `gruvbox`, `nord`, `tokyo-night`. `fstui --list-themes` lists them and any theme files found.

`theme: <name>` is looked up in `$XDG_CONFIG_HOME/fstui/themes/<name>.yaml`, then `/etc/fstui/themes/<name>.yaml`, then among the shipped themes. A file with a shipped theme's name replaces it. Theme files pass the same ownership checks as `config.yaml`.

```yaml
name: mytheme
source: where the palette comes from, and its license
dark:
  background: "#1e1e2e"   # used only to check contrast; fstui never paints the background
  accent: "#89b4fa"
  ok: "#a6e3a1"
  warn: "#f9e2af"
  error: "#f38ba8"
  muted: "#9399b2"
  border: "#45475a"
  key: "#c9a6d0"          # keys, in the key bar and wherever text names one
  meter: ["#a6e3a1", "#f9e2af", "#f38ba8"]   # bar colours: start, at warn, from error
light:
  # the same keys; leave the block out to keep the default light colours
```

Tokens a theme leaves out keep the default theme's values. `key` should differ from `accent`: keys are named in text next to the selection and headings, which use the accent. With 256 colours fstui uses the nearest fixed palette entry; with 16 colours it uses the terminal's own colours. fstui warns when two of `ok`, `warn` and `error` look alike, or when a text colour has less than 2.5:1 contrast against the theme's `background`.

`NO_COLOR` turns colour off. `ascii: true`, or `auto` on the Linux console, draws with plain characters only.
