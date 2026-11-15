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
"$bin/integration.test" -test.run '^TestIntegration' -test.count=1 -test.v "$@"
