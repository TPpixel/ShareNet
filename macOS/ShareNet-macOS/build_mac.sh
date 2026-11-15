#!/bin/bash
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
SRC="$DIR/src/HotspotVPNApp.swift"
OUT="$DIR/ShareNet.app/Contents/MacOS/ShareNet-native"

if ! command -v swiftc >/dev/null 2>&1; then
    echo "Ошибка: swiftc не найден. Установите Xcode Command Line Tools: xcode-select --install"
    exit 1
fi

ARCH="$(uname -m)"
if [ "$ARCH" != "arm64" ] && [ "$ARCH" != "x86_64" ]; then
    ARCH="x86_64"
fi

echo "Компиляция ShareNet-native ($ARCH)..."
swiftc -O -parse-as-library \
    -target "${ARCH}-apple-macos12.0" \
    -framework SwiftUI \
    -framework AppKit \
    -o "$OUT" \
    "$SRC"

chmod +x "$OUT"
xattr -dr com.apple.quarantine "$DIR/ShareNet.app" 2>/dev/null || true
echo "Готово: $OUT"
