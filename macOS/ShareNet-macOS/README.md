# ShareNet для macOS

Клиентское приложение **ShareNet** для macOS в стиле Apple **Liquid Glass**, предназначенное для приёма интернета/VPN с телефона Android (через точку доступа Wi-Fi или USB-модем) на порт `:8080`.

## Состав проекта

- **`src/macapp/`** — основной бэкенд на Go + веб-интерфейс (`ui.html`) с локальным HTTP API, защищённым случайным токеном сессии (`X-Session-Token`), и проверкой целостности.
- **`ShareNet.app`** — готовый `.app`-бандл для macOS (запускает бинарник `ShareNet` + окно интерфейса).
- **`src/HotspotVPNApp.swift` и `build_mac.sh`** — нативный SwiftUI/AppKit интерфейс (при наличии Xcode Command Line Tools).
- **`ShareNet.command`** — автономный терминальный переключатель VPN (`sing-box` + DoH `1.1.1.1`).
- **`preview-liquid-glass.html`** — офлайн-превью интерфейса Liquid Glass в браузере.

## Режимы работы

1. **Полный VPN (TUN через `sing-box`)** — направляет весь системный трафик macOS (`utun`) через SOCKS5 телефона (`<шлюз>:8080`) с DNS-over-HTTPS `1.1.1.1`. Запрашивает права администратора macOS через системный диалог.
2. **Системный прокси (`networksetup`)** — включает SOCKS5-прокси для активного интерфейса без прав администратора.

## Запуск и сборка

- При переносе на Mac используйте архив (или выполните `./Install_ShareNet_Mac.command`), чтобы восстановить права `+x` и снять `com.apple.quarantine` с `ShareNet.app` и `sing-box`.
- Ядро `sing-box` для macOS не хранится в истории Git из-за размера (>85 МБ) — поместите бинарник `sing-box` в корень `macOS/ShareNet-macOS/` и в `ShareNet.app/Contents/Resources/` (или скачайте готовый архив из GitHub Releases).
- Сборка Go-части (`src/macapp`):
  ```bash
  cd src/macapp && go build -o ../../ShareNet.app/Contents/MacOS/ShareNet .
  ```

Разработчик: [TPpixel](https://github.com/TPpixel)
