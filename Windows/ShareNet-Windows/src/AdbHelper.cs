using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Text.RegularExpressions;

namespace ShareNetUSB
{
    internal sealed class CommandResult
    {
        public int ExitCode { get; set; }
        public string Output { get; set; }
    }

    internal sealed class DeviceInfo
    {
        public string Serial { get; set; }
        public string State { get; set; }
        public string Model { get; set; }
    }

    internal sealed class PrivateDnsBackup
    {
        public bool WasModified { get; set; }
        public string Mode { get; set; }
        public string Specifier { get; set; }
    }

    internal sealed class HotspotStatus
    {
        public bool IsActive { get; set; }
        public string InterfaceName { get; set; }
        public string IpAddress { get; set; }
    }

    internal sealed class AdbHelper
    {
        public const int TargetHotspotProxyVersionCode = 24;

        public string AppRoot { get; private set; }
        public string BundledPlatformToolsDir { get; private set; }
        public string GnirehtetDir { get; private set; }
        public string AdbExePath { get; private set; }
        public string GnirehtetExePath { get; private set; }
        public string GnirehtetApkPath { get; private set; }
        public string HotspotProxyApkPath { get; private set; }

        public AdbHelper()
        {
            AppRoot = AppDomain.CurrentDomain.BaseDirectory;
            BundledPlatformToolsDir = Path.Combine(AppRoot, @"tools\platform-tools");
            GnirehtetDir = Path.Combine(AppRoot, @"tools\gnirehtet");

            string bundledAdb = Path.Combine(BundledPlatformToolsDir, "adb.exe");
            if (File.Exists(bundledAdb))
            {
                AdbExePath = bundledAdb;
            }
            else
            {
                string localAppData = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
                string installedAdb = Path.Combine(localAppData, @"Android\Sdk\platform-tools\adb.exe");
                AdbExePath = File.Exists(installedAdb) ? installedAdb : "adb.exe";
            }

            GnirehtetExePath = Path.Combine(GnirehtetDir, "gnirehtet.exe");
            GnirehtetApkPath = Path.Combine(GnirehtetDir, "gnirehtet.apk");
            HotspotProxyApkPath = Path.Combine(AppRoot, @"tools\hotspot-proxy\hotspot-proxy.apk");
        }

        public string GetEffectivePathEnv()
        {
            string current = Environment.GetEnvironmentVariable("PATH") ?? string.Empty;
            string adbDir = Path.GetDirectoryName(AdbExePath);
            if (!string.IsNullOrEmpty(adbDir) && Directory.Exists(adbDir))
            {
                return adbDir + ";" + current;
            }
            return current;
        }

        public CommandResult RunAdb(string arguments, int timeoutMs)
        {
            var psi = new ProcessStartInfo
            {
                FileName = AdbExePath,
                Arguments = arguments,
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                StandardOutputEncoding = Encoding.UTF8,
                StandardErrorEncoding = Encoding.UTF8
            };

            var sb = new StringBuilder();
            using (var proc = Process.Start(psi))
            {
                string stdout = proc.StandardOutput.ReadToEnd();
                string stderr = proc.StandardError.ReadToEnd();
                if (!proc.WaitForExit(timeoutMs))
                {
                    try { proc.Kill(); } catch { }
                    return new CommandResult { ExitCode = -1, Output = "Превышено время ожидания adb." };
                }
                if (!string.IsNullOrEmpty(stdout)) sb.AppendLine(stdout.TrimEnd());
                if (!string.IsNullOrEmpty(stderr)) sb.AppendLine(stderr.TrimEnd());
                return new CommandResult { ExitCode = proc.ExitCode, Output = sb.ToString().Trim() };
            }
        }

        public List<DeviceInfo> GetDevices()
        {
            var devices = new List<DeviceInfo>();
            CommandResult res = RunAdb("devices -l", 8000);
            if (res.ExitCode != 0 || string.IsNullOrEmpty(res.Output))
            {
                return devices;
            }

            string[] lines = res.Output.Split(new[] { '\r', '\n' }, StringSplitOptions.RemoveEmptyEntries);
            foreach (string raw in lines)
            {
                string line = raw.Trim();
                if (line.Length == 0 || line.StartsWith("List of devices") || line.StartsWith("*"))
                {
                    continue;
                }

                string[] tokens = line.Split(new[] { ' ', '\t' }, StringSplitOptions.RemoveEmptyEntries);
                if (tokens.Length < 2)
                {
                    continue;
                }

                string model = string.Empty;
                for (int i = 2; i < tokens.Length; i++)
                {
                    if (tokens[i].StartsWith("model:"))
                    {
                        model = tokens[i].Substring("model:".Length).Replace('_', ' ');
                        break;
                    }
                }

                devices.Add(new DeviceInfo
                {
                    Serial = tokens[0],
                    State = tokens[1],
                    Model = model
                });
            }
            return devices;
        }

        public int GetInstalledVersionCode(string serial, string packageName)
        {
            string prefix = string.IsNullOrEmpty(serial) ? string.Empty : "-s " + serial + " ";
            CommandResult res = RunAdb(prefix + "shell dumpsys package " + packageName, 8000);
            if (res.ExitCode != 0 || string.IsNullOrEmpty(res.Output))
            {
                return 0;
            }
            Match m = Regex.Match(res.Output, @"versionCode=(\d+)");
            if (m.Success)
            {
                int vc;
                if (int.TryParse(m.Groups[1].Value, out vc))
                {
                    return vc;
                }
            }
            return 0;
        }

        public bool IsPackageInstalled(string serial, string packageName)
        {
            string prefix = string.IsNullOrEmpty(serial) ? string.Empty : "-s " + serial + " ";
            CommandResult res = RunAdb(prefix + "shell pm list packages " + packageName, 6000);
            return res.ExitCode == 0 &&
                   !string.IsNullOrEmpty(res.Output) &&
                   res.Output.IndexOf("package:" + packageName, StringComparison.OrdinalIgnoreCase) >= 0;
        }

        public void EnsureRequiredApksInstalled(string serial, bool installHotspotProxy, bool forceReinstall, Action<string> log)
        {
            string prefix = string.IsNullOrEmpty(serial) ? string.Empty : "-s " + serial + " ";

            if (File.Exists(GnirehtetApkPath))
            {
                if (forceReinstall || !IsPackageInstalled(serial, "com.genymobile.gnirehtet"))
                {
                    log("Установка gnirehtet.apk на телефон...");
                    CommandResult res = RunAdb(prefix + "install -r \"" + GnirehtetApkPath + "\"", 60000);
                    log("gnirehtet.apk: " + res.Output);
                }
            }

            if (installHotspotProxy && File.Exists(HotspotProxyApkPath))
            {
                int ver = GetInstalledVersionCode(serial, "com.tppixel.sharenet");
                if (forceReinstall || ver < TargetHotspotProxyVersionCode)
                {
                    log("Установка/обновление Hotspot Proxy на телефон...");
                    CommandResult res = RunAdb(prefix + "install -r \"" + HotspotProxyApkPath + "\"", 60000);
                    log("hotspot-proxy.apk: " + res.Output);
                }
            }
        }

        public CommandResult StartHotspotProxyApp(string serial, int port)
        {
            string prefix = string.IsNullOrEmpty(serial) ? string.Empty : "-s " + serial + " ";
            return RunAdb(
                prefix + "shell am start -n com.tppixel.sharenet/.MainActivity --ez autostart true --ei port " + port,
                8000);
        }

        public CommandResult StopHotspotProxyApp(string serial)
        {
            string prefix = string.IsNullOrEmpty(serial) ? string.Empty : "-s " + serial + " ";
            return RunAdb(
                prefix + "shell am start -n com.tppixel.sharenet/.MainActivity --ez autostop true --ez stopvpn true",
                8000);
        }

        public CommandResult OpenHotspotSettings(string serial)
        {
            string prefix = string.IsNullOrEmpty(serial) ? string.Empty : "-s " + serial + " ";
            return RunAdb(prefix + "shell am start -a android.settings.TETHER_SETTINGS", 8000);
        }

        public void ResetGnirehtetState(string serial)
        {
            try
            {
                Process[] existing = Process.GetProcessesByName("gnirehtet");
                foreach (Process p in existing)
                {
                    try
                    {
                        if (!p.HasExited)
                        {
                            p.Kill();
                            p.WaitForExit(1500);
                        }
                    }
                    catch { }
                    finally
                    {
                        try { p.Dispose(); } catch { }
                    }
                }
            }
            catch { }

            if (!string.IsNullOrEmpty(serial))
            {
                try { RunGnirehtetCommand("stop " + serial, 3000); } catch { }
                try { RunAdb("-s " + serial + " shell am force-stop com.genymobile.gnirehtet", 5000); } catch { }
            }
        }

        public HotspotStatus DetectHotspotStatus(string serial)
        {
            var status = new HotspotStatus();
            if (string.IsNullOrEmpty(serial))
            {
                return status;
            }

            string prefix = "-s " + serial + " ";
            string tetheredIface = null;

            CommandResult tetherRes = RunAdb(prefix + "shell dumpsys tethering", 6000);
            if (tetherRes.ExitCode == 0 && !string.IsNullOrEmpty(tetherRes.Output))
            {
                Match m = Regex.Match(tetherRes.Output, @"^\s*([a-zA-Z0-9_.-]+)\s*-\s*TetheredState", RegexOptions.Multiline);
                if (m.Success)
                {
                    tetheredIface = m.Groups[1].Value.Trim();
                }
            }

            CommandResult ipRes = RunAdb(prefix + "shell ip -4 addr", 6000);
            if (ipRes.ExitCode != 0 || string.IsNullOrEmpty(ipRes.Output))
            {
                return status;
            }

            var ifaceIps = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
            string currentIface = null;
            string[] lines = ipRes.Output.Split(new[] { '\r', '\n' }, StringSplitOptions.RemoveEmptyEntries);
            foreach (string line in lines)
            {
                Match hdr = Regex.Match(line, @"^\d+:\s+([^:@\s]+)");
                if (hdr.Success)
                {
                    currentIface = hdr.Groups[1].Value;
                    continue;
                }
                if (!string.IsNullOrEmpty(currentIface))
                {
                    Match inet = Regex.Match(line, @"\binet\s+(\d+\.\d+\.\d+\.\d+)/\d+");
                    if (inet.Success)
                    {
                        string ip = inet.Groups[1].Value;
                        if (ip != "127.0.0.1" && ip != "10.0.0.2" && !ifaceIps.ContainsKey(currentIface))
                        {
                            ifaceIps[currentIface] = ip;
                        }
                    }
                }
            }

            if (!string.IsNullOrEmpty(tetheredIface) && ifaceIps.ContainsKey(tetheredIface))
            {
                status.IsActive = true;
                status.InterfaceName = tetheredIface;
                status.IpAddress = ifaceIps[tetheredIface];
                return status;
            }

            // Fallback: look for common hotspot interfaces if dumpsys tethering format differs.
            string[] preferredPrefixes = new[] { "ap", "swlan", "softap", "wlan2", "wlan1" };
            foreach (string pref in preferredPrefixes)
            {
                foreach (var kv in ifaceIps)
                {
                    if (kv.Key.StartsWith(pref, StringComparison.OrdinalIgnoreCase))
                    {
                        status.IsActive = true;
                        status.InterfaceName = kv.Key;
                        status.IpAddress = kv.Value;
                        return status;
                    }
                }
            }

            return status;
        }

        public PrivateDnsBackup DisablePrivateDnsTemporarily(string serial)
        {
            string prefix = string.IsNullOrEmpty(serial) ? string.Empty : "-s " + serial + " ";
            string mode = RunAdb(prefix + "shell settings get global private_dns_mode", 5000).Output.Trim();
            string specifier = RunAdb(prefix + "shell settings get global private_dns_specifier", 5000).Output.Trim();

            if (string.Equals(mode, "hostname", StringComparison.OrdinalIgnoreCase))
            {
                RunAdb(prefix + "shell settings put global private_dns_mode off", 5000);
                return new PrivateDnsBackup
                {
                    WasModified = true,
                    Mode = mode,
                    Specifier = specifier
                };
            }

            return new PrivateDnsBackup { WasModified = false };
        }

        public void RestorePrivateDns(string serial, PrivateDnsBackup backup)
        {
            if (backup == null || !backup.WasModified)
            {
                return;
            }

            string prefix = string.IsNullOrEmpty(serial) ? string.Empty : "-s " + serial + " ";
            if (!string.IsNullOrEmpty(backup.Specifier) && backup.Specifier != "null")
            {
                RunAdb(prefix + "shell settings put global private_dns_specifier \"" + backup.Specifier + "\"", 5000);
            }
            RunAdb(prefix + "shell settings put global private_dns_mode \"" + backup.Mode + "\"", 5000);
        }

        public CommandResult RunGnirehtetCommand(string arguments, int timeoutMs)
        {
            var psi = new ProcessStartInfo
            {
                FileName = GnirehtetExePath,
                Arguments = arguments,
                WorkingDirectory = GnirehtetDir,
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                StandardOutputEncoding = Encoding.UTF8,
                StandardErrorEncoding = Encoding.UTF8
            };
            psi.EnvironmentVariables["PATH"] = GetEffectivePathEnv();

            var sb = new StringBuilder();
            using (var proc = Process.Start(psi))
            {
                string stdout = proc.StandardOutput.ReadToEnd();
                string stderr = proc.StandardError.ReadToEnd();
                if (!proc.WaitForExit(timeoutMs))
                {
                    try { proc.Kill(); } catch { }
                    return new CommandResult { ExitCode = -1, Output = "Таймаут gnirehtet." };
                }
                if (!string.IsNullOrEmpty(stdout)) sb.AppendLine(stdout.TrimEnd());
                if (!string.IsNullOrEmpty(stderr)) sb.AppendLine(stderr.TrimEnd());
                return new CommandResult { ExitCode = proc.ExitCode, Output = sb.ToString().Trim() };
            }
        }

        public string SendFileSmart(string serial, string localPath)
        {
            string prefix = string.IsNullOrEmpty(serial) ? string.Empty : "-s " + serial + " ";
            string ext = (Path.GetExtension(localPath) ?? string.Empty).ToLowerInvariant();
            string fileName = Path.GetFileName(localPath);

            if (ext == ".apk")
            {
                CommandResult res = RunAdb(prefix + "install -r \"" + localPath + "\"", 60000);
                return "Установка APK (" + fileName + "): " + res.Output;
            }

            bool isImage = ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" ||
                           ext == ".gif" || ext == ".bmp" || ext == ".jfif";

            if (isImage)
            {
                string remoteName = (ext == ".jfif")
                    ? Path.GetFileNameWithoutExtension(fileName) + ".jpg"
                    : fileName;
                string remotePath = "/sdcard/Pictures/" + remoteName;
                CommandResult pushRes = RunAdb(prefix + "push \"" + localPath + "\" \"" + remotePath + "\"", 60000);
                if (pushRes.ExitCode == 0)
                {
                    RunAdb(prefix + "shell content call --uri content://media --method scan_volume --arg external_primary", 10000);
                    return "Изображение отправлено в " + remotePath + " (проиндексировано для галереи).\r\n" + pushRes.Output;
                }
                return "Ошибка отправки изображения: " + pushRes.Output;
            }

            string downloadPath = "/sdcard/Download/" + fileName;
            CommandResult fileRes = RunAdb(prefix + "push \"" + localPath + "\" \"" + downloadPath + "\"", 60000);
            return "Файл отправлен в " + downloadPath + ":\r\n" + fileRes.Output;
        }
    }
}
