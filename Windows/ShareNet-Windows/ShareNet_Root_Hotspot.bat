@echo off
chcp 65001 >nul
setlocal EnableDelayedExpansion
title ShareNet — Root Hotspot (ПК -> USB -> Android -> Wi-Fi -> Mac)

echo ============================================================================
echo   ShareNet Root Hotspot: ПК --[USB]--^> Android (Root) --[Wi-Fi]--^> Mac
echo   На Mac ничего устанавливать не нужно — просто подключитесь к Wi-Fi.
echo ============================================================================
echo.

:: 1. Поиск adb.exe и gnirehtet.exe (рядом со скриптом, в runtime ShareNet или в PATH)
set "ADB_EXE="
set "GNIREHTET_EXE="
set "GNIREHTET_APK="

if exist "%~dp0adb.exe" set "ADB_EXE=%~dp0adb.exe"
if exist "%~dp0platform-tools\adb.exe" set "ADB_EXE=%~dp0platform-tools\adb.exe"
if exist "%~dp0gnirehtet.exe" set "GNIREHTET_EXE=%~dp0gnirehtet.exe"
if exist "%~dp0gnirehtet\gnirehtet.exe" set "GNIREHTET_EXE=%~dp0gnirehtet\gnirehtet.exe"
if exist "%~dp0gnirehtet.apk" set "GNIREHTET_APK=%~dp0gnirehtet.apk"
if exist "%~dp0gnirehtet\gnirehtet.apk" set "GNIREHTET_APK=%~dp0gnirehtet\gnirehtet.apk"

if not defined ADB_EXE (
    for /d %%D in ("%LOCALAPPDATA%\ShareNet\runtime\*") do (
        if exist "%%~fD\platform-tools\adb.exe" set "ADB_EXE=%%~fD\platform-tools\adb.exe"
        if exist "%%~fD\adb.exe" set "ADB_EXE=%%~fD\adb.exe"
        if exist "%%~fD\gnirehtet\gnirehtet.exe" set "GNIREHTET_EXE=%%~fD\gnirehtet\gnirehtet.exe"
        if exist "%%~fD\gnirehtet.exe" set "GNIREHTET_EXE=%%~fD\gnirehtet.exe"
        if exist "%%~fD\gnirehtet\gnirehtet.apk" set "GNIREHTET_APK=%%~fD\gnirehtet\gnirehtet.apk"
        if exist "%%~fD\gnirehtet.apk" set "GNIREHTET_APK=%%~fD\gnirehtet.apk"
    )
)

if not defined ADB_EXE (
    if exist "%LOCALAPPDATA%\Android\Sdk\platform-tools\adb.exe" (
        set "ADB_EXE=%LOCALAPPDATA%\Android\Sdk\platform-tools\adb.exe"
    )
)

if not defined ADB_EXE (
    for %%P in (adb.exe) do set "ADB_EXE=%%~$PATH:P"
)

if not defined ADB_EXE (
    echo [!] Не найден adb.exe.
    echo     Запустите хотя бы один раз ShareNet.exe ^(он сам распакует adb и gnirehtet^),
    echo     либо положите этот .bat рядом с ShareNet.exe / adb.exe.
    echo.
    pause
    exit /b 1
)

for %%F in ("%ADB_EXE%") do set "PATH=%%~dpF;%PATH%"
echo [*] Используется ADB: %ADB_EXE%

:: 2. Ожидание подключения телефона по USB
echo [*] Ожидание Android-устройства по USB (включите Отладку по USB)...
"%ADB_EXE%" wait-for-device

:: 3. Проверка Root-доступа (Magisk / KernelSU / APatch)
echo [*] Проверка Root-прав на телефоне (разрешите запрос Superuser на экране)...
set "ROOT_OK="
for /f "delims=" %%R in ('"%ADB_EXE%" shell "su -c id" 2^>nul') do (
    echo %%R | findstr /i "uid=0" >nul && set "ROOT_OK=1"
)
if not defined ROOT_OK (
    echo [!] Root-доступ не получен ^(su -c id не вернул uid=0^).
    echo     Убедитесь, что на телефоне есть Root и для Shell ^(ADB^) выданы права в Magisk/KernelSU.
    echo.
    pause
    exit /b 1
)
echo [+] Root-доступ подтверждён.

:: 4. Генерация и загрузка вспомогательного shell-скрипта на телефон
set "TMP_SH=%TEMP%\sharenet_root.sh"
(
echo #!/system/bin/sh
echo find_tun^(^) {
echo   ip -o -4 addr show ^| while read -r _ iface _ cidr _; do
echo     case "$iface" in
echo       tun*^) echo "$iface"; return 0 ;;
echo     esac
echo   done
echo }
echo find_ap^(^) {
echo   ip -o -4 addr show ^| while read -r _ iface _ cidr _; do
echo     case "$iface" in
echo       ap*^|swlan*^|softap*^|wlan1*^|wlan2*^|rndis*^|usb*^|ncm*^)
echo         echo "$iface $cidr"; return 0 ;;
echo       wlan0^)
echo         case "$cidr" in
echo           192.168.43.*^|192.168.137.*^) echo "$iface $cidr"; return 0 ;;
echo         esac ;;
echo     esac
echo   done
echo }
echo case "$1" in
echo   check_tun^) find_tun ;;
echo   check_ap^)  find_ap ;;
echo   start^)
echo     TUN="$2"
echo     AP="$3"
echo     CIDR="$4"
echo     SUBNET=$^(ip -o -4 route show dev "$AP" proto kernel 2^>/dev/null ^| while read -r net _; do echo "$net"; break; done^)
echo     [ -z "$SUBNET" ] ^&^& SUBNET="$CIDR"
echo     sysctl -w net.ipv4.ip_forward=1 ^>/dev/null 2^>^&1
echo     sysctl -w net.ipv4.conf.all.rp_filter=0 ^>/dev/null 2^>^&1
echo     sysctl -w net.ipv4.conf."$TUN".rp_filter=0 ^>/dev/null 2^>^&1
echo     sysctl -w net.ipv4.conf."$AP".rp_filter=0 ^>/dev/null 2^>^&1
echo     while ip rule del pref 100 2^>/dev/null; do :; done
echo     ip route flush table 999 2^>/dev/null
echo     ip route replace "$SUBNET" dev "$AP" table 999
echo     ip route replace default dev "$TUN" table 999
echo     ip rule add from "$SUBNET" lookup 999 pref 100
echo     ip rule add to "$SUBNET" lookup 999 pref 100
echo     ip rule add iif "$AP" lookup 999 pref 100
echo     iptables -D FORWARD -i "$AP" -o "$TUN" -j ACCEPT 2^>/dev/null
echo     iptables -D FORWARD -i "$TUN" -o "$AP" -m state --state RELATED,ESTABLISHED -j ACCEPT 2^>/dev/null
echo     iptables -I FORWARD 1 -i "$AP" -o "$TUN" -j ACCEPT
echo     iptables -I FORWARD 1 -i "$TUN" -o "$AP" -m state --state RELATED,ESTABLISHED -j ACCEPT
echo     iptables -t nat -D POSTROUTING -o "$TUN" -j MASQUERADE 2^>/dev/null
echo     iptables -t nat -I POSTROUTING 1 -o "$TUN" -j MASQUERADE
echo     iptables -t nat -D PREROUTING -i "$AP" -p udp --dport 53 -j DNAT --to-destination 1.1.1.1:53 2^>/dev/null
echo     iptables -t nat -D PREROUTING -i "$AP" -p tcp --dport 53 -j DNAT --to-destination 1.1.1.1:53 2^>/dev/null
echo     iptables -t nat -I PREROUTING 1 -i "$AP" -p udp --dport 53 -j DNAT --to-destination 1.1.1.1:53
echo     iptables -t nat -I PREROUTING 1 -i "$AP" -p tcp --dport 53 -j DNAT --to-destination 1.1.1.1:53
echo     iptables -t mangle -D FORWARD -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu 2^>/dev/null
echo     iptables -t mangle -I FORWARD 1 -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
echo     ;;
echo   stop^)
echo     TUN="$2"
echo     AP="$3"
echo     while ip rule del pref 100 2^>/dev/null; do :; done
echo     ip route flush table 999 2^>/dev/null
echo     iptables -D FORWARD -i "$AP" -o "$TUN" -j ACCEPT 2^>/dev/null
echo     iptables -D FORWARD -i "$TUN" -o "$AP" -m state --state RELATED,ESTABLISHED -j ACCEPT 2^>/dev/null
echo     iptables -t nat -D POSTROUTING -o "$TUN" -j MASQUERADE 2^>/dev/null
echo     iptables -t nat -D PREROUTING -i "$AP" -p udp --dport 53 -j DNAT --to-destination 1.1.1.1:53 2^>/dev/null
echo     iptables -t nat -D PREROUTING -i "$AP" -p tcp --dport 53 -j DNAT --to-destination 1.1.1.1:53 2^>/dev/null
echo     iptables -t mangle -D FORWARD -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu 2^>/dev/null
echo     ;;
echo esac
) > "%TMP_SH%"

"%ADB_EXE%" push "%TMP_SH%" /data/local/tmp/sharenet_root.sh >nul
"%ADB_EXE%" shell "sed -i 's/\r$//' /data/local/tmp/sharenet_root.sh && chmod 755 /data/local/tmp/sharenet_root.sh" >nul 2>&1

:: 5. Проверка/запуск USB-туннеля (gnirehtet -> tun0)
set "STARTED_GNIREHTET=0"
set "TUN_IFACE="
for /f "tokens=1" %%I in ('"%ADB_EXE%" shell "su -c 'sh /data/local/tmp/sharenet_root.sh check_tun'" 2^>nul') do (
    set "TUN_IFACE=%%I"
)

if not defined TUN_IFACE (
    if defined GNIREHTET_EXE (
        echo [*] USB-туннель ещё не запущен. Запускаю gnirehtet...
        if defined GNIREHTET_APK (
            "%ADB_EXE%" shell pm list packages com.genymobile.gnirehtet | findstr /i "gnirehtet" >nul
            if errorlevel 1 (
                echo [*] Установка gnirehtet.apk...
                "%ADB_EXE%" install -r "%GNIREHTET_APK%" >nul
            )
        )
        "%ADB_EXE%" shell "settings put global private_dns_mode off" >nul 2>&1
        for %%G in ("%GNIREHTET_EXE%") do set "GN_DIR=%%~dpG"
        start "ShareNet-gnirehtet" /min cmd /c "cd /d "!GN_DIR!" && "%GNIREHTET_EXE%" run -d 1.1.1.1"
        set "STARTED_GNIREHTET=1"
        echo [*] Подтвердите запрос VPN на экране телефона ^(если появился^)...
    ) else (
        echo [*] Интерфейс tun0 не найден. Запустите раздачу в ShareNet.exe и дождитесь подключения.
    )
)

echo [*] Ожидание поднятия VPN-интерфейса (tun0) на телефоне...
:wait_tun
for /f "tokens=1" %%I in ('"%ADB_EXE%" shell "su -c 'sh /data/local/tmp/sharenet_root.sh check_tun'" 2^>nul') do (
    set "TUN_IFACE=%%I"
)
if not defined TUN_IFACE (
    timeout /t 2 /nobreak >nul
    goto wait_tun
)
echo [+] USB-туннель активен: %TUN_IFACE%

:: 6. Проверка/включение точки доступа Wi-Fi на телефоне
echo [*] Поиск интерфейса Точки доступа Wi-Fi на телефоне...
set "AP_IFACE="
set "AP_CIDR="
for /f "tokens=1,2" %%A in ('"%ADB_EXE%" shell "su -c 'sh /data/local/tmp/sharenet_root.sh check_ap'" 2^>nul') do (
    set "AP_IFACE=%%A"
    set "AP_CIDR=%%B"
)
if not defined AP_IFACE (
    echo [*] Точка доступа ещё не включена. Открываю настройки и пробую включить...
    "%ADB_EXE%" shell "su -c 'cmd connectivity start-tethering wifi'" >nul 2>&1
    "%ADB_EXE%" shell "am start -a android.settings.TETHER_SETTINGS" >nul 2>&1
    echo [!] Включите «Точку доступа Wi-Fi» на телефоне...
)

:wait_ap
for /f "tokens=1,2" %%A in ('"%ADB_EXE%" shell "su -c 'sh /data/local/tmp/sharenet_root.sh check_ap'" 2^>nul') do (
    set "AP_IFACE=%%A"
    set "AP_CIDR=%%B"
)
if not defined AP_IFACE (
    timeout /t 2 /nobreak >nul
    goto wait_ap
)
echo [+] Точка доступа активна: %AP_IFACE% (%AP_CIDR%)

:: 7. Включение прозрачной Root-маршрутизации (Hotspot -> tun0)
echo [*] Включение прозрачного NAT и маршрутизации (%AP_IFACE% -^> %TUN_IFACE%)...
"%ADB_EXE%" shell "su -c 'sh /data/local/tmp/sharenet_root.sh start %TUN_IFACE% %AP_IFACE% %AP_CIDR%'"

echo.
echo ============================================================================
echo   [ГОТОВО] Прозрачная раздача активна!
echo   - Интерфейс USB-туннеля : %TUN_IFACE%
echo   - Точка доступа телефона: %AP_IFACE% (%AP_CIDR%)
echo   - DNS для клиентов Wi-Fi: 1.1.1.1 (прозрачный DNAT)
echo.
echo   Теперь просто подключите Mac к Wi-Fi телефона.
echo   Никаких программ или прокси на Mac включать НЕ нужно!
echo ============================================================================
echo.
echo Нажмите любую клавишу, чтобы остановить раздачу и откатить настройки...
pause >nul

:: 8. Откат правил при выходе
echo.
echo [*] Откат правил маршрутизации и iptables на телефоне...
"%ADB_EXE%" shell "su -c 'sh /data/local/tmp/sharenet_root.sh stop %TUN_IFACE% %AP_IFACE%'" >nul 2>&1
"%ADB_EXE%" shell "rm -f /data/local/tmp/sharenet_root.sh" >nul 2>&1
del /q "%TMP_SH%" >nul 2>&1

if "%STARTED_GNIREHTET%"=="1" (
    echo [*] Остановка gnirehtet...
    taskkill /f /im gnirehtet.exe >nul 2>&1
    "%ADB_EXE%" shell "am force-stop com.genymobile.gnirehtet" >nul 2>&1
)

echo [+] Всё чисто остановлено.
timeout /t 2 >nul
exit /b 0
