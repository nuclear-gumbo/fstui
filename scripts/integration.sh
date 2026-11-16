#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Run the integration test binary as root. Build it first as an
# unprivileged user with "make integration-build"; root never compiles.
#
# The binary is copied into a root-owned directory before it runs, so the
# user who built it cannot replace it mid-run. Leftover test pools are
# removed afterwards even if the tests fail.
#
# Usage: integration.sh <path to integration.test> [go test flags...]

set -eu

if [ "$(id -u)" -ne 0 ]; then
	echo "integration: must run as root (creates pools)" >&2
	exit 1
fi
if [ $# -lt 1 ]; then
	echo "usage: $0 <integration.test> [flags...]" >&2
	exit 2
fi
src=$1
shift
here=$(cd "$(dirname "$0")" && pwd)

bin=$(mktemp -d)
trap 'rm -rf "$bin"; sh "$here/test-clean.sh"' EXIT
install -m 0700 "$src" "$bin/integration.test"

# Only the TestIntegration functions: the unit tests in the same binary
# read golden files relative to the package directory.
# Real devices a person allowed (CONTRIBUTING.md); the file is
# usually missing, and then no test touches a real disk. Each listed
# device is wiped first, so its test starts from an empty disk, unless
# it or a partition of it is mounted or in an imported pool.
allowed="$here/../testdata/allowed-devices.conf"
if [ -f "$allowed" ]; then
	grep -v -e '^#' -e '^[[:space:]]*$' "$allowed" | while IFS= read -r dev; do
		case $dev in
		/dev/disk/by-id/*) ;;
		*) echo "integration: $dev in $allowed is not a /dev/disk/by-id path" >&2; exit 1 ;;
		esac
		real=$(readlink -f "$dev")
		if [ ! -b "$real" ]; then
			echo "integration: $dev is not a block device; its test will skip" >&2
			continue
		fi
		if [ -n "$(lsblk -n -o MOUNTPOINTS "$real" | tr -d '[:space:]')" ] ||
			zpool status -P 2>/dev/null | grep -q -e "$dev" -e "$real"; then
			echo "integration: $dev is mounted or in an imported pool; not wiping it" >&2
			exit 1
		fi
		for part in $(lsblk -n -l -o PATH "$real" | tail -n +2); do
			wipefs -q -a "$part"
		done
		wipefs -q -a "$real"
	done
fi
FSTUI_ALLOWED_DEVICES="$allowed" \
	"$bin/integration.test" -test.run '^TestIntegration' -test.count=1 -test.v "$@"
