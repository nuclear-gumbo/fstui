# Codebase

How fstui is put together, for contributors. Each package's doc comment says what it is for; this page says how they fit, follows one change through the code, and defines the terms the code uses.

## Layout

```
cmd/fstui/            program start: flags, fstui check and fstui watch, wiring of the modules
internal/
  execute/            the only package that starts processes: tool paths, argv, timeouts, mutations
  termtext/           text from outside fstui, made safe to draw
  config/             YAML settings: defaults, files, drop-ins, the settings registry, themes, secrets
  storage/            backend-neutral types the generic views use
  problem/            what any backend measures and reports as wrong, for the dashboard and fstui check
  events/             backend-neutral kernel events
  zfs/                the OpenZFS backend: readers and parsers of zfs and zpool output
    name/             validated pool, dataset and snapshot names
    health/           ZFS pool warnings, and the measures alerts compare
    plan/             ZFS changes as plans
  mutate/             plans, confirmation tiers, the re-check before running, saved plans
  audit/              the audit log of every change
  check/              fstui check and fstui watch: alerts, what was sent, sending
  notify/             notification senders; the only package that opens network connections
  schedule/           scrubs, trims, check and watch under systemd, cron or OpenRC
  tune/               tune mode's questions, from measured windows
  disks/              block devices offered for a new pool
  statedir/           where fstui keeps state between runs, and its instance lock
  format/             sizes and durations as fstui writes them
  theme/              colour tokens
  registry/           which modules a build contains
  ui/                 the root model, shared components, the Config view
    keys/             the keymap
    zfsview/          views of ZFS concepts
    actions/          the write module's interface: menus, forms, confirmation, Plans and Audit
  demo/               the demo backend: recorded zfs and zpool output
  testpool/           test pools on sparse files, for integration tests only
testdata/zfs/2.3.9/   real command output the parsers and the demo are tested against
```

Two rules hold the structure together, and the linters enforce both: only `internal/execute` imports `os/exec`, and only `internal/notify` imports `net/http`, `net/smtp` and `crypto/tls`.

## Build tags

Every backend and optional module can be left out (`make check` builds each combination):

| Tag | Leaves out | Files |
|---|---|---|
| `fstui_nowrite` | everything that changes storage or system files | `*_write.go`, `module_write.go`; the stand-ins are `*_nowrite.go` |
| `fstui_nozfs` | the ZFS backend | `module_zfs*.go`; `module_nozfs.go` |
| `fstui_demo` | the real backend, for the demo backend | `module_demo*.go` |

Test-only tags: `fstui_integration` (pool tests, run as root by `scripts/integration.sh`), `fstui_container` (schedule tests for `scripts/test-schedules-container.sh`, never outside a container), `fstui_profile` (`bin/fstui-profile`).

`cmd/fstui/main.go` asks each module for what it adds: views, the changes it offers, a source for fstui check, tasks for the Schedules tab. A build without a module gets its stand-in, so nothing else needs a build tag.

## A read

1. A view's poller (`zfsview/poll.go`) asks a reader on a timer from the settings, through a `tea.Cmd`.
2. The reader (`zfs`) runs the command with `execute.Runner.Read`: an absolute, root-owned tool path, a fixed environment, a timeout.
3. A parser turns the output into typed values; every string from outside is a `termtext.Text`.
4. The result comes back to the view as a message. On an error the view keeps its last data and the status bar says what failed.

## A change

1. In write mode, `a` asks the write module for the changes offered on the selection (`actions.ZFSOffers`, `actions.ScheduleOffers`).
2. The chosen change's builder (`zfs/plan`, `schedule`) makes a `mutate.Plan`: the exact commands, what they affect, the native dry run where there is one, the state it depends on, and anything that blocks it.
3. The confirmation shows the plan; how much it asks (a key, y then enter, or typing the name) comes from the one table in `mutate/tiers.go`.
4. `mutate.Pipeline.Run` reads the state again and stops if anything changed, then `execute.Mutator` runs the steps, each written to the audit log before and after. Mutations have no timeout and are never retried.
5. Screens get `ui.ChangedMsg` and read again.

A plan can be saved and applied later: what is saved is the operation and its parameters, and the plan is built again, never replayed.

## One change, file by file

Taking a snapshot of `tank/home` from the Datasets view:

1. `main` and `run` in `cmd/fstui/main.go` parse the flags; `start` loads the configuration, asks each compiled-in module for its views (`module_zfs.go`) and its changes (`module_zfs_write.go`), and runs the Bubble Tea program with the root model from `ui.New` (`internal/ui/ui.go`).
2. The Datasets view (`internal/ui/zfsview/datasets.go`) starts its poller (`zfsview/poll.go`), which calls `zfs.Reader.Datasets` (`internal/zfs/datasets.go`); the reader runs `zfs list -j` through `execute.Runner.Read` (`internal/execute/run.go`) and parses the JSON. The result comes back to the view as a message.
3. `W` reaches the root's `actionKey` (`internal/ui/modal.go`), which asks the write module (`ui.Actions`, implemented by `actions.Session` in `internal/ui/actions/write.go`) to switch to write mode: a confirmation, then the instance lock.
4. `a` asks the view for its `Selection`, a `ui.Target` naming `tank/home`, and passes it to `Session.Menu`, which lists the `Offer`s from `actions.ZFSOffers` (`internal/ui/actions/zfs.go`) in a menu (`actions/menu.go`).
5. `s` opens the offer's form (`actions/form.go`); its `Build` calls `plan.Builder.SnapshotCreate` (`internal/zfs/plan/snapshot.go`), which returns a `mutate.Plan`: the `zfs snapshot` argv, what it affects, and how to read the state again.
6. The confirmation (`actions/confirm.go`) shows the plan and asks what `mutate.TierOf` (`internal/mutate/tiers.go`) says: for a snapshot, one key.
7. `mutate.Pipeline.Run` (`internal/mutate/run.go`) reads the state again and stops if it changed; `execute.Mutator.Mutate` (`internal/execute/mutate.go`) runs the step, and `audit.Log.Record` (`internal/audit/audit.go`) writes it to the audit log before and after.
8. The confirmation sends `ui.ChangedMsg`; every view reads again, and the new snapshot appears.

## Important files

| File | Defines |
|---|---|
| `cmd/fstui/main.go` | start-up, the subcommands, what a module gives (`opened`) |
| `cmd/fstui/module_*.go` | each module's views, changes and sources, and its stand-in when left out |
| `internal/ui/ui.go` | the root model, `Screen` and its optional interfaces, `ConfigMsg` |
| `internal/ui/modal.go` | `Target`, `Selector`, `Actions`: the boundary between views and the write module |
| `internal/ui/keys/keys.go` | every action a key triggers, and the default keys |
| `internal/config/settings.go` | every setting, with its range and default |
| `internal/config/alerts.go` | the measures alerts can compare (`Metrics`), the built-in alerts (`BuiltinTemplates`), and how alerts and templates are read |
| `internal/check/alerts.go` | how alerts are evaluated against a backend's measures |
| `internal/mutate/plan.go`, `tiers.go` | what a change is, and how much each kind asks to confirm |
| `internal/execute/tools.go` | the programs fstui runs, and how their paths are trusted |
| `internal/zfs/plan/plan.go` | `Builder` and how a saved change is built again (`Build`) |
| `internal/ui/actions/zfs.go` | which changes are offered for which selection |

## Concepts

- **Screen**: a view or tab; it reads, draws itself in a given size and takes keys (`ui.Screen`). Optional interfaces add activation, selection, typing and clicks.
- **Target**: what is selected, as far as changes care: backend, kind, names (`ui.Target`).
- **Offer**: one change offered for a target, with its key, form fields and builder (`actions.Offer`).
- **Plan**: a change ready to confirm: steps, what it affects or destroys, what blocks it, the state it depends on (`mutate.Plan`). A plan is never run without its re-check.
- **Tier**: how much a confirmation asks: a key, y then enter, or typing the name (`mutate.Tier`). Only `mutate/tiers.go` decides it.
- **Spec**: one setting in the registry (`config.Spec`).
- **Source**: what a storage backend gives fstui check: measures of each pool and the kernel's events (`check.Source`).
- **Measure**: one pool's numbers and states by metric name, such as `capacity` or `devices_down` (`problem.Measure`); a backend reports what it can measure.
- **Template** and **Alert**: a template is a condition on one measure, with a threshold, a severity and its message (`config.Template`); an alert turns a template on for some pools and channels (`config.Alert`). The Alerts tab shows each template as a line to tick.
- **Task**: periodic work a backend asks for, such as a scrub, that the Schedules tab runs (`schedule.Task`).
- **Module**: a part of fstui a build can leave out (a backend, the write module), with its build tag and its stand-in file.

Dependencies run one way: `cmd/fstui` wires everything; views (`ui`, `ui/zfsview`) read through backend packages (`zfs`) and never run commands; changes go from `ui/actions` through `mutate` to `execute`; nothing below `ui` imports it.

## Event loop

The interface is one Bubble Tea program. `Update` runs on one goroutine and is the only code that changes a model. Anything slow, reading a pool or sending a message, runs in a `tea.Cmd` on another goroutine; a command captures the values it needs when it is made, never the model, and reports back with a message. Pollers (`zfsview/poll.go`) schedule their next read with a tick. Streams that never end (`zpool iostat`) deliver each line through a channel that a command reads one message at a time. `fstui check` and `fstui watch` have no interface; `check.Watch` is a plain loop with timers.

## Settings

Every setting with a single value is one `Spec` in `internal/config/settings.go`: its path, description, kind, range and default. The YAML parser, the range checks, the Config view, the reference table in `docs/configuration.md` and the editor schema `docs/config.schema.json` come from that list. Lists and maps (keys, colours, event filters, dashboard panels, notification channels, templates and alerts) have their own parsers.

The views share one `*config.Config` and receive `ui.ConfigMsg` when the Config view saves, so a setting applies without a restart unless its `Spec` is marked `Restart`.

## Adding

- **A setting:** add the field and its default in `internal/config`, a `Spec` in `settings.go`, read it where it is used, apply it on `ui.ConfigMsg`, then `make docs` to regenerate the reference and the schema (tests fail until you do). A number that shapes what fstui shows or does is a setting, not a constant.
- **A view or tab:** implement `ui.Screen` (`internal/ui/ui.go`); the optional interfaces beside it add activation, selection for changes, typing and clicks. A ZFS view goes in `zfsview`, a backend-neutral one in `ui`.
- **A change:** a builder returning a `mutate.Plan`, a row in `mutate/tiers.go`, an `Offer` in `ui/actions`, and a case in the backend's `Build` so a saved plan can be rebuilt. Test the builder against a real test pool.
- **A built-in alert:** a `Template` in `config.BuiltinTemplates` with its category and an `About` for the Alerts tab, using a metric from `config.Metrics`; a new metric also needs each backend's `Measures` to report it.
- **A notification type:** a sender in `internal/notify` using `post` (HTTP) or the SMTP client, a case in `Send`, the type's fields in `config.Channel` validation, and a test against `httptest`.
- **A backend:** its readers and parsers, a `check.Source` reporting the measures it can (`config.Metrics`), `schedule.Task`s for its maintenance, its views, a module file pair in `cmd/fstui` behind a `fstui_no<name>` tag, and an entry in the registry.

## Tests

- Unit tests sit beside the code. Parsers are tested against real output in `testdata/zfs/2.3.9`; never write expected output by hand.
- Views are compared with golden files (`internal/ui/uitest`).
- Pool operations run against file-backed pools: `make integration-build`, then `sudo sh scripts/integration.sh bin/integration.test` (see Tests in CONTRIBUTING.md).
- Parsers of untrusted input have fuzz tests (`go test -fuzz`).
