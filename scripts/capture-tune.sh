#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Record what tune mode reads, around known workloads on a throwaway raidz1
# pool on sparse files: the pool's and the datasets' kstats and the global
# ARC, DMU transaction, ZIL and prefetch kstats, per phase, and zpool
# iostat -l interval reports during writes. Each phase writes a directory;
# the difference between two phases is that phase's workload.
# Must run as root on a disposable host.
#
# Writes into the directory given as the only argument; copy the result
# into testdata/zfs/<version>/tune/ as the unprivileged user afterwards.
#
# Usage: capture-tune.sh <output-dir>

set -eu

if [ "$(id -u)" -ne 0 ]; then
	echo "capture-tune: must run as root (creates a pool)" >&2
	exit 1
fi
if [ $# -ne 1 ]; then
	echo "usage: $0 <output-dir>" >&2
	exit 2
fi
out=$1
mkdir -p "$out"

imgdir=/var/tmp/fstui-test
mntdir=$imgdir/mnt
pool=fstuitest_tune

# /var/tmp is writable by anyone: refuse a directory someone else created.
prepare_dir() {
	[ -e "$1" ] || mkdir -m 0750 "$1"
	if [ -L "$1" ] || [ ! -d "$1" ] || [ "$(stat -c %u "$1")" != 0 ] ||
		[ $((0$(stat -c %a "$1") & 022)) -ne 0 ]; then
		echo "capture-tune: $1 must be a directory owned by root and writable only by root; remove it" >&2
		exit 1
	fi
}
prepare_dir "$imgdir"
prepare_dir "$mntdir"

imgs="$imgdir/$pool-a.img $imgdir/$pool-b.img $imgdir/$pool-c.img"
cleanup() {
	zpool destroy -f -- "$pool" 2>/dev/null || true
	# shellcheck disable=SC2086 # a fixed list of paths without spaces
	rm -f -- $imgs
}
trap cleanup EXIT
cleanup

# snap NAME: everything tune mode reads, as it is now.
snap() {
	d=$out/$1
	mkdir -p "$d"
	sync
	zpool sync -- "$pool"
	for f in /proc/spl/kstat/zfs/"$pool"/objset-* /proc/spl/kstat/zfs/"$pool"/txgs /proc/spl/kstat/zfs/"$pool"/iostats; do
		cp "$f" "$d/$(basename "$f")"
	done
	for f in arcstats dmu_tx zil zfetchstats; do
		cp /proc/spl/kstat/zfs/$f "$d/$f"
	done
	zfs get -H -p -r -o name,property,value \
		recordsize,volblocksize,compression,compressratio,refcompressratio,logicalused,used,sync,logbias,primarycache -- "$pool" > "$d/zfs-get.txt"
}

# shellcheck disable=SC2086
truncate -s 1G $imgs
# shellcheck disable=SC2086
zpool create -o cachefile=none -o ashift=12 -O mountpoint="$mntdir/$pool" -O compression=lz4 -- "$pool" raidz1 $imgs
zfs create -o recordsize=1M -- "$pool/seq"
zfs create -o recordsize=128K -o compression=off -- "$pool/small"
zfs create -o recordsize=128K -- "$pool/sync"
zfs create -o recordsize=128K -- "$pool/text"
m=$mntdir/$pool

head -c 64M /dev/urandom > "$m/small/file"
snap 0-base

# Sequential 1 MiB writes into 1 MiB records: no amplification beyond parity.
dd if=/dev/urandom of="$m/seq/file" bs=1M count=200 status=none
snap 1-seqwrite

# Random 4 KiB writes into a 64 MiB file of 128 KiB records: each one
# rewrites a whole record (read-modify-write when it is not cached).
shuf -i 0-16383 -n 2000 | while read -r off; do
	dd if=/dev/urandom of="$m/small/file" bs=4K count=1 seek="$off" conv=notrunc status=none
done
snap 2-smallwrite

# 8 KiB synchronous writes: each goes through the ZIL, on the main pool
# because the pool has no log device.
dd if=/dev/urandom of="$m/sync/file" bs=8K count=400 oflag=dsync status=none
snap 3-syncwrite

# Compressible text beside the incompressible files above.
yes "fstui tune mode, a compressible line of text" | head -c 64M > "$m/text/file"
snap 4-text

# Reads from a cold cache: export and import empty the ARC of the pool's
# blocks. Sequential 4 KiB reads (prefetch helps), then random ones.
zpool export -- "$pool"
zpool import -d "$imgdir" -- "$pool"
snap 5-cold
dd if="$m/seq/file" of=/dev/null bs=4K status=none
shuf -i 0-16383 -n 1000 | while read -r off; do
	dd if="$m/small/file" of=/dev/null bs=4K count=1 skip="$off" status=none
done
snap 6-reads

# Interval reports, as tune mode reads them while it measures: two
# 2-second reports (-y leaves out the since-import one), taken while a
# sequential and a random writer run.
(
	dd if=/dev/urandom of="$m/seq/file2" bs=1M count=150 status=none
	shuf -i 0-16383 -n 1500 | while read -r off; do
		dd if=/dev/urandom of="$m/small/file" bs=4K count=1 seek="$off" conv=notrunc status=none
	done
) &
writer=$!
mkdir -p "$out/interval"
zpool iostat -p -l -v -y -- "$pool" 2 2 > "$out/interval/zpool-iostat-l.txt"
wait "$writer" || true


zfs version > "$out/zfs-version.txt"
echo "capture-tune: wrote $out"
