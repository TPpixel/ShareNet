# ShareNet

Кроссплатформенный набор приложений для передачи интернета и VPN с компьютера с доступом в сеть на Android-смартфон по USB и дальнейшей раздачи на другие устройства (Windows и macOS) через точку доступа Wi-Fi или USB.

Форк развивает **Root-режим** (прозрачная раздача с рутованного Android на любые устройства без установки клиентских программ и прокси на принимающий ноутбук), сохраняя полную совместимость с оригинальным режимом без root.

## Как устроена цепочка

### Вариант 1. С Root на Android (прозрачный Wi-Fi без программ на принимающем ПК/Mac)

```text
ПК №1 (Windows, есть интернет)
  │  USB + ADB + gnirehtet (tun0) + ShareNet.exe (автоопределение Root)
  ▼
Android (Root: Magisk / KernelSU / APatch)
  │  Прозрачный NAT (iptables MASQUERADE + ip rule + DNS DNAT)
  │  Точка доступа Wi-Fi
  ▼
ПК №2 / Ноутбук (macOS, Windows, любые устройства)
  └─ Обычное подключение к Wi-Fi — без установки приложений и настройки прокси
```

### Вариант 2. Без Root на Android (через локальный прокси `:8080`)

```text
ПК №1 (Windows, есть интернет)
  │  USB + ADB + gnirehtet + SOCKS5 (127.0.0.1:10808)
  ▼
Android (ShareNet.apk, порт 8080)
  │  Точка доступа Wi-Fi или USB
  ▼
ПК №2 / Ноутбук (Windows или macOS)
  └─ Полный системный VPN (TUN через sing-box + DoH 1.1.1.1) или системный SOCKS5-прокси
```

---

## Проекты в репозитории

### 1. Android — [`Android/ShareNet-Android`](./Android/ShareNet-Android)
- **Язык и сборка:** чистая Java (Android SDK API 24–33, без внешних Gradle-зависимостей), нативная библиотека `libhev-socks5-tunnel.so`.
- **Дизайн:** Material 3 Expressive с системным Dynamic Color (`android.R.color.system_*`), аппаратно-ускоренным `RippleDrawable` и одометром адреса `IP:Port`.
- **Три режима в одном приложении:**
  - **Раздача** — локальный SOCKS5/HTTP(CONNECT)-прокси на порту `:8080`, работающий поверх активной сети телефона.
  - **Приём** — `VpnService` (`HotspotVpnService`) поверх `hev-socks5-tunnel` + DoH-резолвер для приёма трафика с ПК/сети на телефон по Wi-Fi или USB.
  - **Сброс** — мониторинг подключения, автоматическое отслеживание отключения USB (`UsbDisconnectWatcher`), сброс/перезапуск сетевых служб.
- **Сборка:** [`build.ps1`](./Android/ShareNet-Android/build.ps1) (через `aapt2`, `javac --release 11`, `d8`, `zipalign`, `apksigner`).

### 2. Windows — [`Windows/ShareNet-Windows`](./Windows/ShareNet-Windows)
- **Язык и интерфейс:** Go + WebView2 (`internal/winui`), стиль Windows 11 (автоматическая подстройка под светлую/тёмную тему и акцентный цвет Windows).
- **Режим «2 в 1»:**
  - **Раздача** — автоматический поиск телефона по ADB, установка `gnirehtet.apk` и `ShareNet.apk`, отключение Private DNS на время сеанса (с восстановлением при выходе), локальный SOCKS5 `127.0.0.1:10808` и реверс-туннель `gnirehtet`, отправка файлов/APK и терминал ADB.
  - **Приём** — автоопределение шлюза телефона, полный системный VPN (TUN `sing-box.exe` + DoH `1.1.1.1`) или системный SOCKS5-прокси без прав администратора, проверка подключения (`generate_204`), настройка Telegram `:8080`.
  - **Сброс** — журнал работы и полный откат маршрутов, DNS и настроек прокси.
- **Сборка:** [`build.ps1`](./Windows/ShareNet-Windows/build.ps1) — упаковывает рантайм-инструменты и собирает единый портативный `ShareNet.exe`.

### 3. macOS — [`macOS/ShareNet-macOS`](./macOS/ShareNet-macOS)
- **Язык и интерфейс:** Go (`src/macapp`) + интерфейс для macOS (`ui.html`, светлая/тёмная тема, поддержка SwiftUI в `src/HotspotVPNApp.swift`), бандл `ShareNet.app`.
- **Возможности:**
  - Автоматическое определение активного сетевого интерфейса (`networksetup`) и IP-адреса телефона;
  - **Полный VPN (TUN)** через `sing-box` + DoH `1.1.1.1`;
  - **Системный SOCKS5-прокси** macOS без ввода пароля администратора;
  - Локальный API с одноразовым токеном сессии и проверкой `Origin`.

---

## Готовые сборки (Releases)

Тяжёлые бинарные файлы (`ShareNet.exe`, `ShareNet.apk`, `tools.zip`, бинарник `sing-box` для macOS) исключены из истории Git через [`.gitignore`](./.gitignore) и распространяются отдельно в разделе **Releases** (или собираются локально скриптами `build.ps1` / `build_mac.sh`).

---

Разработчик: [TPpixel](https://github.com/TPpixel)
