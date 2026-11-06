#!/bin/sh
# Builds delta-review and installs it to $PREFIX/bin (default /usr/local/bin).
#
#   sudo ./install.sh
#   sudo PREFIX=/opt/delta-review ./install.sh
#
# Under sudo the build runs as the invoking user, so the Go module and build
# caches in their home directory don't end up owned by root.
set -eu

PREFIX=${PREFIX:-/usr/local}
BIN_DIR=$PREFIX/bin
SRC_DIR=$(cd "$(dirname "$0")" && pwd)

# sudo resets PATH (secure_path), which usually drops the Go install directory.
GO=$(command -v go || true)
if [ -z "$GO" ] && [ -x /usr/local/go/bin/go ]; then
	GO=/usr/local/go/bin/go
fi
if [ -z "$GO" ]; then
	echo "install.sh: go not found; put it on PATH or install it to /usr/local/go" >&2
	exit 1
fi

as_user() {
	if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ]; then
		sudo -u "$SUDO_USER" -H "$@"
	else
		"$@"
	fi
}

BUILD_DIR=$(as_user mktemp -d)
trap 'rm -rf "$BUILD_DIR"' EXIT

echo "building with $("$GO" version)"
(cd "$SRC_DIR" && as_user "$GO" build -trimpath -o "$BUILD_DIR/delta-review" .)

install -d "$BIN_DIR"
install -m 755 "$BUILD_DIR/delta-review" "$BIN_DIR/delta-review"
echo "installed $BIN_DIR/delta-review"
