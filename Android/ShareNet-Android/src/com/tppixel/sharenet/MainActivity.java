package com.tppixel.sharenet;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.BroadcastReceiver;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.res.Configuration;
import android.graphics.Typeface;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.Uri;
import android.net.VpnService;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;
import android.provider.Settings;
import android.text.InputType;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.WindowInsetsController;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

import java.util.List;
import java.util.Locale;

/**
 * Google Clock-inspired multi-screen Material 3 Expressive UI:
 * - Screen 0 ("Прокси"): ultra-clean Clock aesthetic — large IP:Port readout + giant morphing Start/Stop button
 * - Screen 1 ("VPN"): large upstream IP:Port + giant morphing Start/Stop button + Wi-Fi/USB source toggle
 * - Screen 2 ("Сброс"): rollback controls, ADB/Mac/Win reset commands, and quick port/usage reference
 */
public class MainActivity extends Activity {
    private static final int REQ_VPN_PREPARE = 7001;
    private static final String GITHUB_URL = "https://github.com/TPpixel/ShareNet";

    private final Handler handler = new Handler(Looper.getMainLooper());

    private int activeTab = 0; // 0 = Прокси, 1 = VPN, 2 = Сброс
    private int lastThemeSignature = 0;

    private View[] tabPages = new View[3];
    private ExpressiveUi.NavTabBar navBar;

    // Screen 0: Proxy
    private ExpressiveUi.OdometerIpView proxyDisplayView;
    private ExpressiveUi.MorphHeroButton proxyHeroBtn;
    private boolean lastProxyRunning = false;

    // Screen 1: VPN
    private ExpressiveUi.OdometerIpView vpnDisplayView;
    private ExpressiveUi.MorphHeroButton vpnHeroBtn;
    private ExpressiveUi.StackedPillSelector presetGroup;
    private boolean lastVpnRunning = false;

    // Screen 2: Reset & Info
    private TextView resetStatusSummary;

    private String primaryAddress = "";
    private String selectedVpnEndpoint = "";
    private int selectedProxyPort = 8080;
    private boolean customVpnEndpoint = false;
    private boolean userPickedUsbPreset = false;

    private String pendingVpnHost = "";
    private int pendingVpnPort = 8080;

    private Boolean pendingProxyTarget = null;
    private long pendingProxyUntilMs = 0L;
    private Boolean pendingVpnTarget = null;
    private long pendingVpnUntilMs = 0L;
    private volatile boolean usbNetActive = false;

    private static final String ACTION_USBNET_STATE = "com.tppixel.sharenet.USBNET_STATE";
    private static final String ACTION_FORCE_STOP_USBNET = "com.genymobile.gnirehtet.FORCE_STOP_USBNET";
    private static final String ACTION_QUERY_USBNET_STATE = "com.genymobile.gnirehtet.QUERY_STATE";
    private static final String USBNET_PKG = "com.genymobile.gnirehtet";
    private static final String PERM_CONTROL_USBNET = "com.tppixel.sharenet.permission.CONTROL_USBNET";

    private final BroadcastReceiver usbNetStateReceiver = new BroadcastReceiver() {
        @Override
        public void onReceive(Context context, Intent intent) {
            if (intent == null) {
                return;
            }
            String action = intent.getAction();
            if (ACTION_USBNET_STATE.equals(action)) {
                usbNetActive = intent.getBooleanExtra("active", false);
                if (!usbNetActive && pendingVpnTarget == Boolean.TRUE) {
                    pendingVpnTarget = null;
                }
                updateUi(true);
            } else if (ProxyService.ACTION_STATE_CHANGED.equals(action)) {
                if (!ProxyService.isRunning) {
                    pendingProxyTarget = null;
                }
                if (!HotspotVpnService.isRunning && !usbNetActive) {
                    pendingVpnTarget = null;
                }
                updateUi(true);
            }
        }
    };

    private final Runnable refreshTask = new Runnable() {
        @Override
        public void run() {
            updateUi(true);
            handler.postDelayed(this, 800);
        }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        selectedProxyPort = ProxyService.currentPort;
        selectedVpnEndpoint = detectInitialEndpoint();
        buildUi();
        handleControlIntent(getIntent());
        updateUi(false);
    }

    @Override
    public void onConfigurationChanged(Configuration newConfig) {
        super.onConfigurationChanged(newConfig);
        syncThemeIfNeeded();
    }

    private void syncThemeIfNeeded() {
        int newSig = ExpressiveUi.syncWithTheme(this);
        if (newSig != lastThemeSignature) {
            buildUi();
            updateUi(false);
        }
    }

    private void buildUi() {
        lastThemeSignature = ExpressiveUi.syncWithTheme(this);
        applySystemBarsTheme();

        LinearLayout shell = new LinearLayout(this);
        shell.setOrientation(LinearLayout.VERTICAL);
        shell.setBackgroundColor(ExpressiveUi.CANVAS);
        if (Build.VERSION.SDK_INT >= 26) {
            shell.setDefaultFocusHighlightEnabled(false);
        }

        FrameLayout pageHost = new FrameLayout(this);
        LinearLayout.LayoutParams hostLp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f);

        tabPages[0] = buildProxyScreen();
        tabPages[1] = buildVpnScreen();
        tabPages[2] = buildResetScreen();

        for (int i = 0; i < tabPages.length; i++) {
            pageHost.addView(tabPages[i], new FrameLayout.LayoutParams(
                    FrameLayout.LayoutParams.MATCH_PARENT,
                    FrameLayout.LayoutParams.MATCH_PARENT));
        }

        navBar = new ExpressiveUi.NavTabBar(this, this::selectTab);

        shell.addView(pageHost, hostLp);
        shell.addView(navBar, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT));

        setContentView(shell);
        selectTab(activeTab, false);
    }

    private LinearLayout buildTopBar(String titleText, Runnable onMenuClick) {
        LinearLayout top = new LinearLayout(this);
        top.setOrientation(LinearLayout.HORIZONTAL);
        top.setGravity(Gravity.CENTER_VERTICAL);
        top.setPadding(dp(22), dp(18), dp(14), dp(8));

        TextView title = new TextView(this);
        title.setText(titleText);
        title.setTextColor(ExpressiveUi.ON_SURFACE);
        title.setTextSize(TypedValue.COMPLEX_UNIT_SP, 22);
        title.setLayoutParams(new LinearLayout.LayoutParams(
                0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
        top.addView(title);

        if (onMenuClick != null) {
            ExpressiveUi.MoreDotsView dots = new ExpressiveUi.MoreDotsView(this);
            dots.setOnClickListener(v -> onMenuClick.run());
            top.addView(dots, new LinearLayout.LayoutParams(dp(44), dp(44)));
        }
        return top;
    }

    /**
     * Screen 0 ("Прокси"): Strictly Clock aesthetic — top title, giant IP:Port readout,
     * giant morphing Start -> Red Rectangle Stop button, nothing else.
     */
    private LinearLayout buildProxyScreen() {
        LinearLayout screen = new LinearLayout(this);
        screen.setOrientation(LinearLayout.VERTICAL);

        screen.addView(buildTopBar("Раздача (Прокси)", this::showProxyOptionsDialog));

        LinearLayout center = new LinearLayout(this);
        center.setOrientation(LinearLayout.VERTICAL);
        center.setGravity(Gravity.CENTER_HORIZONTAL);
        center.setPadding(dp(16), dp(24), dp(16), dp(36));

        proxyDisplayView = new ExpressiveUi.OdometerIpView(this);
        proxyDisplayView.setPadding(dp(4), dp(8), dp(4), dp(8));
        proxyDisplayView.setOnClickListener(v -> copyAddress());

        LinearLayout.LayoutParams dispLp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(78));
        dispLp.topMargin = dp(42);
        dispLp.bottomMargin = 0;
        center.addView(proxyDisplayView, dispLp);

        View flex = new View(this);
        center.addView(flex, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f));

        proxyHeroBtn = new ExpressiveUi.MorphHeroButton(this);
        proxyHeroBtn.setLabels("Запустить", "Стоп");
        proxyHeroBtn.setOnHeroClick(this::onToggleClick);
        lastProxyRunning = ProxyService.isRunning;
        proxyHeroBtn.setActive(lastProxyRunning, false);

        LinearLayout.LayoutParams btnLp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(128));
        center.addView(proxyHeroBtn, btnLp);

        View bottomReserve = new View(this);
        center.addView(bottomReserve, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(194)));

        screen.addView(center, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f));
        return screen;
    }

    /**
     * Screen 1 ("VPN"): Same Clock aesthetic — giant upstream IP:Port readout,
     * giant morphing Start -> Red Rectangle Stop button, plus compact Wi-Fi/USB source switch.
     */
    private LinearLayout buildVpnScreen() {
        LinearLayout screen = new LinearLayout(this);
        screen.setOrientation(LinearLayout.VERTICAL);

        screen.addView(buildTopBar("Приём (VPN)", this::showVpnEndpointDialog));

        LinearLayout center = new LinearLayout(this);
        center.setOrientation(LinearLayout.VERTICAL);
        center.setGravity(Gravity.CENTER_HORIZONTAL);
        center.setPadding(dp(16), dp(24), dp(16), dp(36));

        vpnDisplayView = new ExpressiveUi.OdometerIpView(this);
        vpnDisplayView.setPadding(dp(4), dp(8), dp(4), dp(8));
        vpnDisplayView.setOnClickListener(v -> showVpnEndpointDialog());

        LinearLayout.LayoutParams dispLp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(78));
        dispLp.topMargin = dp(42);
        dispLp.bottomMargin = 0;
        center.addView(vpnDisplayView, dispLp);

        View flex = new View(this);
        center.addView(flex, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f));

        vpnHeroBtn = new ExpressiveUi.MorphHeroButton(this);
        vpnHeroBtn.setLabels("Запустить", "Стоп");
        vpnHeroBtn.setOnHeroClick(this::onVpnToggleClick);
        lastVpnRunning = HotspotVpnService.isRunning;
        vpnHeroBtn.setActive(lastVpnRunning, false);

        LinearLayout.LayoutParams btnLp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(128));
        btnLp.bottomMargin = dp(9);
        center.addView(vpnHeroBtn, btnLp);

        presetGroup = new ExpressiveUi.StackedPillSelector(
                this,
                "Wi-Fi",
                "USB",
                this::applyWifiGatewayPreset,
                () -> {
                    customVpnEndpoint = false;
                    userPickedUsbPreset = true;
                    selectedVpnEndpoint = "127.0.0.1:10808";
                    updateUi(true);
                    Toast.makeText(this, "USB: 127.0.0.1:10808", Toast.LENGTH_SHORT).show();
                });
        if (selectedVpnEndpoint.endsWith(":10808")) {
            presetGroup.setSelectedIndex(1);
        } else {
            presetGroup.setSelectedIndex(0);
        }
        center.addView(presetGroup, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT));

        screen.addView(center, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f));
        return screen;
    }

    /**
     * Screen 2 ("Утилиты"): Rollback controls at the bottom + quick status & scenario notes.
     */
    private LinearLayout buildResetScreen() {
        LinearLayout screen = new LinearLayout(this);
        screen.setOrientation(LinearLayout.VERTICAL);

        screen.addView(buildTopBar("Утилиты и сброс", null));

        ScrollView scroll = new ScrollView(this);
        scroll.setFillViewport(true);

        LinearLayout body = new LinearLayout(this);
        body.setOrientation(LinearLayout.VERTICAL);
        body.setPadding(dp(16), dp(8), dp(16), dp(36));

        LinearLayout infoCard = new LinearLayout(this);
        infoCard.setOrientation(LinearLayout.VERTICAL);
        infoCard.setPadding(dp(18), dp(18), dp(18), dp(18));
        ExpressiveUi.setUniformBg(infoCard, ExpressiveUi.SURFACE_LOW, dpf(24));

        TextView cardTitle = new TextView(this);
        cardTitle.setText("Состояние и подсказка");
        cardTitle.setTextColor(ExpressiveUi.ON_SURFACE);
        cardTitle.setTextSize(TypedValue.COMPLEX_UNIT_SP, 16.5f);
        cardTitle.setTypeface(Typeface.DEFAULT_BOLD);
        cardTitle.setPadding(0, 0, 0, dp(10));
        infoCard.addView(cardTitle);

        resetStatusSummary = new TextView(this);
        resetStatusSummary.setTextColor(ExpressiveUi.ON_SURFACE_VARIANT);
        resetStatusSummary.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13.5f);
        resetStatusSummary.setLineSpacing(dp(4), 1f);
        infoCard.addView(resetStatusSummary);

        LinearLayout githubRow = new LinearLayout(this);
        githubRow.setOrientation(LinearLayout.HORIZONTAL);
        githubRow.setGravity(Gravity.CENTER_VERTICAL);
        githubRow.setMinimumHeight(dp(48));
        githubRow.setPadding(0, dp(8), 0, dp(2));
        githubRow.setOnClickListener(v -> openGitHub());

        ImageView githubIcon = new ImageView(this);
        githubIcon.setImageResource(R.drawable.ic_github);
        githubIcon.setColorFilter(ExpressiveUi.PRIMARY);
        LinearLayout.LayoutParams ghIconLp = new LinearLayout.LayoutParams(dp(18), dp(18));
        ghIconLp.rightMargin = dp(10);
        githubIcon.setLayoutParams(ghIconLp);
        githubRow.addView(githubIcon);

        TextView githubLink = new TextView(this);
        githubLink.setText("GitHub: github.com/TPpixel/ShareNet");
        githubLink.setTextColor(ExpressiveUi.PRIMARY);
        githubLink.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13.5f);
        githubLink.setLayoutParams(new LinearLayout.LayoutParams(
                0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
        githubRow.addView(githubLink);

        ImageView githubArrow = new ImageView(this);
        githubArrow.setImageResource(R.drawable.ic_chevron_right);
        githubArrow.setColorFilter(ExpressiveUi.PRIMARY);
        githubArrow.setLayoutParams(new LinearLayout.LayoutParams(dp(18), dp(18)));
        githubRow.addView(githubArrow);

        infoCard.addView(githubRow);

        body.addView(infoCard, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT));

        View spacer = new View(this);
        body.addView(spacer, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f));

        Button copyRollbackCmdBtn = createNeutralUtilityButton("Скопировать команды\n(Mac / Win / ADB)");
        LinearLayout.LayoutParams crLp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(88));
        crLp.topMargin = dp(16);
        crLp.bottomMargin = dp(9);
        copyRollbackCmdBtn.setLayoutParams(crLp);
        copyRollbackCmdBtn.setOnClickListener(v -> copyRollbackCommands());
        body.addView(copyRollbackCmdBtn);

        Button rollbackBtn = new Button(this);
        rollbackBtn.setText("Отключить всё и откатить");
        rollbackBtn.setAllCaps(false);
        rollbackBtn.setGravity(Gravity.CENTER);
        rollbackBtn.setTextSize(TypedValue.COMPLEX_UNIT_SP, 22);
        rollbackBtn.setTypeface(Typeface.create("sans-serif", Typeface.NORMAL));
        rollbackBtn.setTextColor(ExpressiveUi.ON_ERROR_HERO);
        rollbackBtn.setPadding(dp(20), 0, dp(20), 0);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            rollbackBtn.setStateListAnimator(null);
        }
        float pillR = dpf(44);
        ExpressiveUi.attachRippleButton(rollbackBtn, ExpressiveUi.ERROR_HERO, pillR, ExpressiveUi.ON_ERROR_HERO, 26);
        rollbackBtn.setOnClickListener(v -> performFullRollback());
        rollbackBtn.setLayoutParams(new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(88)));
        body.addView(rollbackBtn);

        scroll.addView(body);
        screen.addView(scroll, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f));
        return screen;
    }

    private void selectTab(int index) {
        selectTab(index, true);
    }

    private void selectTab(int index, boolean animateTab) {
        activeTab = index;
        if (navBar != null) {
            navBar.setSelectedTab(index, animateTab);
        }
        for (int i = 0; i < tabPages.length; i++) {
            if (tabPages[i] != null) {
                tabPages[i].setVisibility(i == index ? View.VISIBLE : View.GONE);
            }
        }
    }

    private void applySystemBarsTheme() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            getWindow().setStatusBarColor(ExpressiveUi.CANVAS);
            getWindow().setNavigationBarColor(ExpressiveUi.SURFACE);
            if (Build.VERSION.SDK_INT >= 28) {
                getWindow().setNavigationBarDividerColor(ExpressiveUi.SURFACE);
            }
            if (Build.VERSION.SDK_INT >= 29) {
                getWindow().setNavigationBarContrastEnforced(false);
            }
        }
        boolean lightMode = !ExpressiveUi.IS_DARK;
        View decor = getWindow().getDecorView();
        if (Build.VERSION.SDK_INT >= 30) {
            WindowInsetsController wic = decor.getWindowInsetsController();
            if (wic != null) {
                int mask = WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS
                        | WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS;
                wic.setSystemBarsAppearance(lightMode ? mask : 0, mask);
            }
        } else if (Build.VERSION.SDK_INT >= 23) {
            int flags = decor.getSystemUiVisibility();
            if (lightMode) {
                flags |= View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR;
                if (Build.VERSION.SDK_INT >= 26) {
                    flags |= View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR;
                }
            } else {
                flags &= ~View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR;
                if (Build.VERSION.SDK_INT >= 26) {
                    flags &= ~View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR;
                }
            }
            decor.setSystemUiVisibility(flags);
        }
    }

    private void showProxyOptionsDialog() {
        String[] options = new String[]{
                "Скопировать " + (primaryAddress.isEmpty() ? ("0.0.0.0:" + selectedProxyPort) : primaryAddress),
                "Изменить порт (" + selectedProxyPort + ")"
        };
        new AlertDialog.Builder(this)
                .setTitle("Настройки прокси")
                .setItems(options, (d, which) -> {
                    if (which == 0) {
                        copyAddress();
                    } else {
                        showPortInputDialog();
                    }
                })
                .show();
    }

    private void showPortInputDialog() {
        EditText input = new EditText(this);
        input.setInputType(InputType.TYPE_CLASS_NUMBER);
        input.setText(String.valueOf(selectedProxyPort));
        input.setSelection(input.getText().length());

        new AlertDialog.Builder(this)
                .setTitle("Порт раздачи")
                .setView(input)
                .setPositiveButton("Сохранить", (d, w) -> {
                    int p = selectedProxyPort;
                    try {
                        p = Integer.parseInt(input.getText().toString().trim());
                    } catch (NumberFormatException ignored) {
                    }
                    if (p < 1024 || p > 65535) {
                        Toast.makeText(this, "Порт от 1024 до 65535", Toast.LENGTH_SHORT).show();
                        return;
                    }
                    selectedProxyPort = p;
                    updateUi(true);
                })
                .setNegativeButton("Отмена", null)
                .show();
    }

    private void showVpnEndpointDialog() {
        if (HotspotVpnService.isRunning) {
            Toast.makeText(this, "Сначала остановите VPN", Toast.LENGTH_SHORT).show();
            return;
        }
        EditText input = new EditText(this);
        input.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        input.setText(selectedVpnEndpoint);
        input.setSelection(input.getText().length());

        new AlertDialog.Builder(this)
                .setTitle("Прокси-источник (IP:Порт)")
                .setView(input)
                .setPositiveButton("Применить", (d, w) -> {
                    String val = input.getText().toString().trim();
                    if (!val.isEmpty()) {
                        customVpnEndpoint = true;
                        userPickedUsbPreset = val.endsWith(":10808");
                        selectedVpnEndpoint = val;
                        if (presetGroup != null) {
                            presetGroup.setSelectedIndex(val.endsWith(":10808") ? 1 : 0);
                        }
                        updateUi(true);
                    }
                })
                .setNegativeButton("Отмена", null)
                .show();
    }

    private String detectInitialEndpoint() {
        if (HotspotVpnService.isRunning && !HotspotVpnService.currentUpstreamHost.isEmpty()) {
            return HotspotVpnService.currentUpstreamHost + ":" + HotspotVpnService.currentUpstreamPort;
        }
        String gw = NetUtils.detectWifiGateway(this);
        if (!gw.isEmpty()) {
            return gw + ":8080";
        }
        return "127.0.0.1:10808";
    }

    private void applyWifiGatewayPreset() {
        customVpnEndpoint = false;
        userPickedUsbPreset = false;
        String gw = NetUtils.detectWifiGateway(this);
        if (gw.isEmpty()) {
            Toast.makeText(this, "Wi-Fi шлюз не найден — подключитесь к Wi-Fi или задайте IP через ⋮", Toast.LENGTH_SHORT).show();
            return;
        }
        selectedVpnEndpoint = gw + ":8080";
        updateUi(true);
        Toast.makeText(this, "Wi-Fi: " + selectedVpnEndpoint, Toast.LENGTH_SHORT).show();
    }

    private void onVpnToggleClick() {
        boolean usbRunning = isUsbNetVpnRunning();
        boolean actualOn = HotspotVpnService.isRunning || usbRunning;
        boolean currentlyOn = (pendingVpnTarget != null) ? pendingVpnTarget : actualOn;
        if (currentlyOn) {
            pendingVpnTarget = Boolean.FALSE;
            pendingVpnUntilMs = SystemClock.uptimeMillis() + 1500L;
            updateVpnScreen(true);

            stopHotspotVpnService();
            stopUsbNetApp();
            handler.postDelayed(() -> updateUi(true), 250);
            return;
        }

        String raw = selectedVpnEndpoint == null ? "" : selectedVpnEndpoint.trim();
        if (raw.isEmpty()) {
            raw = detectInitialEndpoint();
            selectedVpnEndpoint = raw;
        }
        String host = raw;
        int port = 8080;
        int colon = raw.lastIndexOf(':');
        if (colon > 0 && colon < raw.length() - 1) {
            host = raw.substring(0, colon).trim();
            try {
                port = Integer.parseInt(raw.substring(colon + 1).trim());
            } catch (NumberFormatException ignored) {
            }
        } else if ("127.0.0.1".equals(host)) {
            port = 10808;
        }
        if (host.isEmpty() || port < 1 || port > 65535) {
            Toast.makeText(this, "Укажите корректный IP:Порт", Toast.LENGTH_SHORT).show();
            return;
        }

        launchHotspotVpn(host, port);
    }

    private void launchHotspotVpn(String host, int port) {
        pendingVpnHost = host;
        pendingVpnPort = port;
        Intent prepareIntent = VpnService.prepare(this);
        if (prepareIntent != null) {
            startActivityForResult(prepareIntent, REQ_VPN_PREPARE);
            return;
        }
        startHotspotVpnService(host, port);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == REQ_VPN_PREPARE && resultCode == RESULT_OK) {
            startHotspotVpnService(pendingVpnHost, pendingVpnPort);
        } else if (requestCode == REQ_VPN_PREPARE) {
            Toast.makeText(this, "Разрешение на VPN не выдано", Toast.LENGTH_SHORT).show();
        }
    }

    private void startHotspotVpnService(String host, int port) {
        pendingVpnHost = host;
        pendingVpnPort = port;
        pendingVpnTarget = Boolean.TRUE;
        pendingVpnUntilMs = SystemClock.uptimeMillis() + 1500L;
        updateVpnScreen(true);

        Intent startIntent = new Intent(this, HotspotVpnService.class);
        startIntent.setAction(HotspotVpnService.ACTION_START);
        startIntent.putExtra(HotspotVpnService.EXTRA_PROXY_HOST, host);
        startIntent.putExtra(HotspotVpnService.EXTRA_PROXY_PORT, port);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(startIntent);
        } else {
            startService(startIntent);
        }
        handler.postDelayed(() -> updateUi(true), 250);
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        handleControlIntent(intent);
        updateUi(true);
    }

    private void handleControlIntent(Intent intent) {
        if (intent == null) {
            return;
        }
        boolean hasControlExtra = intent.hasExtra("stopall")
                || intent.hasExtra("autostop")
                || intent.hasExtra("autostart");
        if (!hasControlExtra) {
            return;
        }
        if (Build.VERSION.SDK_INT >= 34) {
            int callerUid = getLaunchedFromUid();
            if (callerUid >= 10000 && callerUid != android.os.Process.myUid()) {
                return;
            }
        }
        if (intent.getBooleanExtra("stopall", false)) {
            performFullRollback();
            return;
        }
        if (intent.getBooleanExtra("autostop", false)) {
            if (ProxyService.isRunning) {
                stopProxyService();
            }
            if (intent.getBooleanExtra("stopvpn", false)) {
                stopHotspotVpnService();
                stopUsbNetApp();
            }
            handler.postDelayed(() -> updateUi(true), 200);
            return;
        }
        if (intent.getBooleanExtra("autostart", false)) {
            int port = intent.getIntExtra(ProxyService.EXTRA_PORT, 8080);
            if (port < 1024 || port > 65535) {
                port = 8080;
            }
            selectedProxyPort = port;
            if (!ProxyService.isRunning) {
                startProxyService(port);
            }
        }
    }

    @Override
    protected void onResume() {
        super.onResume();
        syncThemeIfNeeded();
        IntentFilter usbFilter = new IntentFilter();
        usbFilter.addAction(ACTION_USBNET_STATE);
        usbFilter.addAction(ProxyService.ACTION_STATE_CHANGED);
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(usbNetStateReceiver, usbFilter, PERM_CONTROL_USBNET, null, Context.RECEIVER_EXPORTED);
        } else {
            registerReceiver(usbNetStateReceiver, usbFilter, PERM_CONTROL_USBNET, null);
        }
        queryUsbNetState();
        updateUi(false);
        handler.post(refreshTask);
    }

    private void queryUsbNetState() {
        try {
            Intent i = new Intent(ACTION_QUERY_USBNET_STATE);
            i.setPackage(USBNET_PKG);
            sendBroadcast(i, PERM_CONTROL_USBNET);
        } catch (Exception ignored) {
        }
    }

    @Override
    protected void onPause() {
        handler.removeCallbacks(refreshTask);
        try {
            unregisterReceiver(usbNetStateReceiver);
        } catch (IllegalArgumentException ignored) {
        }
        super.onPause();
    }

    private void onToggleClick() {
        boolean currentlyOn = (pendingProxyTarget != null) ? pendingProxyTarget : ProxyService.isRunning;
        if (currentlyOn) {
            pendingProxyTarget = Boolean.FALSE;
            pendingProxyUntilMs = SystemClock.uptimeMillis() + 1500L;
            updateProxyScreen(true);

            stopProxyService();
            handler.postDelayed(() -> updateUi(true), 220);
        } else {
            int port = selectedProxyPort;
            if (port < 1024 || port > 65535) {
                port = 8080;
                selectedProxyPort = 8080;
            }
            pendingProxyTarget = Boolean.TRUE;
            pendingProxyUntilMs = SystemClock.uptimeMillis() + 1500L;
            updateProxyScreen(true);

            startProxyService(port);
        }
    }

    private void startProxyService(int port) {
        Intent startIntent = new Intent(this, ProxyService.class);
        startIntent.putExtra(ProxyService.EXTRA_PORT, port);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(startIntent);
        } else {
            startService(startIntent);
        }
        handler.postDelayed(() -> updateUi(true), 250);
    }

    private void stopProxyService() {
        try {
            stopService(new Intent(this, ProxyService.class));
        } catch (Exception ignored) {
        }
    }

    private void stopHotspotVpnService() {
        HotspotVpnService.requestStop(this);
    }

    private void stopUsbNetApp() {
        usbNetActive = false;
        try {
            Intent i = new Intent(ACTION_FORCE_STOP_USBNET);
            i.setPackage(USBNET_PKG);
            i.addFlags(Intent.FLAG_INCLUDE_STOPPED_PACKAGES);
            sendBroadcast(i, PERM_CONTROL_USBNET);
        } catch (Exception ignored) {
        }
    }

    private boolean isUsbNetVpnRunning() {
        if (!isSystemVpnActive()) {
            usbNetActive = false;
            return false;
        }
        return usbNetActive;
    }

    private void copyAddress() {
        if (primaryAddress.isEmpty()) {
            Toast.makeText(this, "Включите Точку доступа (Hotspot), чтобы появился IP", Toast.LENGTH_SHORT).show();
            return;
        }
        ClipboardManager cm = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
        if (cm != null) {
            cm.setPrimaryClip(ClipData.newPlainText("ShareNet", primaryAddress));
            Toast.makeText(this, "Скопировано: " + primaryAddress, Toast.LENGTH_SHORT).show();
        }
    }

    private void performFullRollback() {
        pendingProxyTarget = Boolean.FALSE;
        pendingProxyUntilMs = SystemClock.uptimeMillis() + 1500L;
        pendingVpnTarget = Boolean.FALSE;
        pendingVpnUntilMs = SystemClock.uptimeMillis() + 1500L;

        boolean hadUsbNet = isUsbNetVpnRunning();
        boolean hadOurVpn = HotspotVpnService.isRunning;
        boolean hasOtherVpn = !hadOurVpn && !hadUsbNet && isSystemVpnActive();

        stopUsbNetApp();
        stopHotspotVpnService();
        HotspotVpnService.lastError = "";

        boolean revokedOtherVpn = false;
        boolean otherVpnRemains = false;
        if (hasOtherVpn && VpnService.prepare(this) == null) {
            Intent revokeIntent = new Intent(this, HotspotVpnService.class);
            revokeIntent.setAction(HotspotVpnService.ACTION_REVOKE_OTHER);
            try {
                startService(revokeIntent);
                revokedOtherVpn = true;
            } catch (Exception ignored) {
            }
        } else if (hasOtherVpn) {
            otherVpnRemains = true;
        }

        stopProxyService();
        ProxyService.lastError = "";

        DohResolver.clearCache();

        String sysProxy = null;
        try {
            sysProxy = Settings.Global.getString(getContentResolver(), Settings.Global.HTTP_PROXY);
        } catch (Exception ignored) {
        }
        if (sysProxy != null && !sysProxy.trim().isEmpty()) {
            try {
                Settings.Global.putString(getContentResolver(), Settings.Global.HTTP_PROXY, "");
                Settings.Global.putString(getContentResolver(), "global_http_proxy_host", "");
                Settings.Global.putString(getContentResolver(), "global_http_proxy_port", "");
                Settings.Global.putString(getContentResolver(), "global_http_proxy_exclusion_list", "");
            } catch (SecurityException se) {
                Toast.makeText(this, "Обнаружен ручной системный прокси (" + sysProxy + ") — сбросьте в настройках Wi-Fi или по ADB", Toast.LENGTH_LONG).show();
            } catch (Exception ignored) {
            }
        }

        String msg;
        if (otherVpnRemains) {
            msg = "Прокси :8080, ShareNet и UsbNet остановлены. Обнаружен сторонний VPN — отключите в его приложении.";
        } else if (revokedOtherVpn || hadUsbNet) {
            msg = "Откат выполнен: все VPN (ShareNet/UsbNet), прокси :8080 и DoH-кэш отключены";
        } else {
            msg = "Откат выполнен: ShareNet VPN, прокси :8080 и DoH-кэш отключены";
        }
        Toast.makeText(this, msg, Toast.LENGTH_SHORT).show();
        handler.postDelayed(() -> updateUi(true), 300);
    }

    private boolean isSystemVpnActive() {
        try {
            ConnectivityManager cm = (ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE);
            if (cm == null) return false;
            Network[] nets = cm.getAllNetworks();
            if (nets == null) return false;
            for (Network n : nets) {
                NetworkCapabilities nc = cm.getNetworkCapabilities(n);
                if (nc != null && nc.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) {
                    return true;
                }
            }
        } catch (Exception ignored) {
        }
        return false;
    }

    private void openGitHub() {
        try {
            Intent i = new Intent(Intent.ACTION_VIEW, Uri.parse(GITHUB_URL));
            startActivity(i);
        } catch (Exception e) {
            Toast.makeText(this, GITHUB_URL, Toast.LENGTH_SHORT).show();
        }
    }

    private void copyRollbackCommands() {
        String cmds = "# macOS (сброс прокси, DNS и sing-box):\n"
                + "sudo pkill -INT -f hotspot-vpn-singbox.json; "
                + "sudo networksetup -setdnsservers Wi-Fi Empty; "
                + "networksetup -setwebproxystate Wi-Fi off; "
                + "networksetup -setsecurewebproxystate Wi-Fi off; "
                + "networksetup -setsocksfirewallproxystate Wi-Fi off; "
                + "sudo dscacheutil -flushcache; sudo killall -HUP mDNSResponder\n\n"
                + "# Windows (сброс прокси и DNS):\n"
                + "netsh winhttp reset proxy & reg add \"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings\" /v ProxyEnable /t REG_DWORD /d 0 /f & ipconfig /flushdns\n\n"
                + "# Android через ADB (если ставился системный прокси):\n"
                + "adb shell settings delete global http_proxy && adb shell settings delete global global_http_proxy_host && adb shell settings delete global global_http_proxy_port && adb shell am force-stop com.genymobile.gnirehtet";
        ClipboardManager cm = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
        if (cm != null) {
            cm.setPrimaryClip(ClipData.newPlainText("ShareNet Rollback", cmds));
            Toast.makeText(this, "Команды отката для Mac / Win / ADB скопированы", Toast.LENGTH_SHORT).show();
        }
    }

    private void updateUi(boolean animate) {
        updateProxyScreen(animate);
        updateVpnScreen(animate);
        updateResetScreen();
    }

    private void updateProxyScreen(boolean animate) {
        boolean actual = ProxyService.isRunning;
        boolean running = actual;
        if (pendingProxyTarget != null) {
            if (actual == pendingProxyTarget || SystemClock.uptimeMillis() > pendingProxyUntilMs) {
                pendingProxyTarget = null;
            } else {
                running = pendingProxyTarget;
            }
        }
        int port = actual ? ProxyService.currentPort : selectedProxyPort;

        if (proxyHeroBtn != null) {
            proxyHeroBtn.setActive(running, animate && (running != lastProxyRunning));
        }
        lastProxyRunning = running;

        List<String> ips = NetUtils.detectHotspotIps(this);
        String targetAddr;
        if (ips.isEmpty()) {
            primaryAddress = "";
            targetAddr = "0.0.0.0:" + port;
        } else {
            String firstIp = ips.get(0).split(" ")[0];
            primaryAddress = firstIp + ":" + port;
            targetAddr = primaryAddress;
        }
        if (proxyDisplayView != null) {
            proxyDisplayView.update(running, targetAddr, animate);
        }
    }

    private void updateVpnScreen(boolean animate) {
        boolean usbRunning = !HotspotVpnService.isRunning && isUsbNetVpnRunning();
        boolean actual = HotspotVpnService.isRunning || usbRunning;
        boolean vpnOn = actual;
        if (pendingVpnTarget != null) {
            if (actual == pendingVpnTarget || SystemClock.uptimeMillis() > pendingVpnUntilMs) {
                pendingVpnTarget = null;
            } else {
                vpnOn = pendingVpnTarget;
            }
        }
        if (vpnHeroBtn != null) {
            vpnHeroBtn.setActive(vpnOn, animate && (vpnOn != lastVpnRunning));
        }
        lastVpnRunning = vpnOn;

        if (!vpnOn && !customVpnEndpoint && !userPickedUsbPreset) {
            String liveGw = NetUtils.detectWifiGateway(this);
            if (!liveGw.isEmpty()) {
                selectedVpnEndpoint = liveGw + ":8080";
                if (presetGroup != null) {
                    presetGroup.setSelectedIndex(0);
                }
            } else {
                selectedVpnEndpoint = "127.0.0.1:10808";
                if (presetGroup != null) {
                    presetGroup.setSelectedIndex(1);
                }
            }
        }

        String shown;
        if (HotspotVpnService.isRunning) {
            shown = HotspotVpnService.currentUpstreamHost + ":" + HotspotVpnService.currentUpstreamPort;
        } else if (usbRunning) {
            shown = "127.0.0.1:10808";
        } else if (vpnOn && !pendingVpnHost.isEmpty()) {
            shown = pendingVpnHost + ":" + pendingVpnPort;
        } else {
            shown = selectedVpnEndpoint;
        }
        if (vpnDisplayView != null) {
            vpnDisplayView.update(vpnOn, shown, animate);
        }
        if (presetGroup != null) {
            presetGroup.setEnabled(!vpnOn);
        }
    }

    private void updateResetScreen() {
        if (resetStatusSummary == null) return;
        String proxyState = ProxyService.isRunning
                ? ("активен (" + (primaryAddress.isEmpty() ? (":" + ProxyService.currentPort) : primaryAddress)
                   + ", подключений: " + ProxyService.activeConnections.get() + ")")
                : "остановлен";

        String vpnState;
        if (HotspotVpnService.isRunning) {
            long[] stats = HotspotVpnService.getTrafficStats();
            String tr = (stats != null && stats.length >= 4)
                    ? (" · ↑" + formatBytes(stats[1]) + " ↓" + formatBytes(stats[3]))
                    : "";
            vpnState = "активен (" + HotspotVpnService.currentUpstreamHost + ":" + HotspotVpnService.currentUpstreamPort + tr + ")";
        } else if (isUsbNetVpnRunning()) {
            vpnState = "активен (UsbNet по USB)";
        } else if (isSystemVpnActive()) {
            vpnState = "активен (системный / сторонний VPN)";
        } else {
            vpnState = "отключён";
        }

        resetStatusSummary.setText(
                "• Прокси раздачи: " + proxyState + "\n"
                + "• VPN телефона: " + vpnState + "\n\n"
                + "Полный откат выключает собственный VPN и прокси :8080, сбрасывает кэш DoH и при наличии права отзывает сторонний USB-VPN.");
    }

    private Button createNeutralUtilityButton(String text) {
        Button b = new Button(this);
        b.setText(text);
        b.setAllCaps(false);
        b.setGravity(Gravity.CENTER);
        b.setTextSize(TypedValue.COMPLEX_UNIT_SP, 20);
        b.setTypeface(Typeface.create("sans-serif", Typeface.NORMAL));
        b.setTextColor(ExpressiveUi.ON_SURFACE);
        b.setPadding(dp(20), 0, dp(20), 0);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            b.setStateListAnimator(null);
        }
        ExpressiveUi.attachRippleButton(b, ExpressiveUi.SURFACE_HIGHEST, dpf(44), ExpressiveUi.ON_SURFACE, 26);
        return b;
    }

    private static String formatBytes(long bytes) {
        if (bytes < 1024) return bytes + " Б";
        if (bytes < 1024 * 1024) return String.format(Locale.ROOT, "%.1f КБ", bytes / 1024.0);
        return String.format(Locale.ROOT, "%.2f МБ", bytes / (1024.0 * 1024.0));
    }

    private int dp(int value) {
        return ExpressiveUi.dp(this, value);
    }

    private float dpf(float value) {
        return ExpressiveUi.dpf(this, value);
    }
}
