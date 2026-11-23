#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Build fstui with each supported combination of omit tags and
# check that each binary reports exactly the modules it was built with.
# Add a line per new module or meaningful combination.

set -eu

bin=$(mktemp)
trap 'rm -f "$bin"' EXIT
fail=0

# expect <tags> <modules line> <exit without flags> <text expected on stderr>
expect() {
	tags=$1
	want=$2
	want_exit=$3
	want_err=$4
	go build -tags "$tags" -o "$bin" ./cmd/fstui
	got=$("$bin" --version | sed -n 's/^modules: //p')
	if [ "$got" != "$want" ]; then
		echo "FAIL: tags '$tags': modules '$got', want '$want'"
		fail=1
	fi
	set +e
	err=$("$bin" 2>&1 >/dev/null)
	code=$?
	set -e
	if [ "$code" -ne "$want_exit" ]; then
		echo "FAIL: tags '$tags': exit $code, want $want_exit"
		fail=1
	fi
	case $err in
	*"$want_err"*) ;;
	*)
		echo "FAIL: tags '$tags': stderr '$err' lacks '$want_err'"
		fail=1
		;;
	esac
}

expect "" "zfs, write, remote" 2 "needs a terminal"
expect "fstui_noremote" "zfs, write" 2 "needs a terminal"
expect "fstui_nowrite" "zfs, remote" 2 "needs a terminal"
expect "fstui_nowrite fstui_noremote" "zfs" 2 "needs a terminal"
expect "fstui_nozfs" "write" 2 "includes no storage backends"
expect "fstui_nozfs fstui_nowrite" "none" 2 "includes no storage backends"
expect "fstui_demo" "demo, write, remote" 2 "needs a terminal"
expect "fstui_demo fstui_noremote" "demo, write" 2 "needs a terminal"
expect "fstui_demo fstui_nowrite" "demo, remote" 2 "needs a terminal"

# Without the write module the binary contains no code that changes
# storage: none of these packages may be linked in. The demo
# includes the write module but simulates every change
# (cmd/fstui/module_demo_write_test.go).
# no_write_code <tags>
no_write_code() {
	deps=$(go list -deps -tags "$1" ./cmd/fstui)
	for pkg in internal/mutate internal/audit internal/zfs/plan internal/ui/actions; do
		case $deps in
		*"/fstui/$pkg
"* | *"/fstui/$pkg")
			echo "FAIL: tags '$1': links $pkg"
			fail=1
			;;
		esac
	done
	go build -tags "$1" -o "$bin" ./cmd/fstui
	if go tool nm "$bin" | grep -q 'execute\.(\*Mutator)'; then
		echo "FAIL: tags '$1': binary contains execute.Mutator"
		fail=1
	fi
}
no_write_code fstui_nowrite
no_write_code "fstui_demo fstui_nowrite"

# --version adds one line per module saying what it found on this host:
# the OpenZFS versions, or why there are none (most CI hosts have no ZFS).
# expect_describe <tags> <module> <yes|no>
expect_describe() {
	go build -tags "$1" -o "$bin" ./cmd/fstui
	line=$("$bin" --version | sed -n "s/^$2: //p")
	got=no
	[ -n "$line" ] && got=yes
	if [ "$got" != "$3" ]; then
		echo "FAIL: tags '$1': $2 line '$line', want one: $3"
		fail=1
	fi
}
expect_describe "" zfs yes
expect_describe fstui_nozfs zfs no
expect_describe fstui_demo demo yes
expect_describe fstui_demo zfs no

[ "$fail" -eq 0 ] && echo "build variants: all pass"
exit "$fail"
