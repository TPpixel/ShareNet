#!/bin/bash
set -u

DIR="$(cd "$(dirname "$0")" && pwd)"
BIN="$DIR/sing-box"
CFG="/tmp/hotspot-vpn-singbox.json"
PROXY_PORT=8080

echo "=== Hotspot VPN для macOS (Intel) ==="

if [ ! -f "$BIN" ]; then
    echo "Ошибка: не найден файл $BIN"
    read -p "Нажмите Enter для выхода..."
    exit 1
fi

chmod +x "$BIN" 2>/dev/null || true
xattr -d com.apple.quarantine "$BIN" 2>/dev/null || true
xattr -d com.apple.quarantine "$0" 2>/dev/null || true

# Определяем активный сетевой сервис macOS (обычно "Wi-Fi")
detect_service() {
    local iface svc
    iface="$(route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}')"
    if [ -n "$iface" ] && [[ "$iface" != utun* ]]; then
        svc="$(networksetup -listallhardwareports 2>/dev/null | awk -v dev="$iface" '/Hardware Port:/{port=substr($0, index($0,$3))} $0 ~ "Device: "dev"$"{print port; exit}')"
    fi
    if [ -z "${svc:-}" ] || ! networksetup -getinfo "$svc" >/dev/null 2>&1; then
        svc="Wi-Fi"
    fi
    echo "$svc"
}

SERVICE="$(detect_service)"

flush_dns() {
    sudo dscacheutil -flushcache 2>/dev/null || true
    sudo killall -HUP mDNSResponder 2>/dev/null || true
}

restore_dns() {
    sudo networksetup -setdnsservers "$SERVICE" "Empty" 2>/dev/null || true
    flush_dns
}

# Если туннель уже запущен — выключаем его (режим переключателя Вкл/Выкл)
if pgrep -f "$CFG" >/dev/null 2>&1; then
    echo "Туннель уже запущен. Останавливаю..."
    sudo -v || exit 1
    sudo pkill -INT -f "$CFG" 2>/dev/null || true
    sleep 1
    restore_dns
    rm -f "$CFG" 2>/dev/null || true
    echo "VPN выключен, системный DNS ($SERVICE) и обычная сеть восстановлены."
    read -p "Нажмите Enter для закрытия..."
    exit 0
fi

# Определяем IP телефона (шлюз по умолчанию или роутер сервиса)
GW="$(route -n get default 2>/dev/null | awk '/gateway:/{print $2; exit}')"
if [ -z "$GW" ] || [[ "$GW" == 172.19.0.* ]]; then
    GW="$(networksetup -getinfo "$SERVICE" 2>/dev/null | awk -F': ' '/^Router:/{print $2; exit}')"
fi

if [ -z "$GW" ] || [ "$GW" = "none" ]; then
    echo "Ошибка: Mac не подключён к Wi-Fi точке доступа телефона."
    echo "Подключите Mac к Hotspot телефона и запустите скрипт снова."
    read -p "Нажмите Enter для выхода..."
    exit 1
fi

echo "Сетевой интерфейс: $SERVICE | Телефон (шлюз): $GW"

# Проверяем, открыт ли порт 8080 (Hotspot Proxy на телефоне)
if ! nc -z -w 3 "$GW" "$PROXY_PORT" >/dev/null 2>&1; then
    echo ""
    echo "Ошибка: не удалось подключиться к $GW:$PROXY_PORT."
    echo "Проверьте, что на телефоне запущен Hotspot Proxy (или ShareNetUSB на ПК)."
    read -p "Нажмите Enter для выхода..."
    exit 1
fi

# Проверяем, не мешают ли мёртвые переменные Cloudflare-релея (127.0.0.1:8899)
RELAY_ALIVE=0
if lsof -ti tcp:8899 -sTCP:LISTEN >/dev/null 2>&1; then
    RELAY_ALIVE=1
    echo "[INFO] Обнаружен активный локальный прокси :8899 (Cloudflare-релей)."
else
    for var in AG_LS_PROXY HTTPS_PROXY HTTP_PROXY ALL_PROXY https_proxy http_proxy all_proxy; do
        val="$(launchctl getenv "$var" 2>/dev/null || true)"
        if [[ "$val" == *"127.0.0.1:8899"* ]]; then
            echo "[FIX] Убираю мёртвую переменную $var=$val (порт 8899 не запущен)..."
            launchctl unsetenv "$var" 2>/dev/null || true
        fi
    done
fi

# Отключаем ручные системные прокси на Wi-Fi, чтобы весь трафик шёл через TUN
networksetup -setwebproxystate "$SERVICE" off 2>/dev/null || true
networksetup -setsecurewebproxystate "$SERVICE" off 2>/dev/null || true
networksetup -setsocksfirewallproxystate "$SERVICE" off 2>/dev/null || true

cat > "$CFG" <<EOF
{
  "log": {
    "level": "info",
    "timestamp": true
  },
  "dns": {
    "servers": [
      {
        "tag": "doh_cf",
        "type": "https",
        "server": "1.1.1.1",
        "server_port": 443,
        "path": "/dns-query",
        "detour": "proxy-out"
      }
    ],
    "final": "doh_cf",
    "strategy": "ipv4_only"
  },
  "inbounds": [
    {
      "type": "tun",
      "tag": "tun-in",
      "address": [
        "172.19.0.1/30"
      ],
      "mtu": 1500,
      "auto_route": true,
      "strict_route": false,
      "stack": "gvisor",
      "dns_mode": "hijack",
      "dns_address": [
        "172.19.0.2"
      ]
    }
  ],
  "outbounds": [
    {
      "type": "socks",
      "tag": "proxy-out",
      "server": "$GW",
      "server_port": $PROXY_PORT,
      "version": "5",
      "network": "tcp"
    },
    {
      "type": "direct",
      "tag": "direct-out"
    }
  ],
  "route": {
    "rules": [
      {
        "action": "sniff"
      },
      {
        "port": [53],
        "action": "hijack-dns"
      },
      {
        "protocol": "dns",
        "action": "hijack-dns"
      },
      {
        "ip_is_private": true,
        "outbound": "direct-out"
      },
      {
        "network": "udp",
        "action": "reject"
      }
    ],
    "final": "proxy-out",
    "auto_detect_interface": true
  }
}
EOF

if ! "$BIN" check -c "$CFG" >/dev/null 2>&1; then
    echo "Ошибка конфигурации sing-box:"
    "$BIN" check -c "$CFG"
    rm -f "$CFG" 2>/dev/null || true
    read -p "Нажмите Enter для выхода..."
    exit 1
fi

CLEANED=0
cleanup() {
    [ "$CLEANED" -eq 1 ] && return
    CLEANED=1
    echo ""
    echo "Останавливаю VPN-туннель и восстанавливаю DNS ($SERVICE)..."
    sudo pkill -INT -f "$CFG" 2>/dev/null || true
    sleep 1
    restore_dns
    rm -f "$CFG" 2>/dev/null || true
}
trap cleanup INT TERM EXIT

echo ""
echo "Для создания системного VPN-туннеля (utun) и настройки DNS введите пароль от Mac:"
sudo -v || exit 1

# Принудительно направляем системный DNS macOS в TUN (172.19.0.2), иначе mDNSResponder шлёт DNS на телефон ($GW) в обход utun
sudo networksetup -setdnsservers "$SERVICE" 172.19.0.2
flush_dns

echo "Запускаю sing-box..."
sudo "$BIN" run -c "$CFG" &
SB_PID=$!

# Ждём поднятия utun и сразу проверяем интернет + DNS
sleep 2
if ! sudo kill -0 "$SB_PID" 2>/dev/null; then
    echo ""
    echo "[ERROR] sing-box завершился с ошибкой. Проверьте вывод выше."
    read -p "Нажмите Enter для выхода..."
    exit 1
fi

flush_dns
HTTP_CODE="$(curl --noproxy '*' -s -m 8 -o /dev/null -w '%{http_code}' https://www.google.com/generate_204 || echo '000')"
echo ""
if [ "$HTTP_CODE" = "204" ] || [ "$HTTP_CODE" = "200" ]; then
    echo "=========================================================="
    echo "[OK] Интернет и DNS через телефон работают! (HTTP $HTTP_CODE)"
    echo "Теперь браузеры, Antigravity и все программы работают через телефон."
    echo "Для выключения нажмите Ctrl+C или закройте это окно."
    echo "=========================================================="
else
    echo "=========================================================="
    echo "[WARN] Проверка https://www.google.com/generate_204 вернула HTTP $HTTP_CODE."
    echo "Если браузер не открывает сайты, отключите в настройках браузера 'Безопасный DNS / DoH'."
    echo "Текущий DNS macOS:"
    scutil --dns | grep -E 'resolver #|nameserver\[0\]' | head -n 12
    echo "=========================================================="
fi

wait "$SB_PID"