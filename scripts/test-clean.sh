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

if [ -d "$imgdir" ]; then
	find "$imgdir" -maxdepth 1 -type f -name "${prefix}*.img" -print -delete
fi
