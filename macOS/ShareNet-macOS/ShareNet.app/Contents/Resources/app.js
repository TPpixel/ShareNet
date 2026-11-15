ObjC.import('Cocoa');
ObjC.import('Foundation');

function run(argv) {
    var app = $.NSApplication.sharedApplication;
    app.setActivationPolicy($.NSApplicationActivationPolicyRegular);

    var appParent = (argv && argv.length > 0 && argv[0])
        ? String(argv[0])
        : String($.NSBundle.mainBundle.bundlePath.stringByDeletingLastPathComponent.js);
    var resDir = (argv && argv.length > 1 && argv[1])
        ? String(argv[1])
        : (appParent + "/ShareNet.app/Contents/Resources");

    var proxyPort = 8080;
    var githubURL = "https://github.com/TPpixel";
    var ca = Application.currentApplication();
    ca.includeStandardAdditions = true;

    function shellOut(cmd) {
        try {
            return String(ca.doShellScript(cmd) || "").trim();
        } catch (e) {
            return "";
        }
    }

    var singBoxPath = shellOut("[ -f '" + resDir + "/sing-box' ] && echo '" + resDir + "/sing-box' || echo '" + appParent + "/sing-box'");
    var cfgPath = "/tmp/hotspot-vpn-singbox.json";
    var logPath = "/tmp/hotspot-vpn-singbox.log";
    var pidPath = "/tmp/hotspot-vpn-singbox.pid";

    function shellTry(cmd) {
        try {
            return { ok: true, out: String(ca.doShellScript(cmd) || "").trim() };
        } catch (e) {
            return { ok: false, out: String(e) };
        }
    }

    function adminShell(cmd) {
        try {
            var out = ca.doShellScript(cmd, { administratorPrivileges: true });
            return { ok: true, out: String(out || "").trim() };
        } catch (e) {
            return { ok: false, out: String(e) };
        }
    }

    var state = {
        mode: "tun", // "tun" or "proxy"
        hasSingBox: shellOut("[ -f '" + singBoxPath + "' ] && echo yes") === "yes",
        connected: false,
        connectedMode: "",
        service: "Wi-Fi",
        gateway: "",
        reachable: false,
        logOffset: 0
    };
    if (!state.hasSingBox) state.mode = "proxy";

    function detectService() {
        var iface = shellOut("route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}'");
        if (iface && iface.indexOf("utun") !== 0) {
            var svc = shellOut("networksetup -listallhardwareports 2>/dev/null | awk -v dev='" + iface + "' '/Hardware Port:/{port=substr($0, index($0,$3))} $0 ~ \"Device: \"dev\"$\"{print port; exit}'");
            if (svc) return svc;
        }
        return "Wi-Fi";
    }

    function detectGateway(service) {
        var gw = shellOut("route -n get default 2>/dev/null | awk '/gateway:/{print $2; exit}'");
        if (gw && gw.indexOf("172.19.0.") !== 0) return gw;
        var router = shellOut("networksetup -getinfo '" + service.replace(/'/g, "") + "' 2>/dev/null | awk -F': ' '/^Router:/{print $2; exit}'");
        if (router && router !== "none") return router;
        return "";
    }

    function isValidIPv4(ip) {
        return /^(\d{1,3}\.){3}\d{1,3}$/.test(ip);
    }

    function makeGlassCard(frame, materialCode, radius) {
        var v = $.NSVisualEffectView.alloc.initWithFrame(frame);
        v.setMaterial(materialCode);
        v.setBlendingMode(1); // WithinWindow
        v.setState(1); // Active
        v.setWantsLayer(true);
        var layer = v.layer;
        layer.setCornerRadius(radius);
        layer.setMasksToBounds(true);
        layer.setBorderWidth(1.0);
        layer.setBorderColor($.NSColor.colorWithWhiteAlpha(1.0, 0.24).CGColor);
        return v;
    }

    function makeLabel(frame, text, size, weight, color, mono) {
        var tf = $.NSTextField.labelWithString(text);
        tf.setFrame(frame);
        if (mono) {
            tf.setFont($.NSFont.monospacedSystemFontOfSizeWeight(size, weight));
        } else {
            tf.setFont($.NSFont.systemFontOfSizeWeight(size, weight));
        }
        if (color) tf.setTextColor(color);
        return tf;
    }

    var winW = 600;
    var winH = 690;
    var styleMask = 1 | 2 | 4 | 32768; // Titled | Closable | Miniaturizable | FullSizeContentView
    var win = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
        $.NSMakeRect(0, 0, winW, winH),
        styleMask,
        $.NSBackingStoreBuffered,
        false
    );
    win.setTitle("ShareNet");
    win.setTitleVisibility(1); // Hidden
    win.setTitlebarAppearsTransparent(true);
    win.setMovableByWindowBackground(true);
    win.setOpaque(false);
    win.setBackgroundColor($.NSColor.clearColor);
    win.center;

    var rootBg = $.NSVisualEffectView.alloc.initWithFrame($.NSMakeRect(0, 0, winW, winH));
    rootBg.setMaterial(4); // UnderWindowBackground
    rootBg.setBlendingMode(0); // BehindWindow
    rootBg.setState(1);
    win.setContentView(rootBg);

    // Header
    var titleLbl = makeLabel($.NSMakeRect(22, 622, 260, 26), "ShareNet", 19, 0.4, $.NSColor.labelColor, false);
    var subLbl = makeLabel($.NSMakeRect(22, 602, 340, 18), "Liquid Glass · Интерфейс: Wi-Fi", 12, 0.0, $.NSColor.secondaryLabelColor, false);
    rootBg.addSubview(titleLbl);
    rootBg.addSubview(subLbl);

    var gwPill = makeGlassCard($.NSMakeRect(386, 602, 192, 46), 3, 13);
    var gwCap = makeLabel($.NSMakeRect(12, 25, 168, 14), "ШЛЮЗ ТЕЛЕФОНА", 9.5, 0.4, $.NSColor.secondaryLabelColor, false);
    gwCap.setAlignment(1); // Right
    var gwNum = makeLabel($.NSMakeRect(12, 6, 168, 18), "--.--.--.--", 13.5, 0.3, $.NSColor.labelColor, true);
    gwNum.setAlignment(1);
    gwPill.addSubview(gwCap);
    gwPill.addSubview(gwNum);
    rootBg.addSubview(gwPill);

    // Hero Card
    var heroCard = makeGlassCard($.NSMakeRect(20, 444, 560, 144), 13, 20);
    var statusLbl = makeLabel($.NSMakeRect(16, 108, 240, 20), "Поиск шлюза...", 13, 0.3, $.NSColor.systemOrangeColor, false);
    heroCard.addSubview(statusLbl);

    var modeSeg = $.NSSegmentedControl.segmentedControlWithLabelsTrackingModeTargetAction(
        $.NSArray.arrayWithArray(["Полный VPN (TUN)", "Прокси macOS"]),
        0, // SelectOne
        null,
        ""
    );
    modeSeg.setFrame($.NSMakeRect(262, 102, 282, 28));
    modeSeg.setSelectedSegment(state.mode === "tun" ? 0 : 1);
    heroCard.addSubview(modeSeg);

    var heroBtn = $.NSButton.buttonWithTitleTargetAction("Подключиться через телефон", null, "");
    heroBtn.setFrame($.NSMakeRect(16, 40, 528, 48));
    heroBtn.setBezelStyle(1);
    heroBtn.setWantsLayer(true);
    heroBtn.setKeyEquivalent("\r");
    heroCard.addSubview(heroBtn);

    var modeSub = makeLabel($.NSMakeRect(16, 14, 528, 18), "Весь трафик + DoH 1.1.1.1 (запросит пароль Mac)", 11.5, 0.0, $.NSColor.secondaryLabelColor, false);
    heroCard.addSubview(modeSub);
    rootBg.addSubview(heroCard);

    // Gateway & Actions Card
    var ctrlCard = makeGlassCard($.NSMakeRect(20, 326, 560, 104), 13, 18);
    var gwInput = $.NSTextField.alloc.initWithFrame($.NSMakeRect(16, 58, 344, 28));
    gwInput.setPlaceholderString("IP телефона (например 172.20.10.1)");
    gwInput.setFont($.NSFont.monospacedSystemFontOfSizeWeight(12.5, 0.2));
    ctrlCard.addSubview(gwInput);

    var refreshBtn = $.NSButton.buttonWithTitleTargetAction("Обновить", null, "");
    refreshBtn.setFrame($.NSMakeRect(370, 56, 88, 32));
    ctrlCard.addSubview(refreshBtn);

    var autoBtn = $.NSButton.buttonWithTitleTargetAction("Авто", null, "");
    autoBtn.setFrame($.NSMakeRect(464, 56, 80, 32));
    ctrlCard.addSubview(autoBtn);

    var checkBtn = $.NSButton.buttonWithTitleTargetAction("Проверить сеть", null, "");
    checkBtn.setFrame($.NSMakeRect(14, 14, 150, 32));
    ctrlCard.addSubview(checkBtn);

    var tgBtn = $.NSButton.buttonWithTitleTargetAction("Telegram (:8080)", null, "");
    tgBtn.setFrame($.NSMakeRect(172, 14, 148, 32));
    ctrlCard.addSubview(tgBtn);

    var copyBtn = $.NSButton.buttonWithTitleTargetAction("Копировать IP:8080", null, "");
    copyBtn.setFrame($.NSMakeRect(328, 14, 168, 32));
    ctrlCard.addSubview(copyBtn);
    rootBg.addSubview(ctrlCard);

    // Log Console Card
    var logCard = makeGlassCard($.NSMakeRect(20, 82, 560, 230), 13, 18);
    var logTitle = makeLabel($.NSMakeRect(16, 200, 220, 18), "Журнал работы", 12, 0.3, $.NSColor.secondaryLabelColor, false);
    logCard.addSubview(logTitle);

    var scroll = $.NSScrollView.alloc.initWithFrame($.NSMakeRect(16, 16, 528, 178));
    scroll.setHasVerticalScroller(true);
    scroll.setWantsLayer(true);
    scroll.layer.setCornerRadius(12);
    scroll.layer.setBackgroundColor($.NSColor.colorWithRedGreenBlueAlpha(0.051, 0.063, 0.090, 1.0).CGColor);

    var logView = $.NSTextView.alloc.initWithFrame($.NSMakeRect(0, 0, 512, 178));
    logView.setEditable(false);
    logView.setDrawsBackground(false);
    logView.setTextColor($.NSColor.colorWithWhiteAlpha(0.92, 1.0));
    logView.setFont($.NSFont.monospacedSystemFontOfSizeWeight(11.5, 0.0));
    scroll.setDocumentView(logView);
    logCard.addSubview(scroll);
    rootBg.addSubview(logCard);

    // Developer Footer Card
    var devCard = makeGlassCard($.NSMakeRect(20, 18, 560, 50), 3, 16);
    var devBtn = $.NSButton.buttonWithTitleTargetAction("Разработчик · TPpixel   —   Открыть github.com/TPpixel   ›", null, "");
    devBtn.setFrame($.NSMakeRect(12, 8, 536, 34));
    devBtn.setBordered(false);
    devBtn.setFont($.NSFont.systemFontOfSizeWeight(12.5, 0.3));
    devCard.addSubview(devBtn);
    rootBg.addSubview(devCard);

    function appendLog(msg) {
        if (!msg) return;
        var ts = shellOut("date +%H:%M:%S");
        var line = "[" + ts + "] " + msg + "\n";
        var cur = String(logView.string.js || "");
        logView.setString(cur + line);
        logView.scrollRangeToVisible($.NSMakeRange(logView.string.length, 0));
    }

    function updateUI() {
        subLbl.setStringValue("Интерфейс: " + state.service + (state.hasSingBox ? " · sing-box" : " · только прокси"));
        gwNum.setStringValue(state.gateway ? (state.gateway + ":" + proxyPort) : "--.--.--.--");
        if (state.connected) {
            statusLbl.setStringValue(state.connectedMode === "tun" ? "VPN-туннель активен" : "Системный прокси активен");
            statusLbl.setTextColor($.NSColor.systemGreenColor);
            heroBtn.setTitle("Отключить и восстановить сеть");
        } else if (state.reachable) {
            statusLbl.setStringValue("Телефон найден (:8080)");
            statusLbl.setTextColor($.NSColor.systemTealColor);
            heroBtn.setTitle("Подключиться через телефон");
        } else {
            statusLbl.setStringValue(state.gateway ? "Порт 8080 не отвечает" : "Подключитесь к Wi-Fi телефона");
            statusLbl.setTextColor($.NSColor.systemOrangeColor);
            heroBtn.setTitle("Подключиться через телефон");
        }
        modeSub.setStringValue(state.mode === "tun"
            ? "Весь трафик + DoH 1.1.1.1 (запросит пароль Mac)"
            : "SOCKS5 системный прокси для браузеров без пароля");
        modeSeg.setEnabled(!state.connected);
    }

    function refreshNetwork() {
        state.service = detectService();
        var manual = String(gwInput.stringValue.js || "").trim();
        var gw = manual ? manual : detectGateway(state.service);
        state.gateway = gw;
        if (!manual && gw) gwInput.setStringValue(gw);
        if (gw && isValidIPv4(gw)) {
            state.reachable = shellTry("nc -z -w 1 '" + gw + "' " + proxyPort).ok;
        } else {
            state.reachable = false;
        }
        if (state.connected && state.connectedMode === "tun") {
            if (!shellTry("pgrep -f '" + cfgPath + "'").ok) {
                state.connected = false;
                state.connectedMode = "";
            }
        }
        updateUI();
    }

    function disconnect() {
        var curDNS = shellOut("networksetup -getdnsservers '" + state.service + "'");
        var dnsStuck = curDNS.indexOf("172.19.0.2") !== -1;
        if (state.connectedMode === "tun" || shellTry("pgrep -f '" + cfgPath + "'").ok || dnsStuck) {
            appendLog("Останавливаю TUN-туннель и восстанавливаю DNS...");
            adminShell("pkill -INT -f '" + cfgPath + "' >/dev/null 2>&1 || true; sleep 0.5; networksetup -setdnsservers '" + state.service + "' Empty >/dev/null 2>&1 || true; dscacheutil -flushcache >/dev/null 2>&1 || true; killall -HUP mDNSResponder >/dev/null 2>&1 || true; rm -f '" + cfgPath + "' '" + pidPath + "'");
            appendLog("VPN остановлен, DNS восстановлен.");
        }
        if (state.connectedMode === "proxy") {
            shellTry("networksetup -setsocksfirewallproxystate '" + state.service + "' off");
            appendLog("Системный SOCKS5 прокси отключён.");
        }
        state.connected = false;
        state.connectedMode = "";
        updateUI();
    }

    function connect() {
        refreshNetwork();
        var gw = state.gateway;
        if (!gw || !isValidIPv4(gw)) {
            appendLog("Шлюз не обнаружен. Подключите Mac к точке доступа телефона.");
            return;
        }
        if (!state.reachable) {
            appendLog("Порт 8080 на " + gw + " не отвечает. Проверьте ShareNet на телефоне.");
            return;
        }
        if (state.mode === "proxy") {
            var res = shellTry("networksetup -setsocksfirewallproxy '" + state.service + "' '" + gw + "' " + proxyPort + " && networksetup -setsocksfirewallproxystate '" + state.service + "' on");
            if (!res.ok) {
                appendLog("Ошибка настройки прокси: " + res.out);
                return;
            }
            state.connected = true;
            state.connectedMode = "proxy";
            appendLog("Системный SOCKS5 включён -> " + gw + ":" + proxyPort);
            updateUI();
            return;
        }

        var cfg = {
            log: { level: "info", timestamp: true },
            dns: {
                servers: [{ tag: "doh_cf", type: "https", server: "1.1.1.1", server_port: 443, path: "/dns-query", detour: "proxy-out" }],
                final: "doh_cf",
                strategy: "ipv4_only"
            },
            inbounds: [{
                type: "tun",
                tag: "tun-in",
                address: ["172.19.0.1/30"],
                mtu: 1500,
                auto_route: true,
                strict_route: false,
                stack: "gvisor",
                dns_mode: "hijack",
                dns_address: ["172.19.0.2"]
            }],
            outbounds: [
                { type: "socks", tag: "proxy-out", server: gw, server_port: proxyPort, version: "5", network: "tcp" },
                { type: "direct", tag: "direct-out" }
            ],
            route: {
                rules: [
                    { action: "sniff" },
                    { port: [53], action: "hijack-dns" },
                    { protocol: "dns", action: "hijack-dns" },
                    { ip_is_private: true, outbound: "direct-out" },
                    { network: "udp", action: "reject" }
                ],
                final: "proxy-out",
                auto_detect_interface: true
            }
        };
        var jsonStr = JSON.stringify(cfg, null, 2).replace(/'/g, "'\\''");
        shellTry("printf '%s' '" + jsonStr + "' > '" + cfgPath + "' && : > '" + logPath + "'");
        shellTry("chmod +x '" + singBoxPath + "' && xattr -d com.apple.quarantine '" + singBoxPath + "' 2>/dev/null || true");
        var chk = shellTry("'" + singBoxPath + "' check -c '" + cfgPath + "'");
        if (!chk.ok) {
            appendLog("Ошибка конфига sing-box: " + chk.out);
            return;
        }

        shellTry("networksetup -setwebproxystate '" + state.service + "' off; networksetup -setsecurewebproxystate '" + state.service + "' off; networksetup -setsocksfirewallproxystate '" + state.service + "' off");
        var cmd = "pkill -INT -f '" + cfgPath + "' >/dev/null 2>&1 || true; sleep 0.2; " +
            "/bin/sh -c 'trap \"\" HUP; exec \"$1\" run -c \"$2\" </dev/null >\"$3\" 2>&1' _ '" + singBoxPath + "' '" + cfgPath + "' '" + logPath + "' & SB_PID=$!; echo \"$SB_PID\" >'" + pidPath + "'; " +
            "sleep 1.2; " +
            "if ! kill -0 \"$SB_PID\" 2>/dev/null; then exit 1; fi; " +
            "if ! networksetup -setdnsservers '" + state.service + "' 172.19.0.2; then kill -INT \"$SB_PID\" >/dev/null 2>&1 || true; exit 1; fi; " +
            "dscacheutil -flushcache >/dev/null 2>&1 || true; " +
            "killall -HUP mDNSResponder >/dev/null 2>&1 || true";
        var auth = adminShell(cmd);
        if (!auth.ok) {
            appendLog("Запуск TUN отменён или sing-box завершился: " + shellOut("tail -n 8 '" + logPath + "'"));
            return;
        }
        shellTry("sleep 0.5");
        if (!shellTry("pgrep -f '" + cfgPath + "'").ok) {
            appendLog("sing-box не запустился: " + shellOut("tail -n 8 '" + logPath + "'"));
            adminShell("networksetup -setdnsservers '" + state.service + "' Empty >/dev/null 2>&1 || true; dscacheutil -flushcache >/dev/null 2>&1 || true; killall -HUP mDNSResponder >/dev/null 2>&1 || true");
            return;
        }
        state.connected = true;
        state.connectedMode = "tun";
        appendLog("VPN-туннель активен (utun + DNS 172.19.0.2).");
        updateUI();
    }

    ObjC.registerSubclass({
        name: "HotspotDelegate",
        superclass: "NSObject",
        methods: {
            "onHero:": { types: ["void", ["id"]], implementation: function () { state.connected ? disconnect() : connect(); } },
            "onMode:": { types: ["void", ["id"]], implementation: function () { state.mode = modeSeg.selectedSegment === 0 && state.hasSingBox ? "tun" : "proxy"; modeSeg.setSelectedSegment(state.mode === "tun" ? 0 : 1); updateUI(); } },
            "onRefresh:": { types: ["void", ["id"]], implementation: function () { refreshNetwork(); appendLog("Сеть обновлена."); } },
            "onAuto:": { types: ["void", ["id"]], implementation: function () { gwInput.setStringValue(""); refreshNetwork(); } },
            "onCheck:": { types: ["void", ["id"]], implementation: function () {
                var proxyArg = state.connectedMode === "proxy" ? ("--socks5-hostname '" + state.gateway + ":" + proxyPort + "'") : "--noproxy '*'";
                var code = shellOut("curl " + proxyArg + " -s -m 6 -o /dev/null -w '%{http_code}' https://www.google.com/generate_204");
                appendLog("Проверка сети: HTTP " + (code || "000"));
            } },
            "onTg:": { types: ["void", ["id"]], implementation: function () { if (state.gateway) shellTry("open 'tg://socks?server=" + state.gateway + "&port=" + proxyPort + "'"); } },
            "onCopy:": { types: ["void", ["id"]], implementation: function () { if (state.gateway) { var pb = $.NSPasteboard.generalPasteboard; pb.clearContents; pb.setStringForType(state.gateway + ":" + proxyPort, $.NSPasteboardTypeString); appendLog("Скопировано: " + state.gateway + ":" + proxyPort); } } },
            "onGitHub:": { types: ["void", ["id"]], implementation: function () { $.NSWorkspace.sharedWorkspace.openURL($.NSURL.URLWithString(githubURL)); } },
            "applicationWillTerminate:": { types: ["void", ["id"]], implementation: function () { if (state.connected) disconnect(); } },
            "applicationShouldTerminateAfterLastWindowClosed:": { types: ["bool", ["id"]], implementation: function () { return true; } }
        }
    });

    var del = $.HotspotDelegate.alloc.init;
    app.setDelegate(del);

    heroBtn.setTarget(del); heroBtn.setAction("onHero:");
    modeSeg.setTarget(del); modeSeg.setAction("onMode:");
    refreshBtn.setTarget(del); refreshBtn.setAction("onRefresh:");
    autoBtn.setTarget(del); autoBtn.setAction("onAuto:");
    checkBtn.setTarget(del); checkBtn.setAction("onCheck:");
    tgBtn.setTarget(del); tgBtn.setAction("onTg:");
    copyBtn.setTarget(del); copyBtn.setAction("onCopy:");
    devBtn.setTarget(del); devBtn.setAction("onGitHub:");

    appendLog("Готово (Liquid Glass Cocoa). Разработчик: TPpixel.");
    refreshNetwork();

    win.makeKeyAndOrderFront(null);
    app.activateIgnoringOtherApps(true);
    app.run;
}
