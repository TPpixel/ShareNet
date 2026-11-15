#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
echo "Подготовка прав запуска для ShareNet на macOS..."
xattr -cr "$DIR" 2>/dev/null || true
chmod +x "$DIR/sing-box" "$DIR/ShareNet.command" "$DIR/build_mac.sh" "$DIR/ShareNet.app/Contents/MacOS/ShareNet" 2>/dev/null || true

if command -v swiftc >/dev/null 2>&1; then
    echo "Компиляция нативного SwiftUI приложения..."
    "$DIR/build_mac.sh" || echo "Предупреждение: сборка swiftc пропущена, будет использован встроенный AppKit GUI."
fi

echo "Запускаю ShareNet.app..."
open "$DIR/ShareNet.app"
