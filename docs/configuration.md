# Configuration

Every setting is optional. fstui reads, in order, each file overriding the previous one key by key:

1. `/etc/fstui/config.yaml`, for site defaults.
2. `$XDG_CONFIG_HOME/fstui/config.yaml`, by default `~/.config/fstui/config.yaml`. For root the home directory comes from the password database, so under `sudo` root's file applies, not the invoking user's.
3. Command-line flags (`--superuser`).

A file is read only if it and every directory above it are owned by you or root and writable by no one else. fstui usually runs as root, and another local user must not be able to steer that session, for example by rebinding the confirm key. An ignored file is named in the UI with the reason.

A mistake in a file is shown with file and line; the setting keeps its previous value and the rest of the file still applies.

## Settings view

View 9 edits your own `config.yaml`. `enter` or `space` changes the selected setting. Changes stay pending until you review them with `p`, which writes the new file to a private temporary file, checks it, and shows the diff. `w` writes it, `n` or `esc` keeps the old file, and `d` discards the pending changes. The previous file is kept as `config.yaml.bak`, and `u` reverts to it. Saved settings apply at once. `keys:` and `colors:` are edited in the file.

## Settings

```yaml
theme: default            # see Themes below
units: iec                # iec, or exact for byte counts
ascii: auto               # auto (plain characters on the Linux console), true, false
mouse: true               # clickable tabs and rows, wheel scrolling; shift+drag selects text
natural_scrolling: false  # true reverses the wheel, as on a touchpad set to natural scrolling
superuser: false          # true hides advisory warnings; confirmations are always shown

thresholds:
  capacity_warn: 80       # percent full shown as WARN, 1 to 100
  capacity_error: 90      # percent full shown as FULL, above capacity_warn
  fragmentation_warn: 50  # percent
  scrub_overdue_days: 35  # warn when the last scrub is older

refresh:                  # seconds between reads, up to 3600
  pools: 5                # at least 2
  datasets: 15            # at least 5
  arc: 1                  # at least 1, while the ARC view is shown

events:
  retention: 5000         # events kept in memory, 100 to 100000
  filters:                # saved filters, cycled with f in the Events view
    - name: errors only
      severity: [error, warn]
    - name: tank
      pool: tank

dashboard:
  panels: [health, events, io, arc]   # any of health, events, io, arc, scans, in order
  columns: 0              # 1 to 3, or 0 to choose by the terminal's width

keys:                     # rebind actions: one key or a list
  select: [enter, space]
  left: [h, left]

colors:                   # override single colours of the theme
  accent: "#7C9CB4"
  warn: { dark: "#D2B26E", light: "#86651F" }
```

An event filter has a `name` and any of `severity` (one of, or a list of, `info`, `notice`, `warn`, `error`), `pool`, `class` and `text`. A file that lists filters replaces the filters of the files before it.

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
| `save`, `write`, `revert`, `discard` | `p`, `w`, `u`, `d` | Settings: review, write, revert, discard |
| `confirm`, `cancel` | `y`, `n` | confirmation windows |
| `actions` | `a` | changes for the selection |
| `write_mode` | `W` | write mode on or off |
| `new_snapshot` | `n` | new snapshot |
| `diff` | `=` | what changed since the snapshot |
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
  meter: ["#a6e3a1", "#f9e2af", "#f38ba8"]   # bar colours: start, at warn, from error
light:
  # the same keys; leave the block out to keep the default light colours
```

Tokens a theme leaves out keep the default theme's values. With 256 colours fstui uses the nearest fixed palette entry; with 16 colours it uses the terminal's own colours. fstui warns when two of `ok`, `warn` and `error` look alike, or when a text colour has less than 2.5:1 contrast against the theme's `background`.

`NO_COLOR` turns colour off. `ascii: true`, or `auto` on the Linux console, draws with plain characters only.
