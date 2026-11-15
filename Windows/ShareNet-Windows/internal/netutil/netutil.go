package netutil

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// PublicIpv4Routes covers all public IPv4 space excluding RFC1918 private/hotspot subnets
// (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16) so hotspot replies exit via Wi-Fi AP instead of tun0.
var PublicIpv4Routes = []string{
	"0.0.0.0/5",
	"8.0.0.0/7",
	"11.0.0.0/8",
	"12.0.0.0/6",
	"16.0.0.0/4",
	"32.0.0.0/3",
	"64.0.0.0/2",
	"128.0.0.0/3",
	"160.0.0.0/5",
	"168.0.0.0/6",
	"172.0.0.0/12",
	"172.32.0.0/11",
	"172.64.0.0/10",
	"172.128.0.0/9",
	"173.0.0.0/8",
	"174.0.0.0/7",
	"176.0.0.0/4",
	"192.0.0.0/9",
	"192.128.0.0/11",
	"192.160.0.0/13",
	"192.169.0.0/16",
	"192.170.0.0/15",
	"192.172.0.0/14",
	"192.176.0.0/12",
	"192.192.0.0/10",
	"193.0.0.0/8",
	"194.0.0.0/7",
	"196.0.0.0/6",
	"200.0.0.0/5",
	"208.0.0.0/4",
	"224.0.0.0/3",
}

// DetectPrimaryDns returns up to 2 colon-separated active IPv4 DNS servers of this PC,
// or "1.1.1.1:8.8.8.8" as fallback. Uses Windows GetAdaptersAddresses via x/sys/windows.
func DetectPrimaryDns() string {
	dnsList := detectWindowsAdapterDns()
	if len(dnsList) == 0 {
		return "1.1.1.1:8.8.8.8"
	}
	if len(dnsList) == 1 {
		if ip := net.ParseIP(dnsList[0]); ip != nil && IsPrivateIPv4(ip) {
			return dnsList[0] + ":1.1.1.1"
		}
		return dnsList[0]
	}
	if len(dnsList) > 2 {
		return dnsList[0] + ":" + dnsList[1]
	}
	return strings.Join(dnsList, ":")
}

// NormalizeDnsCsv converts colon-, semicolon-, comma-, or space-separated IPv4 DNS entries
// into the comma-separated format required by gnirehtet's -d flag.
func NormalizeDnsCsv(raw string) string {
	var valid []string
	seen := make(map[string]bool)
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ':' || r == ';' || r == ' ' || r == '\t'
	}) {
		part = strings.TrimSpace(part)
		if ip := net.ParseIP(part); ip != nil && ip.To4() != nil {
			s := ip.String()
			if !seen[s] {
				seen[s] = true
				valid = append(valid, s)
			}
		}
	}
	if len(valid) == 0 {
		return "8.8.8.8"
	}
	return strings.Join(valid, ",")
}

func detectWindowsAdapterDns() []string {
	var size uint32 = 16384
	var buf []byte
	for i := 0; i < 4; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(
			windows.AF_INET,
			windows.GAA_FLAG_INCLUDE_GATEWAYS,
			0,
			(*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])),
			&size,
		)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil
		}
	}

	var result []string
	seen := make(map[string]bool)
	aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
	for ; aa != nil; aa = aa.Next {
		if aa.OperStatus != windows.IfOperStatusUp {
			continue
		}
		// Skip Loopback (24) and Tunnel (131) interfaces.
		if aa.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK || aa.IfType == windows.IF_TYPE_TUNNEL {
			continue
		}
		if aa.FirstGatewayAddress == nil {
			continue
		}
		for dns := aa.FirstDnsServerAddress; dns != nil; dns = dns.Next {
			sa, err := dns.Address.Sockaddr.Sockaddr()
			if err != nil {
				continue
			}
			sa4, ok := sa.(*syscall.SockaddrInet4)
			if !ok {
				continue
			}
			ip := net.IP(sa4.Addr[:])
			if ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			s := ip.String()
			if !seen[s] {
				seen[s] = true
				result = append(result, s)
			}
		}
	}
	return result
}

// BuildGnirehtetRoutes builds the comma-separated -r route list for gnirehtet:
// public IPv4 ranges + VPN control IPs + any private PC DNS servers.
func BuildGnirehtetRoutes(dnsCsv string) string {
	routes := make([]string, 0, len(PublicIpv4Routes)+4)
	seen := make(map[string]bool, len(PublicIpv4Routes)+4)
	addRoute := func(r string) {
		if !seen[r] {
			seen[r] = true
			routes = append(routes, r)
		}
	}

	for _, r := range PublicIpv4Routes {
		addRoute(r)
	}
	addRoute("10.0.0.2/32")
	addRoute("10.0.2.2/32")

	for _, part := range strings.FieldsFunc(dnsCsv, func(r rune) bool {
		return r == ',' || r == ':' || r == ';' || r == ' ' || r == '\t'
	}) {
		ip := net.ParseIP(strings.TrimSpace(part)).To4()
		if ip != nil && IsPrivateIPv4(ip) {
			addRoute(ip.String() + "/32")
		}
	}
	return strings.Join(routes, ",")
}

// IsPrivateIPv4 returns true for RFC1918 IPv4 addresses.
func IsPrivateIPv4(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	if ip4[0] == 10 {
		return true
	}
	if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
		return true
	}
	if ip4[0] == 192 && ip4[1] == 168 {
		return true
	}
	return false
}

// UserAdbTargetDir returns the admin-free per-user platform-tools directory under %LOCALAPPDATA%.
func UserAdbTargetDir() string {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		home, _ := os.UserHomeDir()
		localAppData = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(localAppData, "Android", "Sdk", "platform-tools")
}

// EnsureAdbInUserPath checks if adb is available in process or HKCU user PATH.
// If missing and sourceDir is valid, it installs platform-tools into %LOCALAPPDATA%
// and registers it in HKCU\Environment\Path without requiring admin privileges.
func EnsureAdbInUserPath(sourceDir string) (installed bool, msg string, err error) {
	if _, lookErr := exec.LookPath("adb"); lookErr == nil {
		return false, "", nil
	}
	if _, lookErr := exec.LookPath("adb.exe"); lookErr == nil {
		return false, "", nil
	}

	k, regErr := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if regErr == nil {
		userPath, _, getErr := k.GetStringValue("Path")
		_ = k.Close()
		if getErr == nil && pathListHasExistingAdb(userPath) {
			return false, "", nil
		}
	}

	if sourceDir == "" {
		return false, "", fmt.Errorf("папка platform-tools не задана")
	}
	msg, err = InstallAdbToUserProfile(sourceDir)
	if err != nil {
		return false, "", err
	}
	appendDirToProcessPath(UserAdbTargetDir())
	return true, msg, nil
}

func pathListHasExistingAdb(pathList string) bool {
	for _, entry := range strings.Split(pathList, ";") {
		dir := expandPercentEnvVars(strings.TrimSpace(strings.Trim(entry, `"`)))
		if dir == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, "adb.exe")); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func expandPercentEnvVars(s string) string {
	for {
		start := strings.IndexByte(s, '%')
		if start < 0 {
			return s
		}
		end := strings.IndexByte(s[start+1:], '%')
		if end <= 0 {
			return s
		}
		name := s[start+1 : start+1+end]
		val := os.Getenv(name)
		if val == "" {
			return s[:start+1+end+1] + expandPercentEnvVars(s[start+1+end+1:])
		}
		s = s[:start] + val + s[start+1+end+1:]
	}
}

func appendDirToProcessPath(dir string) {
	if dir == "" {
		return
	}
	cur := os.Getenv("PATH")
	norm := strings.TrimRight(dir, `\`)
	for _, p := range strings.Split(cur, ";") {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(p), `\`), norm) {
			return
		}
	}
	if cur == "" {
		_ = os.Setenv("PATH", dir)
	} else {
		_ = os.Setenv("PATH", cur+";"+dir)
	}
}

// InstallAdbToUserProfile copies platform-tools into %LOCALAPPDATA%\Android\Sdk\platform-tools
// and adds that directory to User PATH if absent.
func InstallAdbToUserProfile(sourceDir string) (string, error) {
	if sourceDir == "" {
		return "", fmt.Errorf("папка platform-tools не задана")
	}
	if info, err := os.Stat(sourceDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("папка platform-tools не найдена: %s", sourceDir)
	}

	targetDir := UserAdbTargetDir()
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}

	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(targetDir, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		return copyFile(path, dest)
	})
	if err != nil {
		return "", err
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err == nil {
		defer k.Close()
		userPath, valType, getErr := k.GetStringValue("Path")
		if getErr != nil && getErr != registry.ErrNotExist {
			return "", getErr
		}
		if valType == 0 {
			valType = registry.EXPAND_SZ
		}
		normTarget := strings.TrimRight(targetDir, `\`)
		var cleanParts []string
		exists := false
		for _, p := range strings.Split(userPath, ";") {
			trimmed := strings.TrimSpace(p)
			if trimmed == "" {
				continue
			}
			cleanParts = append(cleanParts, trimmed)
			if strings.EqualFold(strings.TrimRight(trimmed, `\`), normTarget) {
				exists = true
			}
		}
		if !exists {
			cleanParts = append(cleanParts, targetDir)
			newPath := strings.Join(cleanParts, ";")
			if valType == registry.EXPAND_SZ {
				_ = k.SetExpandStringValue("Path", newPath)
			} else {
				_ = k.SetStringValue("Path", newPath)
			}
		}
	}

	return fmt.Sprintf("ADB установлен в %s и добавлен в PATH пользователя.", targetDir), nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// OpenURL opens a URL or URI (e.g. https://github.com/TPpixel or tg://socks?...) via Windows shell handler.
func OpenURL(target string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	HideWindow(cmd)
	return cmd.Start()
}

// HideWindow configures a Windows child command to run without spawning a console window.
func HideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}
