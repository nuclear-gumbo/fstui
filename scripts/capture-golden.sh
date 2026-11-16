#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Build throwaway ZFS pools in known states and capture command output from
# them, for the parser golden tests. Must run as root on a disposable host,
# such as a VM; it creates and destroys pools on sparse files and briefly
# sets the zfs_scan_suspend_progress module parameter.
#
# Writes into the directory given as the only argument, never into the
# repository: copy the result into testdata/zfs/<version>/ as the
# unprivileged user afterwards.
#
# Usage: capture-golden.sh <output-dir>

set -eu

if [ "$(id -u)" -ne 0 ]; then
	echo "capture-golden: must run as root (creates pools)" >&2
	exit 1
fi
if [ $# -ne 1 ]; then
	echo "usage: $0 <output-dir>" >&2
	exit 2
fi
out=$1
mkdir -p "$out"

prefix=fstuitest_g_
imgdir=/var/tmp/fstui-test
mntdir=$imgdir/mnt
suspend=/sys/module/zfs/parameters/zfs_scan_suspend_progress
# /var/tmp is writable by anyone: a directory someone else created first
# would let them swap a backing file for a symlink before root writes to
# it with truncate or dd.
prepare_dir() {
	[ -e "$1" ] || mkdir -m 0750 "$1"
	if [ -L "$1" ] || [ ! -d "$1" ] || [ "$(stat -c %u "$1")" != 0 ] ||
		[ $((0$(stat -c %a "$1") & 022)) -ne 0 ]; then
		echo "capture-golden: $1 must be a directory owned by root and writable only by root; remove it" >&2
		exit 1
	fi
}
prepare_dir "$imgdir"
prepare_dir "$mntdir"

# destroy_pool refuses any pool without the test prefix. It is the only way
# this script destroys a pool.
destroy_pool() {
	case $1 in
	"$prefix"?*) zpool destroy -f -- "$1" ;;
	*)
		echo "capture-golden: refusing to destroy '$1' (no $prefix prefix)" >&2
		exit 1
		;;
	esac
}

cleanup() {
	echo 0 >"$suspend"
	zpool list -H -o name | while IFS= read -r p; do
		case $p in "$prefix"?*) destroy_pool "$p" ;; esac
	done
	find "$imgdir" -maxdepth 1 -type f -name "${prefix}*.img" -delete
	rm -rf "${mntdir:?}"/"${prefix:?}"*
}
trap cleanup EXIT

# img <name> creates a 512 MiB sparse file and prints its path.
img() {
	f=$imgdir/$prefix$1.img
	truncate -s 512M "$f"
	echo "$f"
}

# fill <mountpoint> <MiB> writes incompressible data so scans have work.
fill() {
	dd if=/dev/urandom of="$1/data" bs=1M count="$2" status=none
	sync
}

# capture <scenario> <pool> saves every read fstui parses for one pool.
capture() {
	d=$out/$1
	p=$2
	mkdir -p "$d"
	zpool list -j --json-int -- "$p" >"$d/zpool-list.json"
	zpool status -j --json-int -- "$p" >"$d/zpool-status.json"
	zpool status -- "$p" >"$d/zpool-status.txt"
	zpool get -j --json-int all -- "$p" >"$d/zpool-get.json"
	zfs list -j --json-int -t all -r -- "$p" >"$d/zfs-list.json"
	zfs get -j --json-int all -r -- "$p" >"$d/zfs-get.json"
	# fstui reads properties with -p: --json-int clamps values above 2^63-1.
	zpool get -j -p all -- "$p" >"$d/zpool-get-p.json"
	zfs get -j -p all -r -- "$p" >"$d/zfs-get-p.json"
	zpool iostat -H -p -l -v -y "$p" 1 1 >"$d/zpool-iostat.txt"
	zpool history -i -- "$p" >"$d/zpool-history.txt"
	echo "captured $1"
}

echo 0 >"$suspend"
zfs version -j >"$out/zfs-version.json"
zfs version >"$out/zfs-version.txt"

# 1. Healthy mirror with every dataset type, holds, clones, bookmarks,
#    encryption, and hostile values in a user property and a hold tag.
p=${prefix}healthy
zpool create -O mountpoint="$mntdir/$p" -O compression=lz4 "$p" mirror "$(img healthy-a)" "$(img healthy-b)"
zfs create -o compression=zstd -o quota=100M "$p/home"
zfs create "$p/home/user"
fill "$mntdir/$p/home/user" 16
zfs create -s -V 64M -o volblocksize=16K "$p/vol"
zfs snapshot -r "$p/home@daily-1"
fill "$mntdir/$p/home" 8
zfs snapshot -r "$p/home@daily-2"
zfs hold keep "$p/home@daily-1"
zfs hold "$(printf 'tag\033[1m')" "$p/home@daily-2"
zfs bookmark "$p/home@daily-1" "$p/home#bm-1"
zfs clone "$p/home@daily-1" "$p/clone"
zfs set "com.example:note=$(printf 'evil\033[2J\033]0;pwned\007\rok')" "$p/home"
printf 'testpassphrase\n' | zfs create -o encryption=on -o keyformat=passphrase "$p/secret"
printf 'testpassphrase\n' | zfs create -o encryption=on -o keyformat=passphrase "$p/locked"
zfs unmount "$p/locked"
zfs unload-key "$p/locked"
capture healthy "$p"
zfs holds -H -p -r "$p/home@daily-1" "$p/home@daily-2" >"$out/healthy/zfs-holds.txt"

# 2. Every vdev role: raidz1 data, special mirror, log, cache, spare.
p=${prefix}roles
zpool create -O mountpoint="$mntdir/$p" "$p" \
	raidz1 "$(img roles-a)" "$(img roles-b)" "$(img roles-c)" \
	special mirror "$(img roles-s1)" "$(img roles-s2)" \
	log "$(img roles-log)" \
	cache "$(img roles-cache)" \
	spare "$(img roles-spare)"
capture roles "$p"

# 3. Degraded: one raidz1 member offline.
p=${prefix}degraded
zpool create -O mountpoint="$mntdir/$p" "$p" raidz1 "$(img deg-a)" "$(img deg-b)" "$(img deg-c)"
zpool offline "$p" "$imgdir/${prefix}deg-b.img"
capture degraded "$p"

# 4. Checksum errors: corrupt one side of a mirror behind ZFS's back, then
#    scrub, which finds and repairs them. The first and last 4 MiB hold the
#    vdev labels and are left alone.
p=${prefix}cksum
zpool create -O mountpoint="$mntdir/$p" "$p" mirror "$(img ck-a)" "$(img ck-b)"
fill "$mntdir/$p" 96
dd if=/dev/urandom of="$imgdir/${prefix}ck-b.img" bs=1M seek=8 count=200 conv=notrunc status=none
sync
zpool scrub -w "$p"
capture checksum-repaired "$p"

# 5. Scrub in progress, frozen with zfs_scan_suspend_progress, then paused.
p=${prefix}scrub
zpool create -O mountpoint="$mntdir/$p" "$p" mirror "$(img sc-a)" "$(img sc-b)"
fill "$mntdir/$p" 128
echo 1 >"$suspend"
zpool scrub "$p"
sleep 2
capture scrub-in-progress "$p"
zpool scrub -p "$p"
capture scrub-paused "$p"
zpool scrub -s "$p"
echo 0 >"$suspend"

# 6. Resilver in progress: replace a mirror member, frozen.
p=${prefix}resilver
zpool create -O mountpoint="$mntdir/$p" "$p" mirror "$(img rs-a)" "$(img rs-b)"
fill "$mntdir/$p" 128
echo 1 >"$suspend"
zpool replace "$p" "$imgdir/${prefix}rs-b.img" "$(img rs-c)"
sleep 2
capture resilver-in-progress "$p"
echo 0 >"$suspend"

# 7. Checkpoint holding space: data deleted and written after it was taken
#.
p=${prefix}ckpt
zpool create -O mountpoint="$mntdir/$p" "$p" mirror "$(img ckpt-a)" "$(img ckpt-b)"
fill "$mntdir/$p" 32
zpool checkpoint "$p"
rm "$mntdir/$p/data"
fill "$mntdir/$p" 16
capture checkpoint "$p"

zpool events -H >"$out/zpool-events.txt"
echo "done: $out"
