# Keys

`?` in fstui lists the keys of the current view. Every key below can be rebound in the configuration file ([configuration.md](configuration.md#keys)), except the letters in the change menus.

## Everywhere

| Key | Action |
|---|---|
| `0`-`9` | Dashboard, Pools, Datasets, Snapshots, Volumes, ARC, I/O, Events, Plans, Config |
| `j` `k`, `down` `up` | Move |
| `g` `G`, `home` `end` | First, last |
| `h` `l`, `left` `right` | Previous and next tab; in Datasets, collapse and expand |
| `enter`, `space` | Open |
| `esc`, `q` | Back; quit at the top |
| `ctrl+c` | Quit |
| `?` | Help |
| `H` | Other hosts: list them and switch to one ([remote.md](remote.md)) |
| `r` | Read now |
| `b` | Exact byte counts or IEC units |
| `a` | Changes for the selection |
| `n` | New snapshot of the selected dataset |
| `W` | Write mode on or off |

## In some views

| Key | View | Action |
|---|---|---|
| `x` | Pools | Hide a warning for the session |
| `i` | Pools, History tab | Show or hide internal events |
| `enter`, `r`, `c` | Pools, Tune tab | Show the details; measure again; measure compression per file |
| `s`, `S` | Snapshots, a dataset's Snapshots tab | Sort by the next column; reverse the order |
| `m`, `M` | Datasets, Snapshots | Mark for one change to several; clear the marks |
| `=` | Snapshots | What changed since the snapshot (`zfs diff`) |
| `e` | Snapshots | Estimate the size of a `zfs send` |
| `/` | Events, audit log | Filter by text |
| `P`, `v`, `c` | Events | Next pool, minimum severity, class |
| `f`, `F` | Events | Next saved filter; follow the newest event |
| `p`, `w`, `u`, `d` | Config, General tab | Review the change as a diff, write it, revert to the previous file, discard |
| `t` | Config, Notifications tab | Send a test message to the selected channel |
| `a`, `enter`, `d` | Config, Notifications tab | Add a channel, edit the selected one, remove it (press twice) |
| `s` | Config, Notifications tab | Set up sending: open the Schedules tab on `fstui watch`, its change ready |
| `a`, `enter`, `d` | Config, Hosts tab | Add a host, change the selected one, remove it (press twice) |
| `space`, `t` | Config, Hosts tab | Send notifications about the selected machine or not; test its connection |
| `space`, `enter` | Config, Alerts tab | Turn the selected alert on or off; change its pools, channels and thresholds |
| `a`, `d` | Config, Alerts tab | Add your own alert; remove your own (press twice) |
| `p`, `w` | Config, Notifications and Alerts tabs | Review the changes as a diff, write them |
| `a`, `r` | Config, Schedules tab | Change the selected schedule (write mode); read the schedules again |

In the send size window, `c`, `w` and `I` switch compressed, raw and intermediate snapshots, and `t` sets a capacity.

## Forms and confirmations

| Key | Action |
|---|---|
| `tab`, `down`, `up` | Next or previous field |
| `space` | Switch a field on or off; next choice |
| `enter` | Next field; on the last one, show the change |
| `ctrl+enter` | Show the change from any field (`alt+enter` in terminals that send ctrl+enter as enter) |
| `y` | Choose the change's button in a confirmation |
| `n`, `esc` | Cancel |
| `ctrl+s` | Save the change for later, in Plans |

## Change menus

`a` opens the changes for the selection. Changes that are not possible now are listed below "Unavailable" with the reason.

### Pool

| Key | Change |
|---|---|
| `s` | Start a scrub |
| `p` | Pause the scrub |
| `r` | Resume the scrub |
| `S` | Stop the scrub |
| `e` | Check known errors only |
| `t` | Trim |
| `i` | Initialize free space |
| `c` | Clear errors |
| `A` | Add devices |
| `C` | Take a checkpoint |
| `D` | Discard the checkpoint |
| `L` | Look at the checkpoint, read-only |
| `w` | Rewind to the checkpoint |
| `X` | Cancel the removal of a vdev |
| `x` | Export the pool |
| `u` | Upgrade: enable new features |
| `N` | Create a new pool |
| `I` | Import a pool |

With no pool imported, the Pools view offers `N` and `I` only.

### Device (Pools, Topology tab, `enter`, then a device)

| Key | Change |
|---|---|
| `o` | Bring online |
| `f` | Take offline |
| `c` | Clear its errors |
| `a` | Attach a device: mirror it, or widen its raidz |
| `r` | Replace it |
| `d` | Detach it from its mirror |
| `R` | Remove from the pool (with its mirror, if any) |

### Filesystem

| Key | Change |
|---|---|
| `s` | Create snapshot |
| `n` | Create filesystem below |
| `E` | Create encrypted filesystem below |
| `v` | Create volume below |
| `p` | Promote clone |
| `m` | Rename |
| `e` | Set a property |
| `i` | Inherit a property |
| `o` | Mount |
| `U` | Unmount |
| `D` | Destroy |
| `l` | Load key |
| `u` | Unload key |
| `c` | Change passphrase |

With several filesystems marked, `s` creates one snapshot of each.

### Volume

| Key | Change |
|---|---|
| `s` | Create snapshot |
| `z` | Resize |
| `p` | Promote clone |
| `m` | Rename |
| `e` | Set a property |
| `i` | Inherit a property |
| `D` | Destroy |

### Snapshot

| Key | Change |
|---|---|
| `h` | Hold |
| `u` | Release a hold |
| `b` | Create bookmark |
| `c` | Clone |
| `r` | Rename |
| `R` | Roll back to this snapshot |
| `d` | Destroy |

With several snapshots marked:

| Key | Change |
|---|---|
| `h` | Hold all |
| `u` | Release a hold on all |
| `d` | Destroy all |

### Tune tab

The changes the findings point to, when there are any: `r` sets a dataset's `recordsize`, `c` its `compression`, `p` a module parameter (`zfs_prefetch_disable`, `zfs_arc_meta_balance`) until the next reboot, `s` the ARC limits.

### ARC

| Key | Change |
|---|---|
| `s` | Set ARC limits now |
| `b` | Keep ARC limits after reboot |

### Schedule (Config, Schedules tab)

| Key | Change |
|---|---|
| `w`, `m`, `o` | A scrub or trim weekly, monthly, off |
| `n`, `o` | `fstui check` or `fstui watch` on, off |

