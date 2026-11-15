using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Text;
using System.Threading;
using System.Windows.Forms;

namespace ShareNetUSB
{
    internal sealed class MainForm : Form
    {
        private const int ProxyPort = 8080;
        private const int UsbSocksPort = 10808;

        private readonly AdbHelper _adb = new AdbHelper();
        private Socks5Server _usbSocksServer;

        private StatusPill _pillStatus;
        private Label _lblDeviceStatus;
        private CheckBox _chkAutoStartOnConnect;
        private TextBox _txtDns;
        private CheckBox _chkAutoPrivateDns;
        private CheckBox _chkStartHotspotProxy;
        private ModernButton _btnToggleShare;
        private ModernButton _btnRefresh;
        private ModernButton _btnAutoDns;

        private Label _lblHotspotBanner;
        private ModernButton _btnOpenHotspotSettings;
        private ModernButton _tabBtnVpn;
        private ModernButton _tabBtnMac;
        private ModernButton _tabBtnWin;
        private TextBox _txtGuide;
        private ModernButton _btnCopyGuideCmd;
        private int _selectedTab = 0;

        private ModernButton _btnSendFile;
        private ModernButton _btnInstallAdb;
        private ModernButton _btnOpenCmd;
        private ModernButton _btnReinstallApks;
        private TextBox _txtLog;
        private System.Windows.Forms.Timer _pollTimer;

        private Process _gnirehtetProcess;
        private bool _isSharing;
        private bool _isBusyStarting;
        private string _activeSerial;
        private string _autoStartedSerial;
        private string _currentHotspotIp;
        private PrivateDnsBackup _dnsBackup;

        public MainForm()
        {
            ThemeColors.SyncWithWindowsTheme();
            InitializeUi();
            _txtDns.Text = NetworkHelper.DetectPrimaryDns();
            UpdateLaptopGuides(new HotspotStatus());
            Log("Готово к работе. Авто-DNS: " + _txtDns.Text);
            RefreshDevicesAsync();

            _pollTimer = new System.Windows.Forms.Timer();
            _pollTimer.Interval = 4000;
            _pollTimer.Tick += delegate { RefreshDevicesAsync(); };
            _pollTimer.Start();
        }

        protected override void OnHandleCreated(EventArgs e)
        {
            base.OnHandleCreated(e);
            NativeDwm.EnableDarkTitleBar(Handle);
        }

        private void InitializeUi()
        {
            Text = "ShareNet USB — Интернет по USB и Hotspot";
            ClientSize = new Size(740, 748);
            MinimumSize = new Size(680, 680);
            StartPosition = FormStartPosition.CenterScreen;
            Font = new Font("Segoe UI", 9.5f);
            BackColor = ThemeColors.BgWindow;
            ForeColor = ThemeColors.TextPrimary;

            // Header
            var lblTitle = new Label
            {
                Text = "ShareNet USB",
                Location = new Point(18, 14),
                Size = new Size(240, 30),
                Font = new Font("Segoe UI", 16f, FontStyle.Bold),
                ForeColor = ThemeColors.TextPrimary
            };

            var lblSub = new Label
            {
                Text = "Интернет с ПК на Android + раздача по Hotspot без прав администратора",
                Location = new Point(20, 44),
                Size = new Size(500, 20),
                Font = new Font("Segoe UI", 9f),
                ForeColor = ThemeColors.TextSecondary
            };

            _pillStatus = new StatusPill
            {
                Text = "●  Поиск USB",
                Location = new Point(552, 20),
                Size = new Size(170, 28),
                Anchor = AnchorStyles.Top | AnchorStyles.Right
            };

            Controls.Add(lblTitle);
            Controls.Add(lblSub);
            Controls.Add(_pillStatus);

            // Card 1: Device
            var cardDevice = new RoundedCardPanel
            {
                Location = new Point(16, 74),
                Size = new Size(708, 96),
                TopRadius = 26,
                BottomRadius = 8,
                Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right
            };

            var lblCard1 = new Label
            {
                Text = "УСТРОЙСТВО ANDROID",
                Location = new Point(16, 10),
                Size = new Size(220, 18),
                Font = new Font("Segoe UI", 8f, FontStyle.Bold),
                ForeColor = ThemeColors.TextSecondary,
                BackColor = ThemeColors.BgCard
            };

            _lblDeviceStatus = new Label
            {
                Text = "Поиск устройства...",
                Location = new Point(16, 30),
                Size = new Size(554, 24),
                Font = new Font("Segoe UI", 10f, FontStyle.Bold),
                ForeColor = ThemeColors.WarnAmber,
                BackColor = ThemeColors.BgCard,
                AutoEllipsis = true,
                Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right
            };

            _btnRefresh = new ModernButton
            {
                Text = "Обновить",
                Location = new Point(584, 22),
                Size = new Size(108, 32),
                Anchor = AnchorStyles.Top | AnchorStyles.Right
            };
            _btnRefresh.Click += delegate { RefreshDevicesAsync(); };

            _chkAutoStartOnConnect = new CheckBox
            {
                Text = "Автоматически ставить APK и запускать раздачу при подключении телефона",
                Location = new Point(16, 62),
                Size = new Size(676, 22),
                Checked = true,
                ForeColor = ThemeColors.TextSecondary,
                BackColor = ThemeColors.BgCard
            };

            cardDevice.Controls.Add(lblCard1);
            cardDevice.Controls.Add(_lblDeviceStatus);
            cardDevice.Controls.Add(_btnRefresh);
            cardDevice.Controls.Add(_chkAutoStartOnConnect);
            Controls.Add(cardDevice);

            // Card 2: USB & Hotspot Proxy
            var cardNet = new RoundedCardPanel
            {
                Location = new Point(16, 174),
                Size = new Size(708, 174),
                CornerRadius = 8,
                Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right
            };

            var lblCard2 = new Label
            {
                Text = "РАЗДАЧА ИНТЕРНЕТА (GNIREHTET + HOTSPOT PROXY)",
                Location = new Point(16, 10),
                Size = new Size(360, 18),
                Font = new Font("Segoe UI", 8f, FontStyle.Bold),
                ForeColor = ThemeColors.TextSecondary,
                BackColor = ThemeColors.BgCard
            };

            var lblDns = new Label
            {
                Text = "DNS сети ПК:",
                Location = new Point(16, 38),
                Size = new Size(92, 24),
                ForeColor = ThemeColors.TextPrimary,
                BackColor = ThemeColors.BgCard
            };

            _txtDns = new TextBox
            {
                Location = new Point(112, 35),
                Size = new Size(276, 26),
                BackColor = ThemeColors.BgInput,
                ForeColor = ThemeColors.AccentBlue,
                BorderStyle = BorderStyle.FixedSingle,
                Font = new Font("Consolas", 10f, FontStyle.Bold)
            };

            _btnAutoDns = new ModernButton
            {
                Text = "Определить DNS ПК",
                Location = new Point(398, 33),
                Size = new Size(166, 30)
            };
            _btnAutoDns.Click += delegate
            {
                _txtDns.Text = NetworkHelper.DetectPrimaryDns();
                Log("Определён DNS сети ПК: " + _txtDns.Text);
            };

            _chkAutoPrivateDns = new CheckBox
            {
                Text = "Временно отключать Персональный DNS на телефоне (и возвращать при остановке)",
                Location = new Point(16, 70),
                Size = new Size(676, 22),
                Checked = true,
                ForeColor = ThemeColors.TextSecondary,
                BackColor = ThemeColors.BgCard
            };

            _chkStartHotspotProxy = new CheckBox
            {
                Text = "Автоматически устанавливать и запускать Hotspot Proxy (HTTP + SOCKS5 :8080) на телефоне",
                Location = new Point(16, 95),
                Size = new Size(676, 22),
                Checked = true,
                ForeColor = ThemeColors.TextSecondary,
                BackColor = ThemeColors.BgCard
            };

            _btnToggleShare = new ModernButton
            {
                Text = "Запустить всё (Интернет по USB + Hotspot Proxy)",
                Location = new Point(16, 124),
                Size = new Size(676, 38),
                NormalColor = ThemeColors.AccentEmerald,
                HoverColor = ThemeColors.AccentEmeraldHover,
                Font = new Font("Segoe UI", 10.5f, FontStyle.Bold),
                CornerRadius = 19,
                Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right
            };
            _btnToggleShare.Click += OnToggleShareClick;

            cardNet.Controls.Add(lblCard2);
            cardNet.Controls.Add(lblDns);
            cardNet.Controls.Add(_txtDns);
            cardNet.Controls.Add(_btnAutoDns);
            cardNet.Controls.Add(_chkAutoPrivateDns);
            cardNet.Controls.Add(_chkStartHotspotProxy);
            cardNet.Controls.Add(_btnToggleShare);
            Controls.Add(cardNet);

            // Card 3: Laptop Connection
            var cardHotspot = new RoundedCardPanel
            {
                Location = new Point(16, 352),
                Size = new Size(708, 214),
                CornerRadius = 8,
                Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right
            };

            var lblCard3 = new Label
            {
                Text = "ПОДКЛЮЧЕНИЕ НОУТБУКА ЧЕРЕЗ ТОЧКУ ДОСТУПА",
                Location = new Point(16, 10),
                Size = new Size(340, 18),
                Font = new Font("Segoe UI", 8f, FontStyle.Bold),
                ForeColor = ThemeColors.TextSecondary,
                BackColor = ThemeColors.BgCard
            };

            _lblHotspotBanner = new Label
            {
                Text = "Определение адреса точки доступа...",
                Location = new Point(16, 32),
                Size = new Size(462, 24),
                Font = new Font("Segoe UI", 9.5f, FontStyle.Bold),
                ForeColor = ThemeColors.WarnAmber,
                BackColor = ThemeColors.BgCard,
                AutoEllipsis = true,
                Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right
            };

            _btnOpenHotspotSettings = new ModernButton
            {
                Text = "Открыть Hotspot на телефоне",
                Location = new Point(490, 26),
                Size = new Size(202, 30),
                Anchor = AnchorStyles.Top | AnchorStyles.Right
            };
            _btnOpenHotspotSettings.Click += OnOpenHotspotSettingsClick;

            _tabBtnVpn = new ModernButton { Text = "HotspotVPN GUI (Mac / Win)", Location = new Point(16, 62), Size = new Size(192, 28), CornerRadius = 6 };
            _tabBtnMac = new ModernButton { Text = "macOS (Прокси вручную)", Location = new Point(214, 62), Size = new Size(168, 28), CornerRadius = 6 };
            _tabBtnWin = new ModernButton { Text = "Windows (Прокси / CMD)", Location = new Point(388, 62), Size = new Size(176, 28), CornerRadius = 6 };

            _tabBtnVpn.Click += delegate { SelectTab(0); };
            _tabBtnMac.Click += delegate { SelectTab(1); };
            _tabBtnWin.Click += delegate { SelectTab(2); };

            _txtGuide = new TextBox
            {
                Location = new Point(16, 96),
                Size = new Size(676, 74),
                Multiline = true,
                ReadOnly = true,
                ScrollBars = ScrollBars.Vertical,
                BackColor = ThemeColors.BgInput,
                ForeColor = ThemeColors.TextPrimary,
                BorderStyle = BorderStyle.FixedSingle,
                Font = new Font("Consolas", 8.8f),
                Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right
            };

            _btnCopyGuideCmd = new ModernButton
            {
                Text = "Скопировать команду",
                Location = new Point(16, 176),
                Size = new Size(276, 28),
                CornerRadius = 7
            };
            _btnCopyGuideCmd.Click += OnCopyGuideCmdClick;

            cardHotspot.Controls.Add(lblCard3);
            cardHotspot.Controls.Add(_lblHotspotBanner);
            cardHotspot.Controls.Add(_btnOpenHotspotSettings);
            cardHotspot.Controls.Add(_tabBtnVpn);
            cardHotspot.Controls.Add(_tabBtnMac);
            cardHotspot.Controls.Add(_tabBtnWin);
            cardHotspot.Controls.Add(_txtGuide);
            cardHotspot.Controls.Add(_btnCopyGuideCmd);
            Controls.Add(cardHotspot);

            // Card 4: Tools
            var cardTools = new RoundedCardPanel
            {
                Location = new Point(16, 570),
                Size = new Size(708, 54),
                TopRadius = 8,
                BottomRadius = 26,
                Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right
            };

            _btnSendFile = new ModernButton { Text = "Скинуть файл / APK", Location = new Point(12, 11), Size = new Size(166, 32) };
            _btnSendFile.Click += OnSendFileClick;

            _btnReinstallApks = new ModernButton { Text = "Обновить APK на телефоне", Location = new Point(186, 11), Size = new Size(184, 32) };
            _btnReinstallApks.Click += OnReinstallApksClick;

            _btnInstallAdb = new ModernButton { Text = "ADB в PATH", Location = new Point(378, 11), Size = new Size(146, 32) };
            _btnInstallAdb.Click += OnInstallAdbClick;

            _btnOpenCmd = new ModernButton { Text = "Терминал ADB", Location = new Point(532, 11), Size = new Size(164, 32) };
            _btnOpenCmd.Click += OnOpenCmdClick;

            cardTools.Controls.Add(_btnSendFile);
            cardTools.Controls.Add(_btnReinstallApks);
            cardTools.Controls.Add(_btnInstallAdb);
            cardTools.Controls.Add(_btnOpenCmd);
            Controls.Add(cardTools);

            // Log
            _txtLog = new TextBox
            {
                Location = new Point(16, 648),
                Size = new Size(708, 88),
                Multiline = true,
                ReadOnly = true,
                ScrollBars = ScrollBars.Vertical,
                BackColor = ThemeColors.BgLog,
                ForeColor = ThemeColors.TextSecondary,
                BorderStyle = BorderStyle.None,
                Font = new Font("Consolas", 8.5f),
                Anchor = AnchorStyles.Top | AnchorStyles.Bottom | AnchorStyles.Left | AnchorStyles.Right
            };
            Controls.Add(_txtLog);

            UpdateTabVisuals();
        }

        private void SelectTab(int idx)
        {
            _selectedTab = idx;
            UpdateTabVisuals();
            RenderSelectedGuide();
        }

        private void UpdateTabVisuals()
        {
            ModernButton[] btns = new[] { _tabBtnVpn, _tabBtnMac, _tabBtnWin };
            for (int i = 0; i < btns.Length; i++)
            {
                bool active = (i == _selectedTab);
                btns[i].NormalColor = active ? ThemeColors.TabActiveNormal : ThemeColors.BgInput;
                btns[i].HoverColor = active ? ThemeColors.TabActiveHover : ThemeColors.BtnNeutral;
                btns[i].BorderColor = Color.Transparent;
                btns[i].Invalidate();
            }
        }

        private void Log(string message)
        {
            if (string.IsNullOrEmpty(message))
            {
                return;
            }
            if (InvokeRequired)
            {
                try { BeginInvoke(new Action<string>(Log), message); } catch { }
                return;
            }
            string stamp = DateTime.Now.ToString("HH:mm:ss");
            _txtLog.AppendText("[" + stamp + "] " + message + Environment.NewLine);
        }

        private void SetStatusPill(string text, Color fg, Color bg, Color border)
        {
            _pillStatus.Text = text;
            _pillStatus.ForeColor = fg;
            _pillStatus.PillBackColor = bg;
            _pillStatus.PillBorderColor = border;
            _pillStatus.Invalidate();
        }

        private void RefreshDevicesAsync()
        {
            ThreadPool.QueueUserWorkItem(delegate
            {
                List<DeviceInfo> devices = _adb.GetDevices();
                HotspotStatus hotspot = new HotspotStatus();
                DeviceInfo readyDevice = null;

                foreach (DeviceInfo d in devices)
                {
                    if (d.State == "device")
                    {
                        readyDevice = d;
                        hotspot = _adb.DetectHotspotStatus(d.Serial);
                        break;
                    }
                }

                if (IsDisposed) return;

                try
                {
                    BeginInvoke(new Action(delegate
                    {
                        UpdateLaptopGuides(hotspot);

                        if (devices.Count == 0)
                        {
                            _activeSerial = null;
                            _autoStartedSerial = null;
                            _lblDeviceStatus.Text = "Телефон не обнаружен. Подключите USB и включите Отладку по USB.";
                            _lblDeviceStatus.ForeColor = ThemeColors.WarnAmber;
                            if (!_isSharing)
                            {
                                SetStatusPill("●  Нет USB", ThemeColors.WarnAmber, ThemeColors.PillBgWarn, Color.Transparent);
                            }
                            return;
                        }

                        DeviceInfo d = readyDevice ?? devices[0];
                        _activeSerial = d.Serial;
                        string name = string.IsNullOrEmpty(d.Model) ? d.Serial : (d.Model + " (" + d.Serial + ")");
                        if (d.State == "device")
                        {
                            _lblDeviceStatus.Text = "Подключён: " + name;
                            _lblDeviceStatus.ForeColor = ThemeColors.OkGreen;
                            if (!_isSharing)
                            {
                                SetStatusPill("●  Готов", ThemeColors.AccentBlue, ThemeColors.PillBgReady, Color.Transparent);
                            }

                            if (_chkAutoStartOnConnect.Checked &&
                                !_isSharing &&
                                !_isBusyStarting &&
                                !string.Equals(_autoStartedSerial, d.Serial, StringComparison.Ordinal))
                            {
                                _autoStartedSerial = d.Serial;
                                Log("Автозапуск для " + name + "...");
                                StartSharing();
                            }
                        }
                        else if (d.State == "unauthorized" || d.State == "authorizing")
                        {
                            _lblDeviceStatus.Text = name + " — подтвердите отладку на экране телефона";
                            _lblDeviceStatus.ForeColor = ThemeColors.WarnAmber;
                            SetStatusPill("●  Ожидает OK", ThemeColors.WarnAmber, ThemeColors.PillBgWarn, Color.Transparent);
                        }
                        else
                        {
                            _lblDeviceStatus.Text = name + " [" + d.State + "]";
                            _lblDeviceStatus.ForeColor = ThemeColors.ErrRed;
                            SetStatusPill("●  Ошибка", ThemeColors.ErrRed, ThemeColors.PillBgErr, Color.Transparent);
                        }
                    }));
                }
                catch { }
            });
        }

        private void UpdateLaptopGuides(HotspotStatus hotspot)
        {
            if (hotspot != null && hotspot.IsActive && !string.IsNullOrEmpty(hotspot.IpAddress))
            {
                _currentHotspotIp = hotspot.IpAddress;
                _lblHotspotBanner.Text = "Точка доступа активна (" + hotspot.InterfaceName + "): " +
                                         hotspot.IpAddress + ":" + ProxyPort;
                _lblHotspotBanner.ForeColor = ThemeColors.OkGreen;
                _btnCopyGuideCmd.Enabled = true;
            }
            else
            {
                _currentHotspotIp = null;
                _lblHotspotBanner.Text = "Точка доступа (Hotspot) на телефоне выключена — включите её!";
                _lblHotspotBanner.ForeColor = ThemeColors.WarnAmber;
                _btnCopyGuideCmd.Enabled = (_selectedTab == 0);
            }

            RenderSelectedGuide();
        }

        private void RenderSelectedGuide()
        {
            string ip = string.IsNullOrEmpty(_currentHotspotIp) ? "<IP_ТЕЛЕФОНА>" : _currentHotspotIp;
            string portStr = ProxyPort.ToString();

            if (_selectedTab == 0)
            {
                _txtGuide.Text =
                    "Самый удобный способ (весь трафик + DNS + Antigravity + Telegram):\r\n" +
                    "  • macOS (Intel): откройте HotspotVPN-Mac\\HotspotVPN.app (или Install_HotspotVPN_Mac.command)\r\n" +
                    "  • Windows-ноутбук: откройте HotspotVPN-Win\\HotspotVPN.exe\r\n" +
                    "Утилита сама найдёт телефон (" + ip + ":" + portStr + ") и поднимет системный туннель.";
                _btnCopyGuideCmd.Text = "Скопировать команду Telegram SOCKS5";
                _btnCopyGuideCmd.Enabled = !string.IsNullOrEmpty(_currentHotspotIp);
            }
            else if (_selectedTab == 1)
            {
                _txtGuide.Text =
                    "1. Подключите Mac к Wi-Fi точке доступа телефона.\r\n" +
                    "2. Системные настройки -> Wi-Fi -> Подробнее... -> Прокси:\r\n" +
                    "   Включите Веб-прокси (HTTP) и Защищённый веб-прокси (HTTPS): " + ip + ":" + portStr + " -> OK.\r\n" +
                    "3. Telegram Desktop: SOCKS5 " + ip + ":" + portStr;
                _btnCopyGuideCmd.Text = "Скопировать команду Терминала для Mac";
                _btnCopyGuideCmd.Enabled = !string.IsNullOrEmpty(_currentHotspotIp);
            }
            else
            {
                _txtGuide.Text =
                    "1. Подключите ноутбук Windows к Wi-Fi точке доступа телефона.\r\n" +
                    "2. Параметры -> Сеть и интернет -> Прокси-сервер -> Вручную:\r\n" +
                    "   Адрес: " + ip + "   Порт: " + portStr + "   -> Сохранить.\r\n" +
                    "3. Или запустите HotspotVPN-Win\\HotspotVPN.exe для автоматической настройки.";
                _btnCopyGuideCmd.Text = "Скопировать команду CMD для Windows";
                _btnCopyGuideCmd.Enabled = !string.IsNullOrEmpty(_currentHotspotIp);
            }
        }

        private void OnCopyGuideCmdClick(object sender, EventArgs e)
        {
            if (string.IsNullOrEmpty(_currentHotspotIp))
            {
                return;
            }
            string cmd;
            if (_selectedTab == 0)
            {
                cmd = string.Format("tg://socks?server={0}&port={1}", _currentHotspotIp, ProxyPort);
            }
            else if (_selectedTab == 1)
            {
                cmd = string.Format(
                    "networksetup -setwebproxy \"Wi-Fi\" {0} {1} && networksetup -setsecurewebproxy \"Wi-Fi\" {0} {1} && networksetup -setsocksfirewallproxy \"Wi-Fi\" {0} {1}",
                    _currentHotspotIp, ProxyPort);
            }
            else
            {
                cmd = string.Format(
                    "reg add \"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings\" /v ProxyEnable /t REG_DWORD /d 1 /f && reg add \"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings\" /v ProxyServer /t REG_SZ /d \"{0}:{1}\" /f",
                    _currentHotspotIp, ProxyPort);
            }

            Clipboard.SetText(cmd);
            Log("Скопировано в буфер обмена: " + cmd);
        }

        private void OnToggleShareClick(object sender, EventArgs e)
        {
            if (_isSharing)
            {
                StopSharing();
            }
            else
            {
                StartSharing();
            }
        }

        private void StartSharing()
        {
            if (_isBusyStarting || _isSharing)
            {
                return;
            }
            if (!File.Exists(_adb.GnirehtetExePath))
            {
                MessageBox.Show("Не найден файл: " + _adb.GnirehtetExePath, "Ошибка", MessageBoxButtons.OK, MessageBoxIcon.Error);
                return;
            }

            string dns = _txtDns.Text.Trim();
            if (string.IsNullOrEmpty(dns))
            {
                dns = NetworkHelper.DetectPrimaryDns();
                _txtDns.Text = dns;
            }

            bool disablePrivateDns = _chkAutoPrivateDns.Checked;
            bool startProxy = _chkStartHotspotProxy.Checked;
            string routes = NetworkHelper.BuildGnirehtetRoutes(dns);

            _isBusyStarting = true;
            _btnToggleShare.Enabled = false;
            SetStatusPill("●  Запуск...", ThemeColors.WarnAmber, ThemeColors.PillBgWarn, Color.Transparent);
            Log("Запуск (DNS: " + dns + ", исключены локальные подсети Hotspot)...");

            ThreadPool.QueueUserWorkItem(delegate
            {
                try
                {
                    List<DeviceInfo> devices = _adb.GetDevices();
                    DeviceInfo ready = null;
                    foreach (DeviceInfo d in devices)
                    {
                        if (d.State == "device")
                        {
                            ready = d;
                            break;
                        }
                    }

                    if (ready == null)
                    {
                        BeginInvoke(new Action(delegate
                        {
                            _isBusyStarting = false;
                            _btnToggleShare.Enabled = true;
                            Log("Нет авторизованного устройства. Подтвердите отладку по USB на телефоне.");
                            MessageBox.Show("Телефон не готов. Подключите его и разрешите отладку по USB.", "Нет устройства", MessageBoxButtons.OK, MessageBoxIcon.Warning);
                        }));
                        return;
                    }

                    string serial = ready.Serial;
                    _activeSerial = serial;
                    _autoStartedSerial = serial;

                    _adb.EnsureRequiredApksInstalled(serial, startProxy, false, Log);

                    if (disablePrivateDns)
                    {
                        _dnsBackup = _adb.DisablePrivateDnsTemporarily(serial);
                        if (_dnsBackup.WasModified)
                        {
                            Log("Персональный DNS (" + _dnsBackup.Specifier + ") временно отключён для работы туннеля.");
                        }
                    }
                    else
                    {
                        _dnsBackup = null;
                    }

                    _adb.ResetGnirehtetState(serial);

                    if (_usbSocksServer == null)
                    {
                        _usbSocksServer = new Socks5Server(UsbSocksPort);
                        _usbSocksServer.Start();
                    }
                    _adb.RunAdb("-s " + serial + " reverse --remove tcp:" + UsbSocksPort, 3000);
                    CommandResult revRes = _adb.RunAdb("-s " + serial + " reverse tcp:" + UsbSocksPort + " tcp:" + UsbSocksPort, 5000);
                    if (revRes.ExitCode == 0)
                    {
                        Log("USB SOCKS5 DoH (1.1.1.1) готов на 127.0.0.1:" + UsbSocksPort);
                    }

                    string args = "run " + serial + " -d " + dns + " -r " + routes;
                    var psi = new ProcessStartInfo
                    {
                        FileName = _adb.GnirehtetExePath,
                        Arguments = args,
                        WorkingDirectory = _adb.GnirehtetDir,
                        UseShellExecute = false,
                        CreateNoWindow = true,
                        RedirectStandardOutput = true,
                        RedirectStandardError = true,
                        StandardOutputEncoding = Encoding.UTF8,
                        StandardErrorEncoding = Encoding.UTF8
                    };
                    psi.EnvironmentVariables["PATH"] = _adb.GetEffectivePathEnv();

                    var proc = new Process();
                    proc.StartInfo = psi;
                    proc.EnableRaisingEvents = true;
                    proc.OutputDataReceived += delegate(object s, DataReceivedEventArgs ev)
                    {
                        if (!string.IsNullOrEmpty(ev.Data)) Log(ev.Data);
                    };
                    proc.ErrorDataReceived += delegate(object s, DataReceivedEventArgs ev)
                    {
                        if (!string.IsNullOrEmpty(ev.Data)) Log(ev.Data);
                    };
                    proc.Exited += delegate
                    {
                        if (!IsDisposed)
                        {
                            BeginInvoke(new Action(delegate
                            {
                                if (_isSharing)
                                {
                                    CleanupAfterShareStop();
                                    Log("Раздача завершена.");
                                }
                            }));
                        }
                    };

                    proc.Start();
                    proc.BeginOutputReadLine();
                    proc.BeginErrorReadLine();
                    _gnirehtetProcess = proc;

                    if (startProxy)
                    {
                        Thread.Sleep(1200);
                        CommandResult proxyRes = _adb.StartHotspotProxyApp(serial, ProxyPort);
                        if (proxyRes.ExitCode == 0)
                        {
                            Log("Hotspot Proxy запущен на телефоне (порт " + ProxyPort + ").");
                        }
                        else
                        {
                            Log("Не удалось автоматически запустить Hotspot Proxy: " + proxyRes.Output);
                        }
                    }

                    HotspotStatus hotspot = _adb.DetectHotspotStatus(serial);

                    BeginInvoke(new Action(delegate
                    {
                        _isBusyStarting = false;
                        _isSharing = true;
                        _btnToggleShare.Enabled = true;
                        _btnToggleShare.Text = "Остановить всё";
                        _btnToggleShare.NormalColor = ThemeColors.AccentRose;
                        _btnToggleShare.HoverColor = ThemeColors.AccentRoseHover;
                        _btnToggleShare.BorderColor = Color.Transparent;
                        _btnToggleShare.Invalidate();
                        _txtDns.Enabled = false;
                        _btnAutoDns.Enabled = false;
                        _chkAutoPrivateDns.Enabled = false;
                        _chkStartHotspotProxy.Enabled = false;
                        SetStatusPill("●  Раздача активна", ThemeColors.OkGreen, ThemeColors.PillBgOk, Color.Transparent);
                        UpdateLaptopGuides(hotspot);
                        Log("Если на телефоне появился запрос VPN — нажмите OK.");
                    }));
                }
                catch (Exception ex)
                {
                    BeginInvoke(new Action(delegate
                    {
                        _isBusyStarting = false;
                        _btnToggleShare.Enabled = true;
                        Log("Ошибка запуска: " + ex.Message);
                    }));
                }
            });
        }

        private void StopSharing()
        {
            _btnToggleShare.Enabled = false;
            Log("Остановка раздачи интернета и Hotspot Proxy...");

            ThreadPool.QueueUserWorkItem(delegate
            {
                CleanupProcessesAndRestoreDns();
                if (!IsDisposed)
                {
                    BeginInvoke(new Action(delegate
                    {
                        CleanupAfterShareStop();
                        Log("Раздача остановлена.");
                    }));
                }
            });
        }

        private void CleanupProcessesAndRestoreDns()
        {
            try
            {
                if (_usbSocksServer != null)
                {
                    _usbSocksServer.Stop();
                    _usbSocksServer = null;
                }
            }
            catch { }

            try
            {
                if (_gnirehtetProcess != null && !_gnirehtetProcess.HasExited)
                {
                    _gnirehtetProcess.Kill();
                    _gnirehtetProcess.WaitForExit(2000);
                }
            }
            catch { }
            _gnirehtetProcess = null;

            try
            {
                if (!string.IsNullOrEmpty(_activeSerial))
                {
                    _adb.RunGnirehtetCommand("stop " + _activeSerial, 5000);
                    _adb.RunAdb("-s " + _activeSerial + " shell am force-stop com.genymobile.gnirehtet", 4000);
                    _adb.RunAdb("-s " + _activeSerial + " reverse --remove tcp:" + UsbSocksPort, 3000);
                    if (_chkStartHotspotProxy.Checked)
                    {
                        _adb.StopHotspotProxyApp(_activeSerial);
                    }
                }
            }
            catch { }

            try
            {
                if (_dnsBackup != null && _dnsBackup.WasModified)
                {
                    _adb.RestorePrivateDns(_activeSerial, _dnsBackup);
                    Log("Персональный DNS (" + _dnsBackup.Specifier + ") восстановлен.");
                    _dnsBackup = null;
                }
            }
            catch { }
        }

        private void CleanupAfterShareStop()
        {
            _isBusyStarting = false;
            _isSharing = false;
            _btnToggleShare.Enabled = true;
            _btnToggleShare.Text = "Запустить всё (Интернет по USB + Hotspot Proxy)";
            _btnToggleShare.NormalColor = ThemeColors.AccentEmerald;
            _btnToggleShare.HoverColor = ThemeColors.AccentEmeraldHover;
            _btnToggleShare.BorderColor = Color.Transparent;
            _btnToggleShare.Invalidate();
            _txtDns.Enabled = true;
            _btnAutoDns.Enabled = true;
            _chkAutoPrivateDns.Enabled = true;
            _chkStartHotspotProxy.Enabled = true;
            SetStatusPill("●  Остановлен", ThemeColors.WarnAmber, ThemeColors.PillBgWarn, Color.Transparent);
        }

        private void OnOpenHotspotSettingsClick(object sender, EventArgs e)
        {
            string serial = _activeSerial;
            if (string.IsNullOrEmpty(serial))
            {
                MessageBox.Show("Сначала подключите телефон по USB.", "Нет устройства", MessageBoxButtons.OK, MessageBoxIcon.Warning);
                return;
            }
            ThreadPool.QueueUserWorkItem(delegate
            {
                CommandResult res = _adb.OpenHotspotSettings(serial);
                Log(res.ExitCode == 0 ? "Открыты настройки точки доступа на телефоне." : res.Output);
            });
        }

        private void OnReinstallApksClick(object sender, EventArgs e)
        {
            string serial = _activeSerial;
            if (string.IsNullOrEmpty(serial))
            {
                MessageBox.Show("Сначала подключите телефон по USB.", "Нет устройства", MessageBoxButtons.OK, MessageBoxIcon.Warning);
                return;
            }
            _btnReinstallApks.Enabled = false;
            ThreadPool.QueueUserWorkItem(delegate
            {
                try
                {
                    _adb.EnsureRequiredApksInstalled(serial, true, true, Log);
                }
                finally
                {
                    if (!IsDisposed)
                    {
                        BeginInvoke(new Action(delegate { _btnReinstallApks.Enabled = true; }));
                    }
                }
            });
        }

        private void OnSendFileClick(object sender, EventArgs e)
        {
            using (var dlg = new OpenFileDialog())
            {
                dlg.Title = "Выберите файл, изображение или APK для отправки на телефон";
                dlg.Filter = "Все файлы (*.*)|*.*|Изображения|*.jpg;*.jpeg;*.png;*.webp;*.jfif;*.gif|Приложения (*.apk)|*.apk";
                if (dlg.ShowDialog(this) != DialogResult.OK)
                {
                    return;
                }

                string filePath = dlg.FileName;
                string serial = _activeSerial;
                _btnSendFile.Enabled = false;
                Log("Отправка: " + Path.GetFileName(filePath) + "...");

                ThreadPool.QueueUserWorkItem(delegate
                {
                    string result = _adb.SendFileSmart(serial, filePath);
                    if (!IsDisposed)
                    {
                        BeginInvoke(new Action(delegate
                        {
                            _btnSendFile.Enabled = true;
                            Log(result);
                        }));
                    }
                });
            }
        }

        private void OnInstallAdbClick(object sender, EventArgs e)
        {
            try
            {
                string msg = NetworkHelper.InstallAdbToUserProfile(_adb.BundledPlatformToolsDir);
                Log(msg);
                MessageBox.Show(msg + "\r\n\r\nНовые окна терминала увидят команду adb автоматически.", "Готово", MessageBoxButtons.OK, MessageBoxIcon.Information);
            }
            catch (Exception ex)
            {
                Log("Ошибка установки ADB: " + ex.Message);
                MessageBox.Show("Ошибка: " + ex.Message, "Ошибка", MessageBoxButtons.OK, MessageBoxIcon.Error);
            }
        }

        private void OnOpenCmdClick(object sender, EventArgs e)
        {
            try
            {
                var psi = new ProcessStartInfo
                {
                    FileName = "cmd.exe",
                    Arguments = "/k adb version && adb devices -l",
                    UseShellExecute = false
                };
                psi.EnvironmentVariables["PATH"] = _adb.GetEffectivePathEnv();
                Process.Start(psi);
            }
            catch (Exception ex)
            {
                Log("Не удалось открыть cmd: " + ex.Message);
            }
        }

        protected override void OnFormClosing(FormClosingEventArgs e)
        {
            if (_pollTimer != null)
            {
                _pollTimer.Stop();
            }
            if (_isSharing || (_dnsBackup != null && _dnsBackup.WasModified))
            {
                CleanupProcessesAndRestoreDns();
            }
            base.OnFormClosing(e);
        }
    }
}
