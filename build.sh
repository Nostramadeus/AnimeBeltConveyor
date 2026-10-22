#!/bin/sh
# Builds abc (abc.exe when GOOS is windows) with its icon and version string.
# Needs only Go. GO=/path/to/go ./build.sh when go is not on PATH.
# The icon resource (cmd/abc/rsrc_windows_amd64.syso) is rebuilt from
# assets/icon/abc.ico only when it is missing or older than the .ico. Both files
# are committed, so a plain "go build ./cmd/abc" also gets the icon.
set -eu
cd "$(dirname "$0")"
GO="${GO:-go}"

ICO=assets/icon/abc.ico
SYSO=cmd/abc/rsrc_windows_amd64.syso
if [ -f "$ICO" ]; then
	if [ ! -f "$SYSO" ] || [ "$ICO" -nt "$SYSO" ]; then
		echo "icon: $ICO -> $SYSO"
		"$GO" run github.com/akavel/rsrc@v0.10.2 -ico "$ICO" -arch amd64 -o "$SYSO"
	fi
else
	echo "icon: $ICO not found, building without an icon"
fi

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
OUT=abc
if [ "$("$GO" env GOOS)" = windows ]; then OUT=abc.exe; fi
"$GO" build -ldflags "-X main.version=$VERSION" -o "$OUT" ./cmd/abc
echo "built $OUT ($VERSION)"
