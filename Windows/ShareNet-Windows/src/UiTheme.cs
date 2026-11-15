using System;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Runtime.InteropServices;
using System.Windows.Forms;
using Microsoft.Win32;

namespace ShareNetUSB
{
    /// <summary>
    /// Material 3 Expressive (MD3E) tonal color system synced with Windows Light/Dark theme.
    /// </summary>
    internal static class ThemeColors
    {
        public static bool IsDark { get; private set; }

        public static Color BgWindow = Color.FromArgb(0, 0, 0);
        public static Color BgCard = Color.FromArgb(20, 19, 24);
        public static Color BgInput = Color.FromArgb(37, 36, 43);
        public static Color BgLog = Color.FromArgb(12, 11, 16);
        public static Color BorderCard = Color.FromArgb(28, 27, 32);
        public static Color TextPrimary = Color.FromArgb(230, 225, 230);
        public static Color TextSecondary = Color.FromArgb(196, 192, 206);
        public static Color AccentEmerald = Color.FromArgb(23, 75, 55);
        public static Color AccentEmeraldHover = Color.FromArgb(33, 102, 75);
        public static Color AccentRose = Color.FromArgb(96, 20, 16);
        public static Color AccentRoseHover = Color.FromArgb(125, 29, 24);
        public static Color AccentBlue = Color.FromArgb(200, 191, 255);
        public static Color TabActiveNormal = Color.FromArgb(56, 53, 75);
        public static Color TabActiveHover = Color.FromArgb(72, 68, 96);
        public static Color BtnNeutral = Color.FromArgb(37, 36, 43);
        public static Color BtnNeutralHover = Color.FromArgb(48, 47, 55);
        public static Color BtnMuted = Color.FromArgb(28, 27, 32);
        public static Color WarnAmber = Color.FromArgb(252, 211, 77);
        public static Color OkGreen = Color.FromArgb(120, 220, 178);
        public static Color ErrRed = Color.FromArgb(249, 222, 220);

        public static Color PillBgOk = Color.FromArgb(23, 75, 55);
        public static Color PillBgWarn = Color.FromArgb(66, 44, 5);
        public static Color PillBgErr = Color.FromArgb(96, 20, 16);
        public static Color PillBgReady = Color.FromArgb(56, 53, 75);

        static ThemeColors()
        {
            SyncWithWindowsTheme();
        }

        public static void SyncWithWindowsTheme()
        {
            bool dark = true;
            try
            {
                using (RegistryKey key = Registry.CurrentUser.OpenSubKey(@"Software\Microsoft\Windows\CurrentVersion\Themes\Personalize"))
                {
                    if (key != null)
                    {
                        object v = key.GetValue("AppsUseLightTheme");
                        if (v is int && (int)v == 1)
                        {
                            dark = false;
                        }
                    }
                }
            }
            catch { }

            IsDark = dark;
            if (dark)
            {
                BgWindow = Color.FromArgb(0, 0, 0);
                BgCard = Color.FromArgb(20, 19, 24);
                BgInput = Color.FromArgb(37, 36, 43);
                BgLog = Color.FromArgb(12, 11, 16);
                BorderCard = Color.FromArgb(28, 27, 32);
                TextPrimary = Color.FromArgb(230, 225, 230);
                TextSecondary = Color.FromArgb(196, 192, 206);
                AccentEmerald = Color.FromArgb(23, 75, 55);
                AccentEmeraldHover = Color.FromArgb(33, 102, 75);
                AccentRose = Color.FromArgb(96, 20, 16);
                AccentRoseHover = Color.FromArgb(125, 29, 24);
                AccentBlue = Color.FromArgb(200, 191, 255);
                TabActiveNormal = Color.FromArgb(56, 53, 75);
                TabActiveHover = Color.FromArgb(72, 68, 96);
                BtnNeutral = Color.FromArgb(37, 36, 43);
                BtnNeutralHover = Color.FromArgb(48, 47, 55);
                BtnMuted = Color.FromArgb(28, 27, 32);
                WarnAmber = Color.FromArgb(252, 211, 77);
                OkGreen = Color.FromArgb(120, 220, 178);
                ErrRed = Color.FromArgb(249, 222, 220);
                PillBgOk = Color.FromArgb(23, 75, 55);
                PillBgWarn = Color.FromArgb(66, 44, 5);
                PillBgErr = Color.FromArgb(96, 20, 16);
                PillBgReady = Color.FromArgb(56, 53, 75);
            }
            else
            {
                BgWindow = Color.FromArgb(242, 238, 247);
                BgCard = Color.FromArgb(255, 255, 255);
                BgInput = Color.FromArgb(231, 225, 238);
                BgLog = Color.FromArgb(247, 242, 250);
                BorderCard = Color.FromArgb(231, 225, 238);
                TextPrimary = Color.FromArgb(28, 27, 32);
                TextSecondary = Color.FromArgb(73, 69, 82);
                AccentEmerald = Color.FromArgb(164, 242, 206);
                AccentEmeraldHover = Color.FromArgb(141, 231, 190);
                AccentRose = Color.FromArgb(249, 222, 220);
                AccentRoseHover = Color.FromArgb(242, 204, 201);
                AccentBlue = Color.FromArgb(91, 75, 183);
                TabActiveNormal = Color.FromArgb(227, 223, 250);
                TabActiveHover = Color.FromArgb(212, 206, 245);
                BtnNeutral = Color.FromArgb(231, 225, 238);
                BtnNeutralHover = Color.FromArgb(220, 213, 229);
                BtnMuted = Color.FromArgb(239, 234, 245);
                WarnAmber = Color.FromArgb(122, 77, 0);
                OkGreen = Color.FromArgb(18, 110, 75);
                ErrRed = Color.FromArgb(179, 38, 30);
                PillBgOk = Color.FromArgb(184, 243, 216);
                PillBgWarn = Color.FromArgb(255, 240, 199);
                PillBgErr = Color.FromArgb(249, 222, 220);
                PillBgReady = Color.FromArgb(227, 223, 250);
            }
        }
    }

    internal static class NativeDwm
    {
        [DllImport("dwmapi.dll")]
        private static extern int DwmSetWindowAttribute(IntPtr hwnd, int attr, ref int attrValue, int attrSize);

        public static void EnableDarkTitleBar(IntPtr handle)
        {
            try
            {
                int dark = ThemeColors.IsDark ? 1 : 0;
                if (DwmSetWindowAttribute(handle, 20, ref dark, sizeof(int)) != 0)
                {
                    DwmSetWindowAttribute(handle, 19, ref dark, sizeof(int));
                }
            }
            catch
            {
            }
        }
    }

    internal sealed class RoundedCardPanel : Panel
    {
        public int CornerRadius { get; set; }
        public int TopRadius { get; set; }
        public int BottomRadius { get; set; }
        public Color FillColor { get; set; }
        public Color StrokeColor { get; set; }

        public RoundedCardPanel()
        {
            CornerRadius = 22;
            TopRadius = -1;
            BottomRadius = -1;
            FillColor = ThemeColors.BgCard;
            StrokeColor = Color.Transparent;
            BackColor = ThemeColors.BgWindow;
            DoubleBuffered = true;
            SetStyle(ControlStyles.AllPaintingInWmPaint | ControlStyles.UserPaint | ControlStyles.ResizeRedraw, true);
        }

        protected override void OnPaint(PaintEventArgs e)
        {
            base.OnPaint(e);
            e.Graphics.SmoothingMode = SmoothingMode.AntiAlias;
            var rect = new Rectangle(0, 0, Width - 1, Height - 1);
            int tr = (TopRadius >= 0) ? TopRadius : CornerRadius;
            int br = (BottomRadius >= 0) ? BottomRadius : CornerRadius;
            using (GraphicsPath path = CreateAsymmetricPath(rect, tr, tr, br, br))
            using (var brush = new SolidBrush(FillColor))
            {
                e.Graphics.FillPath(brush, path);
                if (StrokeColor.A > 0)
                {
                    using (var pen = new Pen(StrokeColor, 1f))
                    {
                        e.Graphics.DrawPath(pen, path);
                    }
                }
            }
        }

        public static GraphicsPath CreateRoundedPath(Rectangle bounds, int radius)
        {
            return CreateAsymmetricPath(bounds, radius, radius, radius, radius);
        }

        public static GraphicsPath CreateAsymmetricPath(Rectangle bounds, int tl, int tr, int br, int bl)
        {
            var path = new GraphicsPath();
            int maxR = Math.Min(bounds.Width, bounds.Height) / 2;
            tl = Math.Max(0, Math.Min(tl, maxR));
            tr = Math.Max(0, Math.Min(tr, maxR));
            br = Math.Max(0, Math.Min(br, maxR));
            bl = Math.Max(0, Math.Min(bl, maxR));

            if (tl == 0 && tr == 0 && br == 0 && bl == 0)
            {
                path.AddRectangle(bounds);
                return path;
            }

            if (tl > 0) path.AddArc(bounds.X, bounds.Y, tl * 2, tl * 2, 180, 90);
            else path.AddLine(bounds.X, bounds.Y, bounds.X, bounds.Y);

            if (tr > 0) path.AddArc(bounds.Right - tr * 2, bounds.Y, tr * 2, tr * 2, 270, 90);
            else path.AddLine(bounds.Right, bounds.Y, bounds.Right, bounds.Y);

            if (br > 0) path.AddArc(bounds.Right - br * 2, bounds.Bottom - br * 2, br * 2, br * 2, 0, 90);
            else path.AddLine(bounds.Right, bounds.Bottom, bounds.Right, bounds.Bottom);

            if (bl > 0) path.AddArc(bounds.X, bounds.Bottom - bl * 2, bl * 2, bl * 2, 90, 90);
            else path.AddLine(bounds.X, bounds.Bottom, bounds.X, bounds.Bottom);

            path.CloseFigure();
            return path;
        }
    }

    internal sealed class ModernButton : Button
    {
        public Color NormalColor { get; set; }
        public Color HoverColor { get; set; }
        public Color BorderColor { get; set; }
        public int CornerRadius { get; set; }

        private bool _hovered;
        private bool _pressed;

        public ModernButton()
        {
            NormalColor = ThemeColors.BtnNeutral;
            HoverColor = ThemeColors.BtnNeutralHover;
            BorderColor = Color.Transparent;
            CornerRadius = 16;
            FlatStyle = FlatStyle.Flat;
            FlatAppearance.BorderSize = 0;
            ForeColor = ThemeColors.TextPrimary;
            Cursor = Cursors.Hand;
            SetStyle(ControlStyles.UserPaint | ControlStyles.AllPaintingInWmPaint | ControlStyles.DoubleBuffer, true);
        }

        protected override void OnMouseEnter(EventArgs e)
        {
            _hovered = true;
            Invalidate();
            base.OnMouseEnter(e);
        }

        protected override void OnMouseLeave(EventArgs e)
        {
            _hovered = false;
            _pressed = false;
            Invalidate();
            base.OnMouseLeave(e);
        }

        protected override void OnMouseDown(MouseEventArgs mevent)
        {
            _pressed = true;
            Invalidate();
            base.OnMouseDown(mevent);
        }

        protected override void OnMouseUp(MouseEventArgs mevent)
        {
            _pressed = false;
            Invalidate();
            base.OnMouseUp(mevent);
        }

        protected override void OnPaint(PaintEventArgs pevent)
        {
            Graphics g = pevent.Graphics;
            g.SmoothingMode = SmoothingMode.AntiAlias;
            g.Clear(Parent != null ? Parent.BackColor : ThemeColors.BgCard);

            Color fill = NormalColor;
            if (!Enabled) fill = ThemeColors.BtnMuted;
            else if (_pressed) fill = ControlPaint.Dark(HoverColor, 0.06f);
            else if (_hovered) fill = HoverColor;

            // Expressive tactile corner morph on press
            int r = _pressed ? Math.Max(8, CornerRadius - 4) : CornerRadius;
            var rect = new Rectangle(0, 0, Width - 1, Height - 1);
            using (GraphicsPath path = RoundedCardPanel.CreateRoundedPath(rect, r))
            using (var brush = new SolidBrush(fill))
            {
                g.FillPath(brush, path);
            }

            Color textCol = Enabled ? ForeColor : ThemeColors.TextSecondary;
            TextRenderer.DrawText(
                g,
                Text,
                Font,
                rect,
                textCol,
                TextFormatFlags.HorizontalCenter | TextFormatFlags.VerticalCenter | TextFormatFlags.EndEllipsis);
        }
    }

    internal sealed class StatusPill : Label
    {
        public Color PillBackColor { get; set; }
        public Color PillBorderColor { get; set; }

        public StatusPill()
        {
            PillBackColor = Color.FromArgb(66, 44, 5);
            PillBorderColor = Color.Transparent;
            ForeColor = ThemeColors.WarnAmber;
            AutoSize = false;
            TextAlign = ContentAlignment.MiddleCenter;
            Font = new Font("Segoe UI", 8.5f, FontStyle.Bold);
            SetStyle(ControlStyles.UserPaint | ControlStyles.AllPaintingInWmPaint | ControlStyles.DoubleBuffer, true);
        }

        protected override void OnPaint(PaintEventArgs e)
        {
            Graphics g = e.Graphics;
            g.SmoothingMode = SmoothingMode.AntiAlias;
            g.Clear(Parent != null ? Parent.BackColor : ThemeColors.BgWindow);

            var rect = new Rectangle(0, 0, Width - 1, Height - 1);
            using (GraphicsPath path = RoundedCardPanel.CreateRoundedPath(rect, (Height - 1) / 2))
            using (var brush = new SolidBrush(PillBackColor))
            {
                g.FillPath(brush, path);
            }

            TextRenderer.DrawText(
                g,
                Text,
                Font,
                rect,
                ForeColor,
                TextFormatFlags.HorizontalCenter | TextFormatFlags.VerticalCenter | TextFormatFlags.EndEllipsis);
        }
    }
}
