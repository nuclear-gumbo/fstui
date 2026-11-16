#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Record `zfs send -n -P` (dry run, parsable) for the send size planner:
# full and incremental streams, a bookmark source, compressed, raw and
# large-block streams, on a throwaway pool on a sparse file. Must run as
# root on a disposable host; nothing is sent.
#
# Writes into the directory given as the only argument; copy the result
# into testdata/zfs/<version>/send/ as the unprivileged user afterwards.
#
# Usage: capture-send.sh <output-dir>

set -eu

if [ "$(id -u)" -ne 0 ]; then
	echo "capture-send: must run as root (creates a pool)" >&2
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
pool=fstuitest_send
img=$imgdir/$pool.img

# /var/tmp is writable by anyone: refuse a directory someone else created.
prepare_dir() {
	[ -e "$1" ] || mkdir -m 0750 "$1"
	if [ -L "$1" ] || [ ! -d "$1" ] || [ "$(stat -c %u "$1")" != 0 ] ||
		[ $((0$(stat -c %a "$1") & 022)) -ne 0 ]; then
		echo "capture-send: $1 must be a directory owned by root and writable only by root; remove it" >&2
		exit 1
	fi
}
prepare_dir "$imgdir"
prepare_dir "$mntdir"

cleanup() {
	zpool destroy -f -- "$pool" 2>/dev/null || true
	rm -f -- "$img"
}
trap cleanup EXIT
cleanup

truncate -s 1G "$img"
zpool create -o cachefile=none -O mountpoint="$mntdir/$pool" -O compression=lz4 -- "$pool" "$img"
zfs create -o recordsize=1M -- "$pool/data"
d=$mntdir/$pool/data

# Compressible text and incompressible random data, three snapshots apart.
yes "fstui send size planner, compressible line" | head -c 64M > "$d/text"
head -c 32M /dev/urandom > "$d/random"
sync
zfs snapshot -- "$pool/data@s1"
zfs bookmark -- "$pool/data@s1" "$pool/data#b1"
head -c 16M /dev/urandom > "$d/random2"
sync
zfs snapshot -- "$pool/data@s2"
yes "more compressible text after s2" | head -c 48M > "$d/text2"
rm -- "$d/random"
sync
zfs snapshot -- "$pool/data@s3"

# An encrypted dataset for raw (-w) sends; the key is a throwaway.
printf 'throwaway-passphrase\n' | zfs create -o encryption=on -o keyformat=passphrase -o keylocation=prompt -- "$pool/secret"
head -c 8M /dev/urandom > "$mntdir/$pool/secret/blob"
sync
zfs snapshot -- "$pool/secret@s1"
zfs snapshot -r -- "$pool@all"

rec() { # rec <name> <zfs send arguments...>
	name=$1
	shift
	start=$(date +%s%N)
	zfs send -n -P "$@" > "$out/$name.txt" 2> "$out/$name.err" || echo "exit $?" >> "$out/$name.err"
	echo "$name $(( ($(date +%s%N) - start) / 1000000 )) ms" >> "$out/timings.txt"
	[ -s "$out/$name.err" ] || rm -f -- "$out/$name.err"
}
: > "$out/timings.txt"
rec full "$pool/data@s1"
rec full-c -c "$pool/data@s1"
rec full-Le -L -e "$pool/data@s1"
rec incr-i -i @s1 "$pool/data@s3"
rec incr-i-c -c -i "$pool/data@s2" "$pool/data@s3"
rec incr-between -I @s1 "$pool/data@s3"
rec incr-bookmark -i "$pool/data#b1" "$pool/data@s3"
rec raw -w "$pool/secret@s1"
rec not-raw-encrypted "$pool/secret@s1"
rec replicate -R "$pool@all"
rec replicate-raw -R -w "$pool@all"
zfs unmount -- "$pool/secret"
zfs unload-key -- "$pool/secret"
rec raw-unloaded-key -w "$pool/secret@s1"
rec not-raw-unloaded-key "$pool/secret@s1"
rec missing -i @nope "$pool/data@s3"
zfs send -n -v -i @s1 "$pool/data@s3" > "$out/incr-i-v.txt" 2>&1 || true
zfs get -Hp -o name,property,value used,referenced,logicalreferenced,compressratio,written -t snapshot -r "$pool" > "$out/snapshot-props.txt"
zfs version > "$out/zfs-version.txt"
