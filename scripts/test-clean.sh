#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Destroy leftover test pools and their sparse backing files. Only pools whose
# name starts with the test prefix are touched, and only image files inside
# the dedicated test directory are deleted.

set -eu

prefix=fstuitest_
imgdir=/var/tmp/fstui-test

if [ "$(id -u)" -ne 0 ]; then
	echo "test-clean: must run as root (destroys pools)" >&2
	exit 1
fi

zpool list -H -o name | while IFS= read -r pool; do
	case $pool in
	"$prefix"?*)
		echo "destroying pool $pool"
		zpool destroy -f "$pool"
		;;
	esac
done

# Only a directory root owns and nobody else can write is cleaned: the
# tests refuse any other, so its files are not test pools' (testpool.go).
if [ -d "$imgdir" ] && [ ! -L "$imgdir" ]; then
	if [ "$(stat -c %u "$imgdir")" = 0 ] && [ $((0$(stat -c %a "$imgdir") & 022)) -eq 0 ]; then
		find "$imgdir" -maxdepth 1 -type f -name "${prefix}*.img" -print -delete
	else
		echo "test-clean: $imgdir is not owned by root or is writable by others; not touching it" >&2
	fi
fi
