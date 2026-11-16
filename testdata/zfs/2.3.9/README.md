# OpenZFS 2.3.9 recordings

Output of OpenZFS 2.3.9 (Debian 13, `zfs-2.3.9-0+deb13u1`), recorded by `scripts/capture-golden.sh` from throwaway pools on sparse files. The parser tests read these files and the demo build replays them. Do not edit them by hand; run the script again instead.

| Directory | Pool state |
|---|---|
| `healthy` | Mirror; filesystems, a sparse zvol, recursive snapshots, holds (one tag contains an escape sequence), a bookmark, a clone, two encrypted datasets (one key unloaded), a user property containing escape sequences, CR and BEL |
| `roles` | raidz1 plus special mirror, log, cache and spare devices |
| `degraded` | raidz1 with one member offline |
| `checksum-repaired` | Mirror with one side corrupted, after a scrub repaired 771 checksum errors |
| `scrub-in-progress` | Scrub held in progress (`zfs_scan_suspend_progress=1`) |
| `scrub-paused` | The same scrub paused (`zpool scrub -p`): `state` stays `SCANNING`, `scrub_pause` holds the pause time |
| `resilver-in-progress` | Mirror member being replaced, resilver held in progress |
| `checkpoint` | Mirror with a checkpoint holding 32 MiB of data deleted after it was taken |

Each scenario also has `zfs-get-p.json` and `zpool-get-p.json` (`get -j -p all`). fstui reads properties with `-p` because `--json-int` clamps values above 2^63-1.

Other files:

- `zfs-version.json`, `zfs-version.txt`: `zfs version`, as JSON and as plain text for the version check's fallback.
- `zpool-list-none.json`: `zpool list -j --json-int` with no pools imported, which prints nothing.
- `zpool-events.txt`: every event of the recording session.
- `events/`: `zpool events` in three formats after faults were induced on a test pool.
- `allow/`: `zfs allow` output for nested delegations, for a pool that only defines a permission set, and for a pool with none.
- `iostat/four-pools.txt`: `zpool iostat -p -l -v -y 1` for three intervals on four pools, two of them being written.
- `arcstats/`: `/proc/spl/kstat/zfs/arcstats` without cache devices (`no-l2arc.txt`) and with a 256 MiB file-backed cache device after 200 MiB were written and the pool re-imported (`l2arc.txt`).
- `send/`: `zfs send -n -P` (full, incremental, `-I`, compressed, raw, from a bookmark, an encrypted dataset with its key loaded and unloaded) and the matching `zfs list`, recorded by `scripts/capture-send.sh`.
- `create/`: `zpool create -n` for a valid layout and an invalid one.
- `raidz-expand/`: `zpool status` of a raidz1 of three files during and after `zpool attach` of a fourth (`raidz_expand_stats`). After an expansion ZFS starts a scrub by itself.
- `remove/`: `zpool status` while a mirror is removed, after `zpool remove -s` cancels it, and after it finished, when the removed vdev is no longer listed and `removing_vdev` no longer indexes a listed vdev. `zpool remove -n` prints only the mapping memory; on a pool with raidz it accepts removing a single-disk vdev that the removal itself refuses.
- `import/`: `zpool import -d` with two exported pools present (`zpool-import-ok.txt`), with one mirror side and one single-device pool's device missing (`zpool-import-missing.txt`: the mirror is `DEGRADED`, the other pool is not listed), and with nothing to import (`zpool-import-none.stderr`, exit 0). There is no JSON form of this listing.
- `upgrade/`: the compatibility files `grub2` (a link to `grub2-2.12`) and `openzfs-2.1-linux` from `/usr/share/zfs/compatibility.d`. `zpool upgrade` without a pool lists features that a pool's `compatibility` setting holds back; `zpool upgrade <pool>` does not enable them.

`testdata/systemd/257/list-timers.json` is `systemctl list-timers --all --output=json` with two OpenZFS scrub timers enabled, and `testdata/debian/13/cron.d-zfsutils-linux` is the cron file Debian's zfsutils-linux installs.
