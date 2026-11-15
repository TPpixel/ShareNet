import AppKit
import SwiftUI

private let proxyPort = 8080
private let githubURL = "https://github.com/TPpixel/ShareNet"
private let singBoxConfigPath = "/tmp/hotspot-vpn-singbox.json"
private let singBoxLogPath = "/tmp/hotspot-vpn-singbox.log"
private let singBoxPidPath = "/tmp/hotspot-vpn-singbox.pid"

enum ConnectionMode: String, CaseIterable, Identifiable {
    case tun
    case systemProxy

    var id: String { rawValue }
    var title: String {
        switch self {
        case .tun: return "Полный VPN (TUN)"
        case .systemProxy: return "Системный прокси"
        }
    }
    var subtitle: String {
        switch self {
        case .tun: return "Весь трафик + DoH 1.1.1.1 (нужен пароль Mac)"
        case .systemProxy: return "SOCKS5 для браузеров без пароля"
        }
    }
}

struct LogLine: Identifiable, Equatable {
    let id = UUID()
    let timestamp: String
    let message: String
}

final class ShellRunner {
    @discardableResult
    static func run(_ executable: String, args: [String] = [], timeout: TimeInterval = 8) -> (code: Int32, out: String) {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: executable)
        proc.arguments = args

        let pipe = Pipe()
        proc.standardOutput = pipe
        proc.standardError = pipe

        do {
            try proc.run()
        } catch {
            return (-1, error.localizedDescription)
        }

        let deadline = Date().addingTimeInterval(timeout)
        while proc.isRunning && Date() < deadline {
            Thread.sleep(forTimeInterval: 0.03)
        }
        if proc.isRunning {
            proc.terminate()
            return (-1, "timeout")
        }

        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let text = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return (proc.terminationStatus, text)
    }

    static func shellQuote(_ s: String) -> String {
        "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    static func runAdminShell(_ script: String) -> (ok: Bool, out: String) {
        let escaped = script
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
        let source = "do shell script \"\(escaped)\" with administrator privileges"
        guard let appleScript = NSAppleScript(source: source) else {
            return (false, "Не удалось инициализировать NSAppleScript")
        }
        var errInfo: NSDictionary?
        let result = appleScript.executeAndReturnError(&errInfo)
        if let errInfo = errInfo {
            let msg = (errInfo[NSAppleScript.errorMessage] as? String) ?? "Операция отменена"
            return (false, msg)
        }
        return (true, result.stringValue ?? "")
    }

    static func probeTCP(host: String, port: Int, timeoutMs: Int32 = 1200) -> Bool {
        guard !host.isEmpty else { return false }
        var addr = sockaddr_in()
        addr.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = UInt16(port).bigEndian

        let converted = host.withCString { cstr in
            inet_pton(AF_INET, cstr, &addr.sin_addr)
        }
        if converted != 1 { return false }

        let fd = Darwin.socket(AF_INET, SOCK_STREAM, 0)
        if fd < 0 { return false }
        defer { Darwin.close(fd) }

        let flags = fcntl(fd, F_GETFL, 0)
        _ = fcntl(fd, F_SETFL, flags | O_NONBLOCK)

        var sockAddr = addr
        let res = withUnsafePointer(to: &sockAddr) { ptr -> Int32 in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sa in
                Darwin.connect(fd, sa, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        if res == 0 { return true }
        if errno != EINPROGRESS { return false }

        var pfd = pollfd(fd: fd, events: Int16(POLLOUT), revents: 0)
        let pollRes = Darwin.poll(&pfd, 1, timeoutMs)
        if pollRes <= 0 { return false }

        var soError: Int32 = 0
        var len = socklen_t(MemoryLayout<Int32>.size)
        getsockopt(fd, SOL_SOCKET, SO_ERROR, &soError, &len)
        return soError == 0
    }
}

final class VPNViewModel: ObservableObject {
    @Published var gateway: String = ""
    @Published var manualGateway: String = ""
    @Published var networkService: String = "Wi-Fi"
    @Published var gatewayReachable: Bool = false
    @Published var isConnected: Bool = false
    @Published var isBusy: Bool = false
    @Published var mode: ConnectionMode = .tun
    @Published var hasSingBox: Bool = false
    @Published var checkStatus: String = "Порт 8080 · DoH 1.1.1.1"
    @Published var logs: [LogLine] = []

    private var activeMode: ConnectionMode?
    private var logOffset: UInt64 = 0
    private var pollTimer: Timer?
    private let workQueue = DispatchQueue(label: "com.tppixel.hotspotvpn.work", qos: .userInitiated)
    private let singBoxPath: String

    init() {
        self.singBoxPath = VPNViewModel.resolveSingBoxPath()
        self.hasSingBox = FileManager.default.isExecutableFile(atPath: singBoxPath) || FileManager.default.fileExists(atPath: singBoxPath)
        if !self.hasSingBox {
            self.mode = .systemProxy
            appendLog("Бинарник sing-box не найден; доступен режим системного прокси.")
        } else {
            appendLog("Готово. Подключите Mac к точке доступа телефона.")
        }
        refreshNetwork()
        pollTimer = Timer.scheduledTimer(withTimeInterval: 2.5, repeats: true) { [weak self] _ in
            self?.tick()
        }
    }

    static func resolveSingBoxPath() -> String {
        var candidates: [String] = []
        if let envRes = ProcessInfo.processInfo.environment["HOTSPOTVPN_RESOURCES"], !envRes.isEmpty {
            candidates.append(URL(fileURLWithPath: envRes).appendingPathComponent("sing-box").path)
        }
        if let resURL = Bundle.main.resourceURL {
            candidates.append(resURL.appendingPathComponent("sing-box").path)
        }
        candidates.append(URL(fileURLWithPath: resolveAppParentDir()).appendingPathComponent("sing-box").path)
        for p in candidates where FileManager.default.fileExists(atPath: p) {
            return p
        }
        return candidates.first ?? "sing-box"
    }

    static func resolveAppParentDir() -> String {
        let bundleURL = Bundle.main.bundleURL
        if bundleURL.pathExtension == "app" {
            return bundleURL.deletingLastPathComponent().path
        }
        return FileManager.default.currentDirectoryPath
    }

    func appendLog(_ text: String) {
        let clean = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !clean.isEmpty else { return }
        let df = DateFormatter()
        df.dateFormat = "HH:mm:ss"
        let item = LogLine(timestamp: df.string(from: Date()), message: clean)
        DispatchQueue.main.async {
            self.logs.append(item)
            if self.logs.count > 220 {
                self.logs.removeFirst(self.logs.count - 220)
            }
        }
    }

    private func tick() {
        tailSingBoxLog()
        refreshNetwork()
    }

    func refreshNetwork() {
        workQueue.async {
            let service = self.detectActiveService()
            let autoGW = self.detectGateway(service: service)
            let chosenGW = self.manualGateway.isEmpty ? autoGW : self.manualGateway
            let reachable = ShellRunner.probeTCP(host: chosenGW, port: proxyPort, timeoutMs: 850)
            let tunAlive = self.isSingBoxRunning()

            DispatchQueue.main.async {
                self.networkService = service
                self.gateway = chosenGW
                self.gatewayReachable = reachable
                if self.activeMode == .tun && !tunAlive && !self.isBusy {
                    self.isConnected = false
                    self.activeMode = nil
                }
            }
        }
    }

    private func detectActiveService() -> String {
        let ifaceRes = ShellRunner.run("/sbin/route", args: ["-n", "get", "default"], timeout: 3)
        var iface = ""
        for line in ifaceRes.out.components(separatedBy: "\n") {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.hasPrefix("interface:") {
                iface = trimmed.replacingOccurrences(of: "interface:", with: "").trimmingCharacters(in: .whitespaces)
                break
            }
        }
        if !iface.isEmpty && !iface.hasPrefix("utun") {
            let ports = ShellRunner.run("/usr/sbin/networksetup", args: ["-listallhardwareports"], timeout: 3).out
            var currentPort = ""
            for line in ports.components(separatedBy: "\n") {
                if line.hasPrefix("Hardware Port:") {
                    currentPort = line.replacingOccurrences(of: "Hardware Port:", with: "").trimmingCharacters(in: .whitespaces)
                } else if line.contains("Device: \(iface)") && !currentPort.isEmpty {
                    return currentPort
                }
            }
        }
        return "Wi-Fi"
    }

    private func detectGateway(service: String) -> String {
        let routeRes = ShellRunner.run("/sbin/route", args: ["-n", "get", "default"], timeout: 3).out
        for line in routeRes.components(separatedBy: "\n") {
            let t = line.trimmingCharacters(in: .whitespaces)
            if t.hasPrefix("gateway:") {
                let gw = t.replacingOccurrences(of: "gateway:", with: "").trimmingCharacters(in: .whitespaces)
                if !gw.isEmpty && !gw.hasPrefix("172.19.0.") {
                    return gw
                }
            }
        }
        let info = ShellRunner.run("/usr/sbin/networksetup", args: ["-getinfo", service], timeout: 3).out
        for line in info.components(separatedBy: "\n") {
            if line.hasPrefix("Router:") {
                let gw = line.replacingOccurrences(of: "Router:", with: "").trimmingCharacters(in: .whitespaces)
                if !gw.isEmpty && gw != "none" {
                    return gw
                }
            }
        }
        return ""
    }

    private func isSingBoxRunning() -> Bool {
        let res = ShellRunner.run("/usr/bin/pgrep", args: ["-f", singBoxConfigPath], timeout: 2)
        return res.code == 0 && !res.out.isEmpty
    }

    func toggleConnection() {
        if isBusy { return }
        if isConnected {
            disconnect()
        } else {
            connect()
        }
    }

    func connect() {
        let targetGW = gateway.trimmingCharacters(in: .whitespacesAndNewlines)
        let targetService = networkService
        let chosenMode = mode

        guard !targetGW.isEmpty else {
            appendLog("Шлюз телефона не найден. Подключите Mac к Wi-Fi точке доступа.")
            return
        }

        DispatchQueue.main.async { self.isBusy = true }
        workQueue.async {
            defer { DispatchQueue.main.async { self.isBusy = false } }

            self.appendLog("Проверка SOCKS5 на \(targetGW):\(proxyPort)...")
            guard ShellRunner.probeTCP(host: targetGW, port: proxyPort, timeoutMs: 1600) else {
                self.appendLog("Порт \(proxyPort) на \(targetGW) не отвечает. Проверьте ShareNet на телефоне.")
                return
            }

            self.cleanupDeadRelayEnv()

            if chosenMode == .tun {
                self.startTun(gateway: targetGW, service: targetService)
            } else {
                self.startSystemProxy(gateway: targetGW, service: targetService)
            }
        }
    }

    private func cleanupDeadRelayEnv() {
        let listen = ShellRunner.run("/usr/sbin/lsof", args: ["-ti", "tcp:8899", "-sTCP:LISTEN"], timeout: 2)
        if listen.code == 0 && !listen.out.isEmpty {
            return
        }
        for key in ["AG_LS_PROXY", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "https_proxy", "http_proxy", "all_proxy"] {
            let val = ShellRunner.run("/bin/launchctl", args: ["getenv", key], timeout: 2).out
            if val.contains("127.0.0.1:8899") {
                _ = ShellRunner.run("/bin/launchctl", args: ["unsetenv", key], timeout: 2)
                appendLog("Очищена неактивная переменная \(key).")
            }
        }
    }

    private func startTun(gateway: String, service: String) {
        let cfgObject: [String: Any] = [
            "log": ["level": "info", "timestamp": true],
            "dns": [
                "servers": [[
                    "tag": "doh_cf",
                    "type": "https",
                    "server": "1.1.1.1",
                    "server_port": 443,
                    "path": "/dns-query",
                    "detour": "proxy-out"
                ]],
                "final": "doh_cf",
                "strategy": "ipv4_only"
            ],
            "inbounds": [[
                "type": "tun",
                "tag": "tun-in",
                "address": ["172.19.0.1/30"],
                "mtu": 1500,
                "auto_route": true,
                "strict_route": false,
                "stack": "gvisor",
                "dns_mode": "hijack",
                "dns_address": ["172.19.0.2"]
            ]],
            "outbounds": [
                [
                    "type": "socks",
                    "tag": "proxy-out",
                    "server": gateway,
                    "server_port": proxyPort,
                    "version": "5",
                    "network": "tcp"
                ],
                [
                    "type": "direct",
                    "tag": "direct-out"
                ]
            ],
            "route": [
                "rules": [
                    ["action": "sniff"],
                    ["port": [53], "action": "hijack-dns"],
                    ["protocol": "dns", "action": "hijack-dns"],
                    ["ip_is_private": true, "outbound": "direct-out"],
                    ["network": "udp", "action": "reject"]
                ],
                "final": "proxy-out",
                "auto_detect_interface": true
            ]
        ]

        guard let jsonData = try? JSONSerialization.data(withJSONObject: cfgObject, options: [.prettyPrinted]) else {
            appendLog("Не удалось сформировать конфиг sing-box.")
            return
        }
        try? jsonData.write(to: URL(fileURLWithPath: singBoxConfigPath))
        try? "".write(toFile: singBoxLogPath, atomically: true, encoding: .utf8)
        logOffset = 0

        _ = ShellRunner.run("/bin/chmod", args: ["+x", singBoxPath], timeout: 2)
        _ = ShellRunner.run("/usr/bin/xattr", args: ["-d", "com.apple.quarantine", singBoxPath], timeout: 2)

        let check = ShellRunner.run(singBoxPath, args: ["check", "-c", singBoxConfigPath], timeout: 5)
        if check.code != 0 {
            appendLog("Ошибка конфигурации sing-box: \(check.out)")
            return
        }

        _ = ShellRunner.run("/usr/sbin/networksetup", args: ["-setwebproxystate", service, "off"], timeout: 2)
        _ = ShellRunner.run("/usr/sbin/networksetup", args: ["-setsecurewebproxystate", service, "off"], timeout: 2)
        _ = ShellRunner.run("/usr/sbin/networksetup", args: ["-setsocksfirewallproxystate", service, "off"], timeout: 2)

        let dnsOut = ShellRunner.run("/usr/sbin/networksetup", args: ["-getdnsservers", service], timeout: 3).out
        let validDNSChars = CharacterSet(charactersIn: "0123456789abcdefABCDEF.:")
        var backupDNS = dnsOut
            .components(separatedBy: "\n")
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty && !$0.hasPrefix("There aren't") && $0 != "172.19.0.2" && $0.unicodeScalars.allSatisfy { validDNSChars.contains($0) } }
        if backupDNS.isEmpty { backupDNS = ["Empty"] }
        let backupArgs = backupDNS.map { ShellRunner.shellQuote($0) }.joined(separator: " ")

        let qCfg = ShellRunner.shellQuote(singBoxConfigPath)
        let qBin = ShellRunner.shellQuote(singBoxPath)
        let qLog = ShellRunner.shellQuote(singBoxLogPath)
        let qPid = ShellRunner.shellQuote(singBoxPidPath)
        let qSvc = ShellRunner.shellQuote(service)

        let cmd = [
            "pkill -INT -f \(qCfg) >/dev/null 2>&1 || true",
            "sleep 0.2",
            "/bin/sh -c 'trap \"\" HUP; exec \"$1\" run -c \"$2\" </dev/null >\"$3\" 2>&1' _ \(qBin) \(qCfg) \(qLog) & SB_PID=$!",
            "echo \"$SB_PID\" >\(qPid)",
            "sleep 1.2",
            "if ! kill -0 \"$SB_PID\" 2>/dev/null; then exit 1; fi",
            "if ! networksetup -setdnsservers \(qSvc) 172.19.0.2; then kill -INT \"$SB_PID\" >/dev/null 2>&1 || true; exit 1; fi",
            "dscacheutil -flushcache >/dev/null 2>&1 || true",
            "killall -HUP mDNSResponder >/dev/null 2>&1 || true",
            "/bin/sh -c 'trap \"\" HUP; SB=\"$1\"; APP=\"$2\"; SVC=\"$3\"; CFG=\"$4\"; PIDF=\"$5\"; shift 5; while kill -0 \"$SB\" 2>/dev/null && kill -0 \"$APP\" 2>/dev/null; do sleep 1; done; kill -INT \"$SB\" >/dev/null 2>&1 || true; sleep 0.5; kill -TERM \"$SB\" >/dev/null 2>&1 || true; networksetup -setdnsservers \"$SVC\" \"$@\" >/dev/null 2>&1 || true; dscacheutil -flushcache >/dev/null 2>&1 || true; killall -HUP mDNSResponder >/dev/null 2>&1 || true; rm -f \"$CFG\" \"$PIDF\"' _ \"$SB_PID\" \(ProcessInfo.processInfo.processIdentifier) \(qSvc) \(qCfg) \(qPid) \(backupArgs) </dev/null >/dev/null 2>&1 &"
        ].joined(separator: "; ")

        appendLog("Запрашиваю права macOS для создания TUN-туннеля...")
        let auth = ShellRunner.runAdminShell(cmd)
        if !auth.ok {
            tailSingBoxLog()
            appendLog("Запуск TUN не выполнен: \(auth.out)")
            return
        }

        var up = false
        for _ in 0..<10 {
            Thread.sleep(forTimeInterval: 0.35)
            tailSingBoxLog()
            if isSingBoxRunning() {
                up = true
                break
            }
        }
        if !up {
            appendLog("sing-box не запустился. Восстанавливаю DNS...")
            let restoreCmd = "networksetup -setdnsservers \(qSvc) \(backupArgs) >/dev/null 2>&1 || true; dscacheutil -flushcache >/dev/null 2>&1 || true; killall -HUP mDNSResponder >/dev/null 2>&1 || true"
            _ = ShellRunner.runAdminShell(restoreCmd)
            return
        }

        DispatchQueue.main.async {
            self.activeMode = .tun
            self.isConnected = true
        }
        appendLog("VPN-туннель (utun + DNS 172.19.0.2) активен.")
        testConnectivity()
    }

    private func startSystemProxy(gateway: String, service: String) {
        let r1 = ShellRunner.run("/usr/sbin/networksetup", args: ["-setsocksfirewallproxy", service, gateway, "\(proxyPort)"], timeout: 4)
        let r2 = ShellRunner.run("/usr/sbin/networksetup", args: ["-setsocksfirewallproxystate", service, "on"], timeout: 4)
        if r1.code != 0 || r2.code != 0 {
            appendLog("Ошибка настройки SOCKS5 для \(service): \(r1.out) \(r2.out)")
            return
        }
        DispatchQueue.main.async {
            self.activeMode = .systemProxy
            self.isConnected = true
        }
        appendLog("Системный SOCKS5 прокси включён для «\(service)» -> \(gateway):\(proxyPort).")
        testConnectivity()
    }

    func disconnect(sync: Bool = false) {
        let curActive = activeMode
        let targetService = networkService
        let block = {
            let currentDNS = ShellRunner.run("/usr/sbin/networksetup", args: ["-getdnsservers", targetService], timeout: 3).out
            let dnsStuck = currentDNS.contains("172.19.0.2")
            if curActive == .tun || self.isSingBoxRunning() || dnsStuck {
                self.appendLog("Останавливаю TUN-туннель и восстанавливаю DNS...")
                let qCfg = ShellRunner.shellQuote(singBoxConfigPath)
                let qPid = ShellRunner.shellQuote(singBoxPidPath)
                let qSvc = ShellRunner.shellQuote(targetService)
                let cmd = [
                    "pkill -INT -f \(qCfg) >/dev/null 2>&1 || true",
                    "sleep 0.5",
                    "networksetup -setdnsservers \(qSvc) Empty >/dev/null 2>&1 || true",
                    "dscacheutil -flushcache >/dev/null 2>&1 || true",
                    "killall -HUP mDNSResponder >/dev/null 2>&1 || true",
                    "rm -f \(qCfg) \(qPid)"
                ].joined(separator: "; ")
                let res = ShellRunner.runAdminShell(cmd)
                if !res.ok {
                    self.appendLog("Предупреждение при остановке: \(res.out)")
                } else {
                    self.appendLog("VPN выключен, DNS «\(targetService)» восстановлен.")
                }
            }
            if curActive == .systemProxy {
                _ = ShellRunner.run("/usr/sbin/networksetup", args: ["-setsocksfirewallproxystate", targetService, "off"], timeout: 4)
                self.appendLog("Системный SOCKS5 прокси для «\(targetService)» отключён.")
            }
            DispatchQueue.main.async {
                self.isConnected = false
                self.activeMode = nil
                self.isBusy = false
                self.checkStatus = "Порт 8080 · DoH 1.1.1.1"
            }
        }

        if sync {
            block()
        } else {
            DispatchQueue.main.async { self.isBusy = true }
            workQueue.async(execute: block)
        }
    }

    func testConnectivity() {
        let gw = gateway
        let curMode = activeMode ?? mode
        workQueue.async {
            let start = Date()
            var args = ["-s", "-m", "7", "-o", "/dev/null", "-w", "%{http_code}"]
            if curMode == .systemProxy && !gw.isEmpty {
                args.append(contentsOf: ["--socks5-hostname", "\(gw):\(proxyPort)"])
            } else {
                args.append(contentsOf: ["--noproxy", "*"])
            }
            args.append("https://www.google.com/generate_204")

            let res = ShellRunner.run("/usr/bin/curl", args: args, timeout: 9)
            let ms = Int(Date().timeIntervalSince(start) * 1000)
            let code = res.out.trimmingCharacters(in: .whitespacesAndNewlines)
            DispatchQueue.main.async {
                if code == "204" || code == "200" {
                    self.checkStatus = "Сеть OK · HTTP \(code) · \(ms) мс"
                    self.appendLog("Проверка сети пройдена: HTTP \(code) (\(ms) мс).")
                } else {
                    self.checkStatus = "Нет ответа (HTTP \(code.isEmpty ? "000" : code))"
                    self.appendLog("Проверка сети: HTTP \(code.isEmpty ? "000" : code).")
                }
            }
        }
    }

    func copyGateway() {
        guard !gateway.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString("\(gateway):\(proxyPort)", forType: .string)
        appendLog("Скопировано в буфер: \(gateway):\(proxyPort)")
    }

    func openTelegram() {
        guard !gateway.isEmpty, let url = URL(string: "tg://socks?server=\(gateway)&port=\(proxyPort)") else { return }
        NSWorkspace.shared.open(url)
        appendLog("Открыта настройка прокси Telegram (\(gateway):\(proxyPort)).")
    }

    func openGitHub() {
        guard let url = URL(string: githubURL) else { return }
        NSWorkspace.shared.open(url)
    }

    private func tailSingBoxLog() {
        guard activeMode == .tun || isBusy else { return }
        guard let fh = FileHandle(forReadingAtPath: singBoxLogPath) else { return }
        defer { try? fh.close() }
        let fileSize = fh.seekToEndOfFile()
        guard fileSize > logOffset else { return }
        fh.seek(toFileOffset: logOffset)
        let data = fh.readData(ofLength: Int(min(16384, fileSize - logOffset)))
        logOffset += UInt64(data.count)
        guard let chunk = String(data: data, encoding: .utf8) else { return }
        for line in chunk.components(separatedBy: "\n") where !line.trimmingCharacters(in: .whitespaces).isEmpty {
            appendLog("[sing-box] \(line)")
        }
    }
}

struct VisualEffectGlass: NSViewRepresentable {
    var material: NSVisualEffectView.Material
    var blendingMode: NSVisualEffectView.BlendingMode = .withinWindow

    func makeNSView(context: Context) -> NSVisualEffectView {
        let v = NSVisualEffectView()
        v.material = material
        v.blendingMode = blendingMode
        v.state = .active
        return v
    }

    func updateNSView(_ nsView: NSVisualEffectView, context: Context) {
        nsView.material = material
        nsView.blendingMode = blendingMode
    }
}

struct LiquidGlassSurface: ViewModifier {
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency
    var cornerRadius: CGFloat
    var material: NSVisualEffectView.Material = .hudWindow

    func body(content: Content) -> some View {
        #if compiler(>=6.2)
        if #available(macOS 26.0, *), !reduceTransparency {
            content
                .glassEffect(.regular, in: .rect(cornerRadius: cornerRadius))
        } else {
            fallbackSurface(content)
        }
        #else
        fallbackSurface(content)
        #endif
    }

    @ViewBuilder
    private func fallbackSurface(_ content: Content) -> some View {
        if reduceTransparency {
            content
                .background(
                    RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                        .fill(Color(nsColor: .controlBackgroundColor))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                        .strokeBorder(Color.primary.opacity(0.14), lineWidth: 1)
                )
        } else {
            content
                .background(
                    VisualEffectGlass(material: material, blendingMode: .withinWindow)
                        .clipShape(RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                        .strokeBorder(
                            LinearGradient(
                                colors: [Color.white.opacity(0.32), Color.white.opacity(0.06)],
                                startPoint: .topLeading,
                                endPoint: .bottomTrailing
                            ),
                            lineWidth: 1
                        )
                )
                .shadow(color: Color.black.opacity(0.14), radius: 14, x: 0, y: 6)
        }
    }
}

extension View {
    func liquidGlassCard(cornerRadius: CGFloat = 18, material: NSVisualEffectView.Material = .hudWindow) -> some View {
        modifier(LiquidGlassSurface(cornerRadius: cornerRadius, material: material))
    }
}

struct HotspotContentView: View {
    @ObservedObject var vm: VPNViewModel
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    var body: some View {
        ZStack {
            if !reduceTransparency {
                VisualEffectGlass(material: .underWindowBackground, blendingMode: .behindWindow)
                    .ignoresSafeArea()
            } else {
                Color(nsColor: .windowBackgroundColor).ignoresSafeArea()
            }

            VStack(spacing: 12) {
                headerRow

                heroCard

                gatewayCard

                logCard

                developerFooter
            }
            .padding(.top, 38)
            .padding(.horizontal, 18)
            .padding(.bottom, 16)
        }
        .frame(minWidth: 560, minHeight: 680)
    }

    private var headerRow: some View {
        HStack(spacing: 12) {
            ZStack {
                Circle()
                    .fill(vm.isConnected ? Color.green.opacity(0.20) : Color.accentColor.opacity(0.16))
                    .frame(width: 38, height: 38)
                Image(systemName: vm.isConnected ? "lock.shield.fill" : "dot.radiowaves.left.and.right")
                    .font(.system(size: 16, weight: .semibold))
                    .foregroundStyle(vm.isConnected ? Color.green : Color.accentColor)
            }

            VStack(alignment: .leading, spacing: 2) {
                Text("ShareNet")
                    .font(.system(size: 18, weight: .semibold, design: .rounded))
                Text("Интерфейс: \(vm.networkService) · \(vm.checkStatus)")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            }

            Spacer()

            VStack(alignment: .trailing, spacing: 2) {
                Text("ШЛЮЗ")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(.secondary)
                Text(vm.gateway.isEmpty ? "--.--.--.--" : "\(vm.gateway):\(proxyPort)")
                    .font(.system(size: 14, weight: .semibold, design: .monospaced))
                    .monospacedDigit()
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
            .liquidGlassCard(cornerRadius: 12, material: .headerView)
        }
    }

    private var heroCard: some View {
        VStack(spacing: 12) {
            HStack {
                HStack(spacing: 7) {
                    Circle()
                        .fill(statusColor)
                        .frame(width: 8, height: 8)
                    Text(statusTitle)
                        .font(.system(size: 13, weight: .semibold))
                }
                .padding(.horizontal, 11)
                .padding(.vertical, 5)
                .background(statusColor.opacity(0.16), in: Capsule())

                Spacer()

                Picker("Режим", selection: $vm.mode) {
                    ForEach(ConnectionMode.allCases) { item in
                        Text(item.title).tag(item)
                    }
                }
                .pickerStyle(.segmented)
                .disabled(vm.isConnected || (!vm.hasSingBox && vm.mode == .tun))
                .frame(maxWidth: 310)
            }

            Button(action: {
                if reduceMotion {
                    vm.toggleConnection()
                } else {
                    withAnimation(.spring(response: 0.36, dampingFraction: 0.82)) {
                        vm.toggleConnection()
                    }
                }
            }) {
                HStack(spacing: 10) {
                    Image(systemName: vm.isConnected ? "stop.circle.fill" : "play.circle.fill")
                        .font(.system(size: 18, weight: .semibold))
                    Text(vm.isBusy ? "Выполняется..." : (vm.isConnected ? "Отключить и восстановить сеть" : "Подключиться через телефон"))
                        .font(.system(size: 15, weight: .semibold))
                }
                .frame(maxWidth: .infinity)
                .frame(height: 50)
                .foregroundStyle(.white)
                .background(
                    RoundedRectangle(cornerRadius: vm.isConnected ? 16 : 25, style: .continuous)
                        .fill(vm.isConnected ? Color.red.opacity(0.86) : Color.accentColor)
                )
            }
            .buttonStyle(.plain)
            .disabled(vm.isBusy)

            Text(vm.mode.subtitle)
                .font(.system(size: 11.5))
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(14)
        .liquidGlassCard(cornerRadius: 20)
    }

    private var gatewayCard: some View {
        VStack(spacing: 10) {
            HStack(spacing: 8) {
                TextField("IP телефона (например 172.20.10.1 или 192.168.43.1)", text: $vm.manualGateway)
                    .textFieldStyle(.roundedBorder)
                    .font(.system(size: 13, design: .monospaced))
                    .onSubmit { vm.refreshNetwork() }

                Button("Обновить") { vm.refreshNetwork() }
                    .keyboardShortcut("r", modifiers: .command)

                Button("Авто") {
                    vm.manualGateway = ""
                    vm.refreshNetwork()
                }
            }

            HStack(spacing: 8) {
                Button("Проверить сеть") { vm.testConnectivity() }
                Button("Telegram (:8080)") { vm.openTelegram() }
                    .disabled(vm.gateway.isEmpty)
                Button("Копировать IP:8080") { vm.copyGateway() }
                    .disabled(vm.gateway.isEmpty)
                Spacer()
            }
        }
        .padding(14)
        .liquidGlassCard(cornerRadius: 18)
    }

    private var logCard: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Журнал работы")
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(.secondary)

            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 4) {
                        ForEach(vm.logs) { line in
                            HStack(alignment: .top, spacing: 8) {
                                Text(line.timestamp)
                                    .font(.system(size: 11, design: .monospaced))
                                    .foregroundStyle(Color.white.opacity(0.50))
                                Text(line.message)
                                    .font(.system(size: 11.5, design: .monospaced))
                                    .foregroundStyle(Color.white.opacity(0.92))
                                    .textSelection(.enabled)
                            }
                            .id(line.id)
                        }
                    }
                    .padding(10)
                }
                .onChange(of: vm.logs.count) { _ in
                    if let last = vm.logs.last {
                        proxy.scrollTo(last.id, anchor: .bottom)
                    }
                }
            }
            .frame(minHeight: 170)
            .background(Color(red: 0.051, green: 0.063, blue: 0.090), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 12, style: .continuous)
                    .strokeBorder(Color.white.opacity(0.10), lineWidth: 1)
            )
        }
        .padding(14)
        .liquidGlassCard(cornerRadius: 18)
    }

    private var developerFooter: some View {
        Button(action: { vm.openGitHub() }) {
            HStack(spacing: 10) {
                GitHubLogoShape()
                    .fill(.primary)
                    .frame(width: 18, height: 18)
                VStack(alignment: .leading, spacing: 1) {
                    Text("Разработчик · TPpixel")
                        .font(.system(size: 13, weight: .semibold))
                    Text("Открыть репозиторий GitHub (github.com/TPpixel/ShareNet)")
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Image(systemName: "chevron.right")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .liquidGlassCard(cornerRadius: 16, material: .headerView)
    }

    private struct GitHubLogoShape: Shape {
        func path(in rect: CGRect) -> Path {
            let s = min(rect.width, rect.height) / 16.0
            var path = Path()
            path.move(to: CGPoint(x: 8.00 * s, y: 0.00 * s))
            path.addCurve(to: CGPoint(x: 0.00 * s, y: 8.00 * s), control1: CGPoint(x: 3.58 * s, y: 0.00 * s), control2: CGPoint(x: 0.00 * s, y: 3.58 * s))
            path.addCurve(to: CGPoint(x: 5.47 * s, y: 15.59 * s), control1: CGPoint(x: 0.00 * s, y: 11.54 * s), control2: CGPoint(x: 2.29 * s, y: 14.53 * s))
            path.addCurve(to: CGPoint(x: 6.02 * s, y: 15.21 * s), control1: CGPoint(x: 5.87 * s, y: 15.66 * s), control2: CGPoint(x: 6.02 * s, y: 15.42 * s))
            path.addCurve(to: CGPoint(x: 6.01 * s, y: 13.72 * s), control1: CGPoint(x: 6.02 * s, y: 15.02 * s), control2: CGPoint(x: 6.01 * s, y: 14.39 * s))
            path.addCurve(to: CGPoint(x: 3.32 * s, y: 12.78 * s), control1: CGPoint(x: 4.00 * s, y: 14.09 * s), control2: CGPoint(x: 3.48 * s, y: 13.23 * s))
            path.addCurve(to: CGPoint(x: 2.50 * s, y: 11.65 * s), control1: CGPoint(x: 3.23 * s, y: 12.55 * s), control2: CGPoint(x: 2.84 * s, y: 11.84 * s))
            path.addCurve(to: CGPoint(x: 2.49 * s, y: 11.12 * s), control1: CGPoint(x: 2.22 * s, y: 11.50 * s), control2: CGPoint(x: 1.82 * s, y: 11.13 * s))
            path.addCurve(to: CGPoint(x: 3.72 * s, y: 11.94 * s), control1: CGPoint(x: 3.12 * s, y: 11.11 * s), control2: CGPoint(x: 3.57 * s, y: 11.70 * s))
            path.addCurve(to: CGPoint(x: 6.05 * s, y: 12.60 * s), control1: CGPoint(x: 4.44 * s, y: 13.15 * s), control2: CGPoint(x: 5.59 * s, y: 12.81 * s))
            path.addCurve(to: CGPoint(x: 6.56 * s, y: 11.53 * s), control1: CGPoint(x: 6.12 * s, y: 12.08 * s), control2: CGPoint(x: 6.33 * s, y: 11.73 * s))
            path.addCurve(to: CGPoint(x: 2.92 * s, y: 7.58 * s), control1: CGPoint(x: 4.78 * s, y: 11.33 * s), control2: CGPoint(x: 2.92 * s, y: 10.64 * s))
            path.addCurve(to: CGPoint(x: 3.74 * s, y: 5.43 * s), control1: CGPoint(x: 2.92 * s, y: 6.71 * s), control2: CGPoint(x: 3.23 * s, y: 5.99 * s))
            path.addCurve(to: CGPoint(x: 3.82 * s, y: 3.31 * s), control1: CGPoint(x: 3.66 * s, y: 5.23 * s), control2: CGPoint(x: 3.38 * s, y: 4.41 * s))
            path.addCurve(to: CGPoint(x: 6.02 * s, y: 4.13 * s), control1: CGPoint(x: 3.82 * s, y: 3.31 * s), control2: CGPoint(x: 4.49 * s, y: 3.10 * s))
            path.addCurve(to: CGPoint(x: 8.02 * s, y: 3.86 * s), control1: CGPoint(x: 6.66 * s, y: 3.95 * s), control2: CGPoint(x: 7.34 * s, y: 3.86 * s))
            path.addCurve(to: CGPoint(x: 10.02 * s, y: 4.13 * s), control1: CGPoint(x: 8.70 * s, y: 3.86 * s), control2: CGPoint(x: 9.38 * s, y: 3.95 * s))
            path.addCurve(to: CGPoint(x: 12.22 * s, y: 3.31 * s), control1: CGPoint(x: 11.55 * s, y: 3.09 * s), control2: CGPoint(x: 12.22 * s, y: 3.31 * s))
            path.addCurve(to: CGPoint(x: 12.30 * s, y: 5.43 * s), control1: CGPoint(x: 12.66 * s, y: 4.41 * s), control2: CGPoint(x: 12.38 * s, y: 5.23 * s))
            path.addCurve(to: CGPoint(x: 13.12 * s, y: 7.58 * s), control1: CGPoint(x: 12.81 * s, y: 5.99 * s), control2: CGPoint(x: 13.12 * s, y: 6.70 * s))
            path.addCurve(to: CGPoint(x: 9.47 * s, y: 11.53 * s), control1: CGPoint(x: 13.12 * s, y: 10.65 * s), control2: CGPoint(x: 11.25 * s, y: 11.33 * s))
            path.addCurve(to: CGPoint(x: 10.01 * s, y: 13.01 * s), control1: CGPoint(x: 9.76 * s, y: 11.78 * s), control2: CGPoint(x: 10.01 * s, y: 12.26 * s))
            path.addCurve(to: CGPoint(x: 10.00 * s, y: 15.21 * s), control1: CGPoint(x: 10.01 * s, y: 14.08 * s), control2: CGPoint(x: 10.00 * s, y: 14.94 * s))
            path.addCurve(to: CGPoint(x: 10.53 * s, y: 15.59 * s), control1: CGPoint(x: 10.00 * s, y: 15.42 * s), control2: CGPoint(x: 10.15 * s, y: 15.67 * s))
            path.addCurve(to: CGPoint(x: 16.00 * s, y: 8.00 * s), control1: CGPoint(x: 13.71 * s, y: 14.53 * s), control2: CGPoint(x: 16.00 * s, y: 11.54 * s))
            path.addCurve(to: CGPoint(x: 8.00 * s, y: 0.00 * s), control1: CGPoint(x: 16.00 * s, y: 3.58 * s), control2: CGPoint(x: 12.42 * s, y: 0.00 * s))
            path.closeSubpath()
            return path
        }
    }

    private var statusColor: Color {
        if vm.isConnected { return .green }
        if vm.gatewayReachable { return .teal }
        return .orange
    }

    private var statusTitle: String {
        if vm.isBusy { return "Переключение..." }
        if vm.isConnected {
            return vm.mode == .tun ? "VPN-туннель активен" : "Системный прокси активен"
        }
        if vm.gatewayReachable { return "Телефон найден (:8080)" }
        return vm.gateway.isEmpty ? "Подключитесь к Wi-Fi телефона" : "Порт 8080 не отвечает"
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    var window: NSWindow?
    let vm = VPNViewModel()

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)

        let rootView = HotspotContentView(vm: vm)
        let hosting = NSHostingView(rootView: rootView)

        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 600, height: 700),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        win.title = "ShareNet"
        win.titleVisibility = .hidden
        win.titlebarAppearsTransparent = true
        win.isMovableByWindowBackground = true
        win.isOpaque = false
        win.backgroundColor = .clear
        win.contentView = hosting
        win.center()
        win.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        self.window = win
    }

    func applicationWillTerminate(_ notification: Notification) {
        if vm.isConnected {
            vm.disconnect(sync: true)
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }
}

@main
struct HotspotVPNLauncher {
    static func main() {
        let app = NSApplication.shared
        let delegate = AppDelegate()
        app.delegate = delegate
        app.run()
    }
}
